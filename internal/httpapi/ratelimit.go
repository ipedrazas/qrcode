package httpapi

import (
	"fmt"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimiter enforces a token bucket per client. IPv4 clients are keyed by
// address and IPv6 clients by /64, since one host usually controls a whole
// /64 and could otherwise rotate addresses to dodge the limit.
//
// Keys come from the TCP peer address only. Headers such as X-Forwarded-For
// are ignored because any client can forge them. Behind a reverse proxy every
// request shares the proxy's bucket, so rate limit at the proxy instead.
type RateLimiter struct {
	limit   rate.Limit
	burst   int
	idleTTL time.Duration
	now     func() time.Time

	mu        sync.Mutex
	clients   map[netip.Prefix]*rateClient
	lastSweep time.Time
}

type rateClient struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter allows each client rps requests per second on average, with
// bursts of up to burst requests. Both must be positive.
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	// A bucket idle for longer than it takes to refill is indistinguishable
	// from a new one, so it can be forgotten.
	ttl := time.Minute
	if refill := time.Duration(float64(burst) / rps * float64(time.Second)); refill > ttl {
		ttl = refill
	}
	return &RateLimiter{
		limit:   rate.Limit(rps),
		burst:   burst,
		idleTTL: ttl,
		now:     time.Now,
		clients: make(map[netip.Prefix]*rateClient),
	}
}

// reserve reports whether a request from key may proceed now and, if not,
// how long the client should wait.
func (rl *RateLimiter) reserve(key netip.Prefix) (bool, time.Duration) {
	now := rl.now()
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.sweep(now)
	c, ok := rl.clients[key]
	if !ok {
		c = &rateClient{limiter: rate.NewLimiter(rl.limit, rl.burst)}
		rl.clients[key] = c
	}
	c.lastSeen = now

	res := c.limiter.ReserveN(now, 1)
	if !res.OK() {
		return false, time.Second
	}
	if d := res.DelayFrom(now); d > 0 {
		res.CancelAt(now)
		return false, d
	}
	return true, 0
}

// sweep forgets idle clients, at most once per idleTTL, so memory stays
// bounded by the number of recently active clients.
func (rl *RateLimiter) sweep(now time.Time) {
	if now.Sub(rl.lastSweep) < rl.idleTTL {
		return
	}
	rl.lastSweep = now
	for k, c := range rl.clients {
		if now.Sub(c.lastSeen) >= rl.idleTTL {
			delete(rl.clients, k)
		}
	}
}

// Middleware rejects requests over the limit with 429 and Retry-After.
// Liveness probes are never limited.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		ok, wait := rl.reserve(clientKey(r.RemoteAddr))
		if !ok {
			secs := max(int(math.Ceil(wait.Seconds())), 1)
			w.Header().Set("Retry-After", strconv.Itoa(secs))
			writeJSONError(w, &APIError{
				Status:  http.StatusTooManyRequests,
				Code:    CodeRateLimited,
				Message: fmt.Sprintf("rate limit exceeded; retry after %ds", secs),
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientKey maps a peer address to its rate limit bucket. Unparseable
// addresses all share the zero key.
func clientKey(remoteAddr string) netip.Prefix {
	ap, err := netip.ParseAddrPort(remoteAddr)
	if err != nil {
		return netip.Prefix{}
	}
	addr := ap.Addr().Unmap()
	bits := 32
	if addr.Is6() {
		bits = 64
	}
	p, err := addr.Prefix(bits)
	if err != nil {
		return netip.Prefix{}
	}
	return p
}
