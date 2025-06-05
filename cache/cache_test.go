package cache

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResponseCache_GetSet(t *testing.T) {
	cache := NewResponseCache(5*time.Minute, 100)
	
	req := httptest.NewRequest("GET", "/test", nil)
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	body := []byte(`{"test": "data"}`)
	
	// Cache miss initially
	if _, found := cache.Get(req); found {
		t.Error("Expected cache miss for new request")
	}
	
	// Set cache entry
	cache.Set(req, http.StatusOK, headers, body)
	
	// Cache hit
	entry, found := cache.Get(req)
	if !found {
		t.Error("Expected cache hit after setting entry")
	}
	
	if entry.StatusCode != http.StatusOK {
		t.Errorf("Expected status code 200, got %d", entry.StatusCode)
	}
	
	if string(entry.Body) != string(body) {
		t.Errorf("Expected body %s, got %s", string(body), string(entry.Body))
	}
}

func TestResponseCache_Expiry(t *testing.T) {
	cache := NewResponseCache(100*time.Millisecond, 100)
	
	req := httptest.NewRequest("GET", "/test", nil)
	headers := make(http.Header)
	body := []byte(`{"test": "data"}`)
	
	// Set cache entry
	cache.Set(req, http.StatusOK, headers, body)
	
	// Should be found immediately
	if _, found := cache.Get(req); !found {
		t.Error("Expected cache hit immediately after setting")
	}
	
	// Wait for expiry
	time.Sleep(150 * time.Millisecond)
	
	// Should be expired now
	if _, found := cache.Get(req); found {
		t.Error("Expected cache miss after expiry")
	}
}

func TestResponseCache_ShouldCache(t *testing.T) {
	cache := NewResponseCache(5*time.Minute, 100)
	
	tests := []struct {
		name       string
		method     string
		headers    map[string]string
		statusCode int
		shouldCache bool
	}{
		{
			name:        "GET request with 200 status",
			method:      "GET",
			statusCode:  http.StatusOK,
			shouldCache: true,
		},
		{
			name:        "POST request",
			method:      "POST",
			statusCode:  http.StatusOK,
			shouldCache: false,
		},
		{
			name:        "GET with 404 status",
			method:      "GET",
			statusCode:  http.StatusNotFound,
			shouldCache: false,
		},
		{
			name:        "GET with Authorization header",
			method:      "GET",
			statusCode:  http.StatusOK,
			headers:     map[string]string{"Authorization": "Bearer token"},
			shouldCache: false,
		},
		{
			name:        "GET with Cache-Control: no-cache",
			method:      "GET",
			statusCode:  http.StatusOK,
			headers:     map[string]string{"Cache-Control": "no-cache"},
			shouldCache: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/test", nil)
			
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			
			result := cache.ShouldCache(req, tt.statusCode)
			if result != tt.shouldCache {
				t.Errorf("Expected ShouldCache=%t, got %t", tt.shouldCache, result)
			}
		})
	}
}

