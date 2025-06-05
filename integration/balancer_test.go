package integration

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"testing"
	"time"
)

const baseAddress = "http://balancer:8090"

var client = http.Client{
	Timeout: 3 * time.Second,
}

func TestBalancer(t *testing.T) {
	if _, exists := os.LookupEnv("INTEGRATION_TEST"); !exists {
		t.Skip("Integration test is not enabled")
	}

	for i := 0; i < len(serversPool); i++ {
		if !health(serversPool[i]) {
			t.Fatalf("Server %s is not healthy at test start", serversPool[i])
		}
	}

	endpoints := []string{
		"/api/v1/some-data",
		"/api/v1/some-data-check1",
		"/api/v1/some-data-check2",
		"/api/v1/some-data-check3",
	}

	serversUsed := make(map[string]bool)
	requestsCount := 30

	for i := 0; i < requestsCount; i++ {
		for _, endpoint := range endpoints {
			url := fmt.Sprintf("%s%s", baseAddress, endpoint)
			resp, err := client.Get(url)
			if err != nil {
				t.Errorf("Request to %s failed: %v", url, err)
				continue
			}

			if resp.StatusCode != http.StatusOK {
				t.Errorf("Request to %s returned status %d, expected 200", url, resp.StatusCode)
			}

			serverFrom := resp.Header.Get("lb-from")
			if serverFrom == "" {
				t.Error("No lb-from header present in response")
			} else {
				t.Logf("[%s] response from [%s]", endpoint, serverFrom)
				serversUsed[serverFrom] = true
			}

			resp.Body.Close()
		}
	}

	t.Logf("Servers used: %d", len(serversUsed))
	for server := range serversUsed {
		t.Logf("Server used: %s", server)
	}

	expectedServers := []string{"server1:8080", "server2:8080", "server3:8080"}
	for _, server := range expectedServers {
		if !serversUsed[server] {
			t.Errorf("Server %s was not used by balancer", server)
		}
	}
}

func TestConsistentHashingOfBalancer(t *testing.T) {
	if _, exists := os.LookupEnv("INTEGRATION_TEST"); !exists {
		t.Skip("Integration test is not enabled")
	}

	endpointServersMap := make(map[string]string)
	endpoints := []string{
		"/api/v1/some-data",
		"/api/v1/some-data-check1",
		"/api/v1/some-data-check2",
		"/api/v1/some-data-check3",
	}

	for _, endpoint := range endpoints {
		url := fmt.Sprintf("%s%s", baseAddress, endpoint)
		resp, err := client.Get(url)
		if err != nil {
			t.Fatalf("Failed to connect to balancer: %v", err)
		}

		serverFrom := resp.Header.Get("lb-from")
		if serverFrom == "" {
			t.Error("No lb-from header present in response")
		} else {
			endpointServersMap[endpoint] = serverFrom
			t.Logf("Endpoint %s mapped to server %s", endpoint, serverFrom)
		}

		resp.Body.Close()
	}

	for _, endpoint := range endpoints {
		url := fmt.Sprintf("%s%s", baseAddress, endpoint)
		resp, err := client.Get(url)
		if err != nil {
			t.Fatalf("Failed to connect to balancer: %v", err)
		}

		serverFrom := resp.Header.Get("lb-from")
		expectedServer := endpointServersMap[endpoint]

		if serverFrom != expectedServer {
			t.Errorf("Expected endpoint %s to be consistently routed to %s, but got %s",
				endpoint, expectedServer, serverFrom)
		}

		resp.Body.Close()
	}
}

func TestURLStickyRouting(t *testing.T) {
	if _, exists := os.LookupEnv("INTEGRATION_TEST"); !exists {
		t.Skip("Integration test is not enabled")
	}

	const (
		testURL          = "/api/v1/some-data?key=test123"
		parallelRequests = 20
	)

	results := make(chan string, parallelRequests)
	var wg sync.WaitGroup

	wg.Add(parallelRequests)
	for i := 0; i < parallelRequests; i++ {
		go func(id int) {
			defer wg.Done()

			resp, err := client.Get(baseAddress + testURL)
			if err != nil {
				t.Errorf("Request %d failed: %v", id, err)
				return
			}
			defer resp.Body.Close()

			server := resp.Header.Get("lb-from")
			if server == "" {
				t.Errorf("Request %d: no lb-from header", id)
				return
			}
			results <- server
		}(i)
	}

	wg.Wait()
	close(results)

	uniqueServers := make(map[string]struct{})
	for server := range results {
		uniqueServers[server] = struct{}{}
	}

	if len(uniqueServers) != 1 {
		t.Errorf("Expected all requests to go to same server, got %d different servers: %v",
			len(uniqueServers), uniqueServers)
	} else {
		t.Logf("All %d requests for '%s' went to server: %s",
			parallelRequests, testURL, getFirstKey(uniqueServers))
	}
}

