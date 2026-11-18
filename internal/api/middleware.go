package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// rateLimiter is a simple per-IP sliding window counter.
type rateLimiter struct {
	mu        sync.Mutex
	windows   map[string][]time.Time
	limit     int
	window    time.Duration
	lastSweep time.Time
}

func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{windows: map[string][]time.Time{}, limit: limit, window: window}
}

func (rl *rateLimiter) allow(ip string) bool {
	now := time.Now()
	cutoff := now.Add(-rl.window)
	rl.mu.Lock()
	defer rl.mu.Unlock()
	if now.Sub(rl.lastSweep) > time.Minute {
		for k, v := range rl.windows {
			nv := v[:0]
			for _, t := range v {
				if t.After(cutoff) {
					nv = append(nv, t)
				}
			}
			if len(nv) == 0 {
				delete(rl.windows, k)
			} else {
				rl.windows[k] = nv
			}
		}
		rl.lastSweep = now
	}
	timestamps := rl.windows[ip]
	kept := timestamps[:0]
	for _, t := range timestamps {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= rl.limit {
		rl.windows[ip] = kept
		return false
	}
	rl.windows[ip] = append(kept, now)
	return true
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RequireXMLHttpRequest is the CSRF defense for the cookie-less SPA: every mutating
// API call must carry a header browsers won't send cross-origin via simple requests.
func RequireXMLHttpRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
			http.Error(w, `{"error":"missing X-Requested-With header (CSRF protection)"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