func TestResponseCache_Middleware(t *testing.T) {
	cache := NewResponseCache(5*time.Minute, 100)
	
	callCount := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"call": ` + fmt.Sprintf("%d", callCount) + `}`))
	})
	
	middleware := cache.Middleware(handler)
	
	// First request - cache miss
	req1 := httptest.NewRequest("GET", "/test", nil)
	w1 := httptest.NewRecorder()
	middleware.ServeHTTP(w1, req1)
	
	if w1.Header().Get("X-Cache") != "MISS" {
		t.Error("Expected X-Cache: MISS for first request")
	}
	
	if callCount != 1 {
		t.Errorf("Expected handler to be called once, got %d", callCount)
	}
	
	// Second identical request - cache hit
	req2 := httptest.NewRequest("GET", "/test", nil)
	w2 := httptest.NewRecorder()
	middleware.ServeHTTP(w2, req2)
	
	if w2.Header().Get("X-Cache") != "HIT" {
		t.Error("Expected X-Cache: HIT for second request")
	}
	
	if callCount != 1 {
		t.Errorf("Expected handler to be called once, got %d", callCount)
	}
	
	// Responses should be identical
	if w1.Body.String() != w2.Body.String() {
		t.Error("Cached response should be identical to original")
	}
}

func TestResponseCache_MaxAge(t *testing.T) {
	cache := NewResponseCache(1*time.Hour, 100) // Default 1 hour
	
	req := httptest.NewRequest("GET", "/test", nil)
	headers := make(http.Header)
	headers.Set("Cache-Control", "max-age=300") // 5 minutes
	body := []byte(`{"test": "data"}`)
	
	cache.Set(req, http.StatusOK, headers, body)
	
	entry, found := cache.Get(req)
	if !found {
		t.Error("Expected to find cached entry")
	}
	
	// TTL should be 5 minutes, not 1 hour
	expectedTTL := 5 * time.Minute
	if entry.TTL != expectedTTL {
		t.Errorf("Expected TTL %v, got %v", expectedTTL, entry.TTL)
	}
}

func TestResponseCache_KeyGeneration(t *testing.T) {
	cache := NewResponseCache(5*time.Minute, 100)
	
	// Different paths should generate different keys
	req1 := httptest.NewRequest("GET", "/path1", nil)
	req2 := httptest.NewRequest("GET", "/path2", nil)
	
	key1 := cache.generateKey(req1)
	key2 := cache.generateKey(req2)
	
	if key1 == key2 {
		t.Error("Different paths should generate different cache keys")
	}
	
	// Same path should generate same key
	req3 := httptest.NewRequest("GET", "/path1", nil)
	key3 := cache.generateKey(req3)
	
	if key1 != key3 {
		t.Error("Same paths should generate same cache keys")
	}
	
	// Query parameters should affect key
	req4 := httptest.NewRequest("GET", "/path1?param=value", nil)
	key4 := cache.generateKey(req4)
	
	if key1 == key4 {
		t.Error("Query parameters should affect cache key")
	}
}

func TestResponseCapture(t *testing.T) {
	w := httptest.NewRecorder()
	capture := NewResponseCapture(w)
	
	// Test header setting
	capture.Header().Set("Content-Type", "application/json")
	capture.WriteHeader(http.StatusCreated)
	
	// Test body writing
	testBody := []byte(`{"test": "data"}`)
	capture.Write(testBody)
	
	// Verify captured data
	statusCode, headers, body := capture.GetCapturedData()
	
	if statusCode != http.StatusCreated {
		t.Errorf("Expected status code 201, got %d", statusCode)
	}
	
	if headers.Get("Content-Type") != "application/json" {
		t.Error("Expected Content-Type header to be captured")
	}
	
	if string(body) != string(testBody) {
		t.Errorf("Expected body %s, got %s", string(testBody), string(body))
	}
	
	// Verify original writer received data
	if w.Code != http.StatusCreated {
		t.Errorf("Expected original writer status 201, got %d", w.Code)
	}
	
	if w.Body.String() != string(testBody) {
		t.Error("Expected original writer to receive body")
	}
}

func TestResponseCache_MaxSizeEviction(t *testing.T) {
	cache := NewResponseCache(5*time.Minute, 2) // Small cache for testing
	
	headers := make(http.Header)
	body := []byte(`{"test": "data"}`)
	
	// Add 3 entries to a cache with max size 2
	req1 := httptest.NewRequest("GET", "/test1", nil)
	req2 := httptest.NewRequest("GET", "/test2", nil)
	req3 := httptest.NewRequest("GET", "/test3", nil)
	
	cache.Set(req1, http.StatusOK, headers, body)
	cache.Set(req2, http.StatusOK, headers, body)
	cache.Set(req3, http.StatusOK, headers, body) // Should evict oldest
	
	// First entry should be evicted
	if _, found := cache.Get(req1); found {
		t.Error("First entry should be evicted due to size limit")
	}
	
	// Second and third should still be there
	if _, found := cache.Get(req2); !found {
		t.Error("Second entry should still be in cache")
	}
	
	if _, found := cache.Get(req3); !found {
		t.Error("Third entry should still be in cache")
	}
}

func TestExtractMaxAge(t *testing.T) {
	tests := []struct {
		cacheControl string
		expectedAge  int
	}{
		{"max-age=300", 300},
		{"public, max-age=3600", 3600},
		{"max-age=0, no-cache", 0},
		{"no-cache", 0},
		{"max-age=invalid", 0},
		{"", 0},
	}
	
	for _, tt := range tests {
		t.Run(tt.cacheControl, func(t *testing.T) {
			age := extractMaxAge(tt.cacheControl)
			if age != tt.expectedAge {
				t.Errorf("Expected max-age %d, got %d", tt.expectedAge, age)
			}
		})
	}
}

func BenchmarkResponseCache_Get(b *testing.B) {
	cache := NewResponseCache(5*time.Minute, 1000)
	
	req := httptest.NewRequest("GET", "/test", nil)
	headers := make(http.Header)
	body := []byte(`{"test": "data"}`)
	
	cache.Set(req, http.StatusOK, headers, body)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Get(req)
	}
}

func BenchmarkResponseCache_Set(b *testing.B) {
	cache := NewResponseCache(5*time.Minute, 10000)
	
	headers := make(http.Header)
	body := []byte(`{"test": "data"}`)
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest("GET", fmt.Sprintf("/test%d", i), nil)
		cache.Set(req, http.StatusOK, headers, body)
	}
}