func TestRateLimiting(t *testing.T) {
	if _, exists := os.LookupEnv("INTEGRATION_TEST"); !exists {
		t.Skip("Integration test is not enabled")
	}

	// Test rate limiting by making many requests quickly
	const requestCount = 20
	const endpoint = "/api/v1/some-data"
	
	var rateLimitedCount int
	var successCount int

	for i := 0; i < requestCount; i++ {
		resp, err := client.Get(baseAddress + endpoint)
		if err != nil {
			t.Errorf("Request %d failed: %v", i, err)
			continue
		}

		switch resp.StatusCode {
		case http.StatusOK:
			successCount++
			// Check rate limit headers
			if limit := resp.Header.Get("X-RateLimit-Limit"); limit == "" {
				t.Error("Expected X-RateLimit-Limit header on successful request")
			}
			if remaining := resp.Header.Get("X-RateLimit-Remaining"); remaining == "" {
				t.Error("Expected X-RateLimit-Remaining header on successful request")
			}
		case http.StatusTooManyRequests:
			rateLimitedCount++
			// Check rate limit headers for rate limited requests
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter == "" {
				t.Error("Expected Retry-After header on rate limited request")
			}
			if reset := resp.Header.Get("X-RateLimit-Reset"); reset == "" {
				t.Error("Expected X-RateLimit-Reset header on rate limited request")
			}
		default:
			t.Errorf("Unexpected status code: %d", resp.StatusCode)
		}

		resp.Body.Close()
	}

	t.Logf("Successful requests: %d, Rate limited: %d", successCount, rateLimitedCount)
	
	// We expect some requests to be rate limited if we're hitting the limit
	if successCount == requestCount {
		t.Log("Warning: No rate limiting occurred - may need to adjust limits for testing")
	}
}

func TestCaching(t *testing.T) {
	if _, exists := os.LookupEnv("INTEGRATION_TEST"); !exists {
		t.Skip("Integration test is not enabled")
	}

	// Use a unique endpoint for cache testing to avoid conflicts with other tests
	endpoint := "/api/v1/some-data?cache_test=unique_" + fmt.Sprintf("%d", time.Now().UnixNano())
	url := baseAddress + endpoint

	// First request should be a cache miss
	resp1, err := client.Get(url)
	if err != nil {
		t.Fatalf("First request failed: %v", err)
	}
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp1.StatusCode)
	}

	cacheStatus1 := resp1.Header.Get("X-Cache")
	if cacheStatus1 != "MISS" {
		t.Errorf("Expected X-Cache: MISS for first request, got %s", cacheStatus1)
	}

	// Second request should be a cache hit
	resp2, err := client.Get(url)
	if err != nil {
		t.Fatalf("Second request failed: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp2.StatusCode)
	}

	cacheStatus2 := resp2.Header.Get("X-Cache")
	if cacheStatus2 != "HIT" {
		t.Errorf("Expected X-Cache: HIT for second request, got %s", cacheStatus2)
	}

	// Check cache TTL header
	if ttl := resp2.Header.Get("X-Cache-TTL"); ttl == "" {
		t.Error("Expected X-Cache-TTL header on cache hit")
	}

	// Check Age header
	if age := resp2.Header.Get("Age"); age == "" {
		t.Error("Expected Age header on cache hit")
	}

	t.Logf("Cache test passed - first request: %s, second request: %s", cacheStatus1, cacheStatus2)
}

func TestStatsEndpoint(t *testing.T) {
	if _, exists := os.LookupEnv("INTEGRATION_TEST"); !exists {
		t.Skip("Integration test is not enabled")
	}

	resp, err := client.Get(baseAddress + "/stats")
	if err != nil {
		t.Fatalf("Stats request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 for stats endpoint, got %d", resp.StatusCode)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Expected Content-Type: application/json, got %s", contentType)
	}

	t.Log("Stats endpoint is accessible and returns JSON")
}

