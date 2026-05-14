package server

import (
	"net"
	"net/http"
	"sync"

	"golang.org/x/time/rate"
)

type keyedLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	r        rate.Limit
	b        int
}

func newKeyedLimiter(r rate.Limit, b int) *keyedLimiter {
	return &keyedLimiter{
		limiters: make(map[string]*rate.Limiter),
		r:        r,
		b:        b,
	}
}

func (kl *keyedLimiter) allow(key string) bool {
	kl.mu.Lock()
	l, ok := kl.limiters[key]
	if !ok {
		l = rate.NewLimiter(kl.r, kl.b)
		kl.limiters[key] = l
	}
	kl.mu.Unlock()
	return l.Allow()
}

func clientIP(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
