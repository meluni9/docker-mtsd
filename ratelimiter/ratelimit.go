package ratelimit

import (
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

type RateLimiter struct {
	requests    map[string]*ClientLimiter
	mu          sync.RWMutex
	maxRequests int          
	window      time.Duration
	cleanupTick time.Duration
}

type ClientLimiter struct {
	requests  []time.Time
	mu        sync.Mutex
	lastSeen  time.Time
}

func NewRateLimiter(maxRequests int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		requests:    make(map[string]*ClientLimiter),
		maxRequests: maxRequests,
		window:      window,
		cleanupTick: time.Minute * 5,
	}

	go rl.cleanup()

	return rl
}

func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.RLock()
	client, exists := rl.requests[ip]
	rl.mu.RUnlock()

	if !exists {
		client = &ClientLimiter{
			requests: make([]time.Time, 0),
			lastSeen: time.Now(),
		}
		rl.mu.Lock()
		rl.requests[ip] = client
		rl.mu.Unlock()
	}

	client.mu.Lock()
	defer client.mu.Unlock()

	now := time.Now()
	client.lastSeen = now

	cutoff := now.Add(-rl.window)
	validRequests := make([]time.Time, 0, len(client.requests))
	for _, requestTime := range client.requests {
		if requestTime.After(cutoff) {
			validRequests = append(validRequests, requestTime)
		}
	}
	client.requests = validRequests

	if len(client.requests) >= rl.maxRequests {
		return false
	}

	client.requests = append(client.requests, now)
	return true
}

func (rl *RateLimiter) GetRemainingRequests(ip string) int {
	rl.mu.RLock()
	client, exists := rl.requests[ip]
	rl.mu.RUnlock()

	if !exists {
		return rl.maxRequests
	}

	client.mu.Lock()
	defer client.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)
	
	validRequestCount := 0
	for _, requestTime := range client.requests {
		if requestTime.After(cutoff) {
			validRequestCount++
		}
	}

	remaining := rl.maxRequests - validRequestCount
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (rl *RateLimiter) GetResetTime(ip string) time.Time {
	rl.mu.RLock()
	client, exists := rl.requests[ip]
	rl.mu.RUnlock()

	if !exists || len(client.requests) == 0 {
		return time.Now()
	}

	client.mu.Lock()
	defer client.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)
	
	for _, requestTime := range client.requests {
		if requestTime.After(cutoff) {
			return requestTime.Add(rl.window)
		}
	}

	return now
}

func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(rl.cleanupTick)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			cutoff := time.Now().Add(-rl.window * 2)
			
			for ip, client := range rl.requests {
				client.mu.Lock()
				if client.lastSeen.Before(cutoff) {
					delete(rl.requests, ip)
				}
				client.mu.Unlock()
			}
			rl.mu.Unlock()
		}
	}
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := GetClientIP(r)
		
		if !rl.Allow(ip) {
			remaining := rl.GetRemainingRequests(ip)
			resetTime := rl.GetResetTime(ip)
			
			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", rl.maxRequests))
			w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
			w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", resetTime.Unix()))
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", time.Until(resetTime).Seconds()))
			
			http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
			return
		}

		remaining := rl.GetRemainingRequests(ip)
		resetTime := rl.GetResetTime(ip)
		
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", rl.maxRequests))
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", remaining))
		w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", resetTime.Unix()))

		next.ServeHTTP(w, r)
	})
}

func GetClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if ip := net.ParseIP(xff); ip != nil {
			return ip.String()
		}
	}

	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if ip := net.ParseIP(xri); ip != nil {
			return ip.String()
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	
	return host
}