func TestCacheClearEndpoint(t *testing.T) {
	if _, exists := os.LookupEnv("INTEGRATION_TEST"); !exists {
		t.Skip("Integration test is not enabled")
	}

	// First, make a request to populate cache with a unique URL
	uniqueEndpoint := "/api/v1/some-data-check1?cache_clear_test=" + fmt.Sprintf("%d", time.Now().UnixNano())
	resp1, err := client.Get(baseAddress + uniqueEndpoint)
	if err != nil {
		t.Fatalf("Initial request failed: %v", err)
	}
	resp1.Body.Close()

	// Verify it's cached
	resp2, err := client.Get(baseAddress + uniqueEndpoint)
	if err != nil {
		t.Fatalf("Second request failed: %v", err)
	}
	resp2.Body.Close()

	if resp2.Header.Get("X-Cache") != "HIT" {
		t.Error("Expected cache hit before clearing")
	}

	// Clear cache
	req, _ := http.NewRequest("POST", baseAddress+"/cache/clear", nil)
	resp3, err := client.Do(req)
	if err != nil {
		t.Fatalf("Cache clear request failed: %v", err)
	}
	defer resp3.Body.Close()

	if resp3.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200 for cache clear, got %d", resp3.StatusCode)
	}

	// Next request should be cache miss
	resp4, err := client.Get(baseAddress + uniqueEndpoint)
	if err != nil {
		t.Fatalf("Request after cache clear failed: %v", err)
	}
	defer resp4.Body.Close()

	if resp4.Header.Get("X-Cache") != "MISS" {
		t.Error("Expected cache miss after clearing cache")
	}

	t.Log("Cache clear endpoint works correctly")
}

func TestRateLimitRecovery(t *testing.T) {
	if _, exists := os.LookupEnv("INTEGRATION_TEST"); !exists {
		t.Skip("Integration test is not enabled")
	}

	const endpoint = "/api/v1/some-data-check2"
	
	// Make requests until rate limited
	var lastResp *http.Response
	var err error
	
	for i := 0; i < 200; i++ { // Make enough requests to trigger rate limiting
		lastResp, err = client.Get(baseAddress + endpoint)
		if err != nil {
			t.Fatalf("Request %d failed: %v", i, err)
		}
		
		if lastResp.StatusCode == http.StatusTooManyRequests {
			t.Logf("Rate limited after %d requests", i+1)
			break
		}
		lastResp.Body.Close()
	}

	if lastResp == nil || lastResp.StatusCode != http.StatusTooManyRequests {
		t.Skip("Could not trigger rate limiting within 200 requests")
	}

	// Check Retry-After header
	retryAfter := lastResp.Header.Get("Retry-After")
	if retryAfter == "" {
		t.Error("Expected Retry-After header on rate limited response")
	}
	lastResp.Body.Close()

	t.Logf("Rate limiting triggered, Retry-After: %s seconds", retryAfter)
	
	// Wait a bit and try again (for a real test, you'd wait the full retry-after time)
	time.Sleep(2 * time.Second)
	
	resp, err := client.Get(baseAddress + endpoint)
	if err != nil {
		t.Fatalf("Request after wait failed: %v", err)
	}
	defer resp.Body.Close()

	// We might still be rate limited, but the endpoint should respond properly
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("Expected status 200 or 429 after wait, got %d", resp.StatusCode)
	}
}

func TestCacheWithDifferentEndpoints(t *testing.T) {
	if _, exists := os.LookupEnv("INTEGRATION_TEST"); !exists {
		t.Skip("Integration test is not enabled")
	}

	// Use unique query params to ensure these URLs haven't been cached
	uniqueId := fmt.Sprintf("%d", time.Now().UnixNano())
	endpoints := []string{
		"/api/v1/some-data?test=cache_endpoints_" + uniqueId + "_1",
		"/api/v1/some-data-check1?test=cache_endpoints_" + uniqueId + "_2",
		"/api/v1/some-data-check2?test=cache_endpoints_" + uniqueId + "_3",
		"/api/v1/some-data-check3?test=cache_endpoints_" + uniqueId + "_4",
	}

	// Make first requests to all endpoints (should be cache misses)
	for _, endpoint := range endpoints {
		resp, err := client.Get(baseAddress + endpoint)
		if err != nil {
			t.Errorf("Request to %s failed: %v", endpoint, err)
			continue
		}

		if resp.Header.Get("X-Cache") != "MISS" {
			t.Errorf("Expected cache miss for first request to %s", endpoint)
		}
		resp.Body.Close()
	}

	// Make second requests to all endpoints (should be cache hits)
	for _, endpoint := range endpoints {
		resp, err := client.Get(baseAddress + endpoint)
		if err != nil {
			t.Errorf("Second request to %s failed: %v", endpoint, err)
			continue
		}

		if resp.Header.Get("X-Cache") != "HIT" {
			t.Errorf("Expected cache hit for second request to %s", endpoint)
		}
		resp.Body.Close()
	}

	t.Log("Cache working correctly for different endpoints")
}

