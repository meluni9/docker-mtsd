package ratelimit

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_Allow(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute) // 3 requests per minute
	ip := "192.168.1.1"

	// First 3 requests should be allowed
	for i := 0; i < 3; i++ {
		if !rl.Allow(ip) {
			t.Errorf("Request %d should be allowed", i+1)
		}
	}

	// 4th request should be denied
	if rl.Allow(ip) {
		t.Error("4th request should be denied")
	}
}

func TestRateLimiter_GetRemainingRequests(t *testing.T) {
	rl := NewRateLimiter(5, time.Minute)
	ip := "192.168.1.1"

	// Initially should have 5 remaining
	if remaining := rl.GetRemainingRequests(ip); remaining != 5 {
		t.Errorf("Expected 5 remaining requests, got %d", remaining)
	}

	// Use 2 requests
	rl.Allow(ip)
	rl.Allow(ip)

	// Should have 3 remaining
	if remaining := rl.GetRemainingRequests(ip); remaining != 3 {
		t.Errorf("Expected 3 remaining requests, got %d", remaining)
	}
}

func TestRateLimiter_WindowExpiry(t *testing.T) {
	rl := NewRateLimiter(2, 100*time.Millisecond) // 2 requests per 100ms
	ip := "192.168.1.1"

	// Use up the limit
	if !rl.Allow(ip) {
		t.Error("First request should be allowed")
	}
	if !rl.Allow(ip) {
		t.Error("Second request should be allowed")
	}
	if rl.Allow(ip) {
		t.Error("Third request should be denied")
	}

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	// Should be allowed again
	if !rl.Allow(ip) {
		t.Error("Request after window expiry should be allowed")
	}
}

func TestRateLimiter_MultipleIPs(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)
	ip1 := "192.168.1.1"
	ip2 := "192.168.1.2"

	// Each IP should have independent limits
	if !rl.Allow(ip1) {
		t.Error("First request for IP1 should be allowed")
	}
	if !rl.Allow(ip1) {
		t.Error("Second request for IP1 should be allowed")
	}
	if rl.Allow(ip1) {
		t.Error("Third request for IP1 should be denied")
	}

	// IP2 should still be allowed
	if !rl.Allow(ip2) {
		t.Error("First request for IP2 should be allowed")
	}
	if !rl.Allow(ip2) {
		t.Error("Second request for IP2 should be allowed")
	}
}

func TestRateLimiter_Middleware(t *testing.T) {
	rl := NewRateLimiter(2, time.Minute)
	
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	
	middleware := rl.Middleware(handler)

	// First two requests should succeed
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("GET", "/test", nil)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()
		
		middleware.ServeHTTP(w, req)
		
		if w.Code != http.StatusOK {
			t.Errorf("Request %d should succeed, got status %d", i+1, w.Code)
		}
		
		// Check rate limit headers
		if limit := w.Header().Get("X-RateLimit-Limit"); limit != "2" {
			t.Errorf("Expected X-RateLimit-Limit: 2, got %s", limit)
		}
	}

	// Third request should be rate limited
	req := httptest.NewRequest("GET", "/test", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	w := httptest.NewRecorder()
	
	middleware.ServeHTTP(w, req)
	
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("Third request should be rate limited, got status %d", w.Code)
	}
	
	// Check rate limit headers
	if remaining := w.Header().Get("X-RateLimit-Remaining"); remaining != "0" {
		t.Errorf("Expected X-RateLimit-Remaining: 0, got %s", remaining)
	}
	
	if retryAfter := w.Header().Get("Retry-After"); retryAfter == "" {
		t.Error("Expected Retry-After header to be set")
	}
}

func TestGetClientIP(t *testing.T) {
	tests := []struct {
		name           string
		remoteAddr     string
		xForwardedFor  string
		xRealIP        string
		expectedIP     string
	}{
		{
			name:       "RemoteAddr only",
			remoteAddr: "192.168.1.1:12345",
			expectedIP: "192.168.1.1",
		},
		{
			name:          "X-Forwarded-For header",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "192.168.1.1",
			expectedIP:    "192.168.1.1",
		},
		{
			name:       "X-Real-IP header",
			remoteAddr: "10.0.0.1:12345",
			xRealIP:    "192.168.1.1",
			expectedIP: "192.168.1.1",
		},
		{
			name:          "X-Forwarded-For takes precedence",
			remoteAddr:    "10.0.0.1:12345",
			xForwardedFor: "192.168.1.1",
			xRealIP:       "192.168.1.2",
			expectedIP:    "192.168.1.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.RemoteAddr = tt.remoteAddr
			
			if tt.xForwardedFor != "" {
				req.Header.Set("X-Forwarded-For", tt.xForwardedFor)
			}
			
			if tt.xRealIP != "" {
				req.Header.Set("X-Real-IP", tt.xRealIP)
			}

			ip := GetClientIP(req)
			if ip != tt.expectedIP {
				t.Errorf("Expected IP %s, got %s", tt.expectedIP, ip)
			}
		})
	}
}

func BenchmarkRateLimiter_Allow(b *testing.B) {
	rl := NewRateLimiter(1000, time.Minute)
	ip := "192.168.1.1"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rl.Allow(ip)
	}
}

func BenchmarkRateLimiter_AllowConcurrent(b *testing.B) {
	rl := NewRateLimiter(10000, time.Minute)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			ip := fmt.Sprintf("192.168.1.%d", i%255+1)
			rl.Allow(ip)
			i++
		}
	})
}