package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/meluni9/docker-mtsd/cache"
	"github.com/meluni9/docker-mtsd/httptools"
	"github.com/meluni9/docker-mtsd/ratelimiter"
	"github.com/meluni9/docker-mtsd/signal"
)

var (
	port       = flag.Int("port", 8090, "load balancer port")
	timeoutSec = flag.Int("timeout-sec", 3, "request timeout time in seconds")
	https      = flag.Bool("https", false, "whether backends support HTTPs")

	traceEnabled = flag.Bool("trace", false, "whether to include tracing information into responses")
	
	// Rate limiting configuration
	rateLimitEnabled   = flag.Bool("rate-limit", true, "enable rate limiting")
	rateLimitRequests  = flag.Int("rate-limit-requests", 100, "max requests per minute per IP")
	rateLimitWindow    = flag.Duration("rate-limit-window", time.Minute, "rate limiting time window")
	
	// Cache configuration
	cacheEnabled       = flag.Bool("cache", true, "enable response caching")
	cacheTTL          = flag.Duration("cache-ttl", 5*time.Minute, "default cache TTL")
	cacheMaxSize      = flag.Int("cache-max-size", 1000, "maximum number of cache entries")
)

var (
	timeout     = time.Duration(*timeoutSec) * time.Second
	serversPool = []string{
		"server1:8080",
		"server2:8080",
		"server3:8080",
	}
	healthyServers = make([]bool, len(serversPool))
	mut            sync.Mutex
	
	// Rate limiter and cache instances
	rateLimiter *ratelimit.RateLimiter
	responseCache *cache.ResponseCache
)

func scheme() string {
	if *https {
		return "https"
	}
	return "http"
}

func health(dst string) bool {
	ctx, _ := context.WithTimeout(context.Background(), timeout)
	req, _ := http.NewRequestWithContext(ctx, "GET",
		fmt.Sprintf("%s://%s/health", scheme(), dst), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	if resp.StatusCode != http.StatusOK {
		return false
	}
	return true
}

func forward(dst string, rw http.ResponseWriter, r *http.Request) error {
	ctx, _ := context.WithTimeout(r.Context(), timeout)
	fwdRequest := r.Clone(ctx)
	fwdRequest.RequestURI = ""
	fwdRequest.URL.Host = dst
	fwdRequest.URL.Scheme = scheme()
	fwdRequest.Host = dst

	resp, err := http.DefaultClient.Do(fwdRequest)
	if err == nil {
		for k, values := range resp.Header {
			for _, value := range values {
				rw.Header().Add(k, value)
			}
		}
		if *traceEnabled {
			rw.Header().Set("lb-from", dst)
		}
		log.Println("fwd", resp.StatusCode, resp.Request.URL)
		rw.WriteHeader(resp.StatusCode)
		defer resp.Body.Close()
		_, err := io.Copy(rw, resp.Body)
		if err != nil {
			log.Printf("Failed to write response: %s", err)
		}
		return nil
	} else {
		log.Printf("Failed to get response from %s: %s", dst, err)
		rw.WriteHeader(http.StatusServiceUnavailable)
		return err
	}
}

func doHash(input string) uint32 {
	hasher := sha256.New()
	hasher.Write([]byte(input))
	return binary.BigEndian.Uint32(hasher.Sum(nil))
}

func updateHealthyServers() {
	for idx, address := range serversPool {
		srv := address
		index := idx
		go func() {
			for range time.Tick(10 * time.Second) {
				mut.Lock()
				healthyServers[index] = health(srv)
				mut.Unlock()
			}
		}()
	}
}

func chooseServer(uriPath string) string {
	mut.Lock()
	defer mut.Unlock()

	idx := doHash(uriPath) % uint32(len(serversPool))

	startIdx := idx
	for !healthyServers[idx] {
		idx = (idx + 1) % uint32(len(serversPool))
		if idx == startIdx {
			return ""
		}
	}

	return serversPool[idx]
}

// statsHandler provides statistics about rate limiting and caching
func statsHandler(rw http.ResponseWriter, r *http.Request) {
	stats := make(map[string]interface{})
	
	if rateLimiter != nil {
		stats["rate_limiting"] = map[string]interface{}{
			"enabled":      *rateLimitEnabled,
			"max_requests": *rateLimitRequests,
			"window":       rateLimitWindow.String(),
		}
	}
	
	if responseCache != nil {
		stats["caching"] = responseCache.GetStats()
	}
	
	stats["servers"] = map[string]interface{}{
		"pool":    serversPool,
		"healthy": healthyServers,
	}
	
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusOK)
	
	// Simple JSON encoding
	fmt.Fprintf(rw, `{
		"rate_limiting": {
			"enabled": %t,
			"max_requests": %d,
			"window": "%s"
		},
		"caching": {
			"enabled": %t,
			"ttl": "%s",
			"max_size": %d
		},
		"servers": {
			"total": %d,
			"healthy_count": %d
		}
	}`, 
		*rateLimitEnabled, *rateLimitRequests, rateLimitWindow.String(),
		*cacheEnabled, cacheTTL.String(), *cacheMaxSize,
		len(serversPool), countHealthyServers())
}