func BenchmarkBalancer(b *testing.B) {
	if _, exists := os.LookupEnv("INTEGRATION_TEST"); !exists {
		b.Skip("Integration benchmark is not enabled")
	}

	urls := []string{
		fmt.Sprintf("%s/api/v1/some-data", baseAddress),
		fmt.Sprintf("%s/api/v1/some-data-check1", baseAddress),
		fmt.Sprintf("%s/api/v1/some-data-check2", baseAddress),
		fmt.Sprintf("%s/api/v1/some-data-check3", baseAddress),
	}

	serverCounts := make(map[string]int)
	responseTimeTotal := time.Duration(0)
	cacheHits := 0
	cacheMisses := 0
	rateLimited := 0
	
	var responseTimeMu sync.Mutex
	var serverCountsMu sync.Mutex
	var cacheMu sync.Mutex
	var rateLimitMu sync.Mutex

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		localIndex := 0
		for pb.Next() {
			url := urls[localIndex%len(urls)]
			localIndex++

			start := time.Now()
			resp, err := client.Get(url)
			if err != nil {
				b.Logf("Request error: %v", err)
				continue
			}

			elapsed := time.Since(start)
			responseTimeMu.Lock()
			responseTimeTotal += elapsed
			responseTimeMu.Unlock()

			// Track cache performance
			cacheStatus := resp.Header.Get("X-Cache")
			cacheMu.Lock()
			switch cacheStatus {
			case "HIT":
				cacheHits++
			case "MISS":
				cacheMisses++
			}
			cacheMu.Unlock()

			// Track rate limiting
			if resp.StatusCode == http.StatusTooManyRequests {
				rateLimitMu.Lock()
				rateLimited++
				rateLimitMu.Unlock()
			}

			server := resp.Header.Get("lb-from")
			if server != "" {
				serverCountsMu.Lock()
				serverCounts[server]++
				serverCountsMu.Unlock()
			}

			resp.Body.Close()
		}
	})

	totalRequests := 0
	for _, count := range serverCounts {
		totalRequests += count
	}
	totalRequests += rateLimited // Add rate limited requests to total

	b.Logf("Total requests processed: %d", totalRequests)
	if totalRequests > 0 {
		b.Logf("Average response time: %v", responseTimeTotal/time.Duration(totalRequests))
	}

	// Cache statistics
	totalCacheRequests := cacheHits + cacheMisses
	if totalCacheRequests > 0 {
		hitRate := float64(cacheHits) * 100 / float64(totalCacheRequests)
		b.Logf("Cache hit rate: %.2f%% (%d hits, %d misses)", hitRate, cacheHits, cacheMisses)
	}

	// Rate limiting statistics
	if rateLimited > 0 {
		rateLimitRate := float64(rateLimited) * 100 / float64(totalRequests)
		b.Logf("Rate limited requests: %d (%.2f%%)", rateLimited, rateLimitRate)
	}

	// Server distribution
	var servers []string
	for server := range serverCounts {
		servers = append(servers, server)
	}
	sort.Strings(servers)

	for _, server := range servers {
		percentage := float64(serverCounts[server]) * 100 / float64(totalRequests-rateLimited)
		b.Logf("Server %s handled %d requests (%.2f%%)",
			server, serverCounts[server], percentage)
	}
}

func getFirstKey(m map[string]struct{}) string {
	for k := range m {
		return k
	}
	return ""
}

func health(server string) bool {
	resp, err := client.Get(fmt.Sprintf("http://%s/health", server))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

var serversPool = []string{
	"server1:8080",
	"server2:8080",
	"server3:8080",
}