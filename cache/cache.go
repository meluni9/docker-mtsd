package cache

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CacheEntry represents a cached HTTP response
type CacheEntry struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
	TTL        time.Duration
	CreatedAt  time.Time
}

// IsExpired checks if the cache entry has expired
func (e *CacheEntry) IsExpired() bool {
	return time.Since(e.CreatedAt) > e.TTL
}

// ResponseCache is a simple in-memory cache for HTTP responses
type ResponseCache struct {
	mu       sync.RWMutex
	cache    map[string]*CacheEntry
	ttl      time.Duration
	maxSize  int
	order    []string // Track insertion order for LRU eviction
}

// NewResponseCache creates a new response cache
func NewResponseCache(ttl time.Duration, maxSize int) *ResponseCache {
	rc := &ResponseCache{
		cache:   make(map[string]*CacheEntry),
		ttl:     ttl,
		maxSize: maxSize,
		order:   make([]string, 0, maxSize),
	}
	
	// Start cleanup goroutine
	go rc.cleanupExpired()
	
	return rc
}

// generateKey creates a cache key from the request
func (rc *ResponseCache) generateKey(req *http.Request) string {
	// Use URL path and query string for the key
	key := req.URL.Path
	if req.URL.RawQuery != "" {
		key += "?" + req.URL.RawQuery
	}
	
	// Create MD5 hash for consistent key length
	hasher := md5.New()
	hasher.Write([]byte(key))
	return hex.EncodeToString(hasher.Sum(nil))
}

// Get retrieves a cache entry
func (rc *ResponseCache) Get(req *http.Request) (*CacheEntry, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	
	key := rc.generateKey(req)
	entry, exists := rc.cache[key]
	
	if !exists || entry.IsExpired() {
		return nil, false
	}
	
	return entry, true
}

// Set stores a response in the cache
func (rc *ResponseCache) Set(req *http.Request, statusCode int, headers http.Header, body []byte) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	
	key := rc.generateKey(req)
	
	// Determine TTL from Cache-Control header or use default
	ttl := rc.ttl
	if cc := headers.Get("Cache-Control"); cc != "" {
		if maxAge := extractMaxAge(cc); maxAge > 0 {
			ttl = time.Duration(maxAge) * time.Second
		}
	}
	
	entry := &CacheEntry{
		StatusCode: statusCode,
		Headers:    headers.Clone(),
		Body:       body,
		TTL:        ttl,
		CreatedAt:  time.Now(),
	}
	
	// Check if we need to evict old entries
	if len(rc.cache) >= rc.maxSize {
		// Remove the oldest entry
		if len(rc.order) > 0 {
			oldestKey := rc.order[0]
			delete(rc.cache, oldestKey)
			rc.order = rc.order[1:]
		}
	}
	
	rc.cache[key] = entry
	
	// Update order tracking
	// Remove key if it already exists in order
	for i, k := range rc.order {
		if k == key {
			rc.order = append(rc.order[:i], rc.order[i+1:]...)
			break
		}
	}
	rc.order = append(rc.order, key)
}

// ShouldCache determines if a request/response should be cached
func (rc *ResponseCache) ShouldCache(req *http.Request, statusCode int) bool {
	// Only cache GET requests
	if req.Method != http.MethodGet {
		return false
	}
	
	// Only cache successful responses
	if statusCode != http.StatusOK {
		return false
	}
	
	// Don't cache requests with Authorization header
	if req.Header.Get("Authorization") != "" {
		return false
	}
	
	// Check Cache-Control header
	if cc := req.Header.Get("Cache-Control"); cc != "" {
		if strings.Contains(cc, "no-cache") || strings.Contains(cc, "no-store") {
			return false
		}
	}
	
	return true
}

// Clear removes all entries from the cache
func (rc *ResponseCache) Clear() {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	
	rc.cache = make(map[string]*CacheEntry)
	rc.order = make([]string, 0, rc.maxSize)
}

// GetStats returns cache statistics
func (rc *ResponseCache) GetStats() map[string]interface{} {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	
	return map[string]interface{}{
		"enabled":   true,
		"ttl":       rc.ttl.String(),
		"max_size":  rc.maxSize,
		"current":   len(rc.cache),
	}
}

// cleanupExpired periodically removes expired entries
func (rc *ResponseCache) cleanupExpired() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	
	for range ticker.C {
		rc.mu.Lock()
		
		// Find and remove expired entries
		var expiredKeys []string
		for key, entry := range rc.cache {
			if entry.IsExpired() {
				expiredKeys = append(expiredKeys, key)
			}
		}
		
		for _, key := range expiredKeys {
			delete(rc.cache, key)
			// Remove from order tracking
			for i, k := range rc.order {
				if k == key {
					rc.order = append(rc.order[:i], rc.order[i+1:]...)
					break
				}
			}
		}
		
		rc.mu.Unlock()
	}
}

// Middleware creates HTTP middleware for caching
func (rc *ResponseCache) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if we have a cached response
		if entry, found := rc.Get(r); found {
			// Serve from cache
			for k, v := range entry.Headers {
				w.Header()[k] = v
			}
			w.Header().Set("X-Cache", "HIT")
			w.Header().Set("Age", fmt.Sprintf("%.0f", time.Since(entry.CreatedAt).Seconds()))
			w.Header().Set("X-Cache-TTL", fmt.Sprintf("%.0f", entry.TTL.Seconds()))
			
			w.WriteHeader(entry.StatusCode)
			w.Write(entry.Body)
			return
		}
		
		// Cache miss - always set X-Cache: MISS header
		w.Header().Set("X-Cache", "MISS")
		
		// Check if we should cache this response
		if rc.ShouldCache(r, http.StatusOK) {
			capture := NewResponseCapture(w)
			next.ServeHTTP(capture, r)
			
			statusCode, headers, body := capture.GetCapturedData()
			if rc.ShouldCache(r, statusCode) {
				rc.Set(r, statusCode, headers, body)
			}
		} else {
			next.ServeHTTP(w, r)
		}
	})
}

// ResponseCapture wraps http.ResponseWriter to capture the response
type ResponseCapture struct {
	http.ResponseWriter
	statusCode int
	headers    http.Header
	body       *bytes.Buffer
	written    bool
}

// NewResponseCapture creates a new response capture wrapper
func NewResponseCapture(w http.ResponseWriter) *ResponseCapture {
	return &ResponseCapture{
		ResponseWriter: w,
		headers:        w.Header(), // Use the original header map
		body:           new(bytes.Buffer),
		statusCode:     http.StatusOK, // Default status
	}
}

// Write captures the response body
func (rc *ResponseCapture) Write(b []byte) (int, error) {
	rc.body.Write(b)
	return rc.ResponseWriter.Write(b)
}

// WriteHeader captures the status code
func (rc *ResponseCapture) WriteHeader(statusCode int) {
	rc.statusCode = statusCode
	rc.ResponseWriter.WriteHeader(statusCode)
}

// GetCapturedData returns the captured response data
func (rc *ResponseCapture) GetCapturedData() (int, http.Header, []byte) {
	// Headers are already in the original ResponseWriter
	return rc.statusCode, rc.Header(), rc.body.Bytes()
}

// extractMaxAge extracts max-age value from Cache-Control header
func extractMaxAge(cacheControl string) int {
	parts := strings.Split(cacheControl, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "max-age=") {
			ageStr := strings.TrimPrefix(part, "max-age=")
			if age, err := strconv.Atoi(ageStr); err == nil {
				return age
			}
		}
	}
	return 0
}