func countHealthyServers() int {
	mut.Lock()
	defer mut.Unlock()
	
	count := 0
	for _, healthy := range healthyServers {
		if healthy {
			count++
		}
	}
	return count
}

// clearCacheHandler allows clearing the cache via API
func clearCacheHandler(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	if responseCache != nil {
		responseCache.Clear()
		rw.WriteHeader(http.StatusOK)
		rw.Write([]byte("Cache cleared successfully"))
	} else {
		http.Error(rw, "Cache not enabled", http.StatusServiceUnavailable)
	}
}

func main() {
	flag.Parse()

	// Initialize rate limiter if enabled
	if *rateLimitEnabled {
		rateLimiter = ratelimit.NewRateLimiter(*rateLimitRequests, *rateLimitWindow)
		log.Printf("Rate limiting enabled: %d requests per %s", *rateLimitRequests, rateLimitWindow)
	}

	// Initialize cache if enabled
	if *cacheEnabled {
		responseCache = cache.NewResponseCache(*cacheTTL, *cacheMaxSize)
		log.Printf("Response caching enabled: TTL=%s, MaxSize=%d", cacheTTL, *cacheMaxSize)
	}

	updateHealthyServers()

	// Create main handler
	mainHandler := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		server := chooseServer(r.URL.Path)
		if server == "" {
			http.Error(rw, "No healthy servers available", http.StatusServiceUnavailable)
			return
		}

		err := forward(server, rw, r)
		if err != nil {
			log.Printf("Failed to forward request: %s", err)
		}
	})

	// Create mux for different endpoints
	mux := http.NewServeMux()
	
	// Stats endpoint
	mux.HandleFunc("/stats", statsHandler)
	
	// Cache management endpoint
	mux.HandleFunc("/cache/clear", clearCacheHandler)
	
	// Default handler for all other requests
	mux.Handle("/", mainHandler)

	var handler http.Handler = mux

	// Apply caching middleware if enabled
	if *cacheEnabled {
		handler = responseCache.Middleware(handler)
	}

	// Apply rate limiting middleware if enabled (should be outermost)
	if *rateLimitEnabled {
		handler = rateLimiter.Middleware(handler)
	}

	frontend := httptools.CreateServer(*port, handler)

	log.Println("Starting load balancer...")
	log.Printf("Tracing support enabled: %t", *traceEnabled)
	log.Printf("Rate limiting enabled: %t", *rateLimitEnabled)
	log.Printf("Response caching enabled: %t", *cacheEnabled)
	
	if *rateLimitEnabled {
		log.Printf("Rate limit: %d requests per %s", *rateLimitRequests, rateLimitWindow)
	}
	
	if *cacheEnabled {
		log.Printf("Cache TTL: %s, Max size: %d entries", cacheTTL, *cacheMaxSize)
	}
	
	frontend.Start()
	signal.WaitForTerminationSignal()
}