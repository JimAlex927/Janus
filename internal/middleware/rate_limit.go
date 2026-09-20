package middleware

import (
	"container/list"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimitOptions implements a bounded per-client token bucket. Average
// tokens are replenished over Period and Burst controls the initial capacity.
type RateLimitOptions struct {
	Average  int
	Period   time.Duration
	Burst    int
	MaxKeys  int
	ClientIP func(*http.Request) string
	Now      func() time.Time
}

type rateBucket struct {
	tokens   float64
	updated  time.Time
	lastSeen time.Time
	key      string
}

type rateLimiter struct {
	mu       sync.Mutex
	average  float64
	period   time.Duration
	burst    float64
	maxKeys  int
	clientIP func(*http.Request) string
	now      func() time.Time
	buckets  map[string]*list.Element
	lru      list.List
}

func NewRateLimiter(options RateLimitOptions) (*rateLimiter, error) {
	if options.Average < 1 || options.Period <= 0 || options.Burst < 1 || options.MaxKeys < 1 {
		return nil, &rateLimitConfigError{}
	}
	if options.ClientIP == nil {
		options.ClientIP = requestPeerIP
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &rateLimiter{average: float64(options.Average), period: options.Period, burst: float64(options.Burst), maxKeys: options.MaxKeys, clientIP: options.ClientIP, now: options.Now, buckets: make(map[string]*list.Element)}, nil
}

type rateLimitConfigError struct{}

func (*rateLimitConfigError) Error() string { return "invalid rate_limit configuration" }

func RateLimit(options RateLimitOptions) (Middleware, error) {
	limiter, err := NewRateLimiter(options)
	if err != nil {
		return nil, err
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := limiter.clientIP(r)
			allowed, retry := limiter.allow(key)
			if !allowed {
				seconds := int(math.Ceil(retry.Seconds()))
				if seconds < 1 {
					seconds = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(seconds))
				http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}, nil
}

func (l *rateLimiter) allow(key string) (bool, time.Duration) {
	if key == "" {
		key = "unknown"
	}
	now := l.now()
	rate := l.average / l.period.Seconds()
	l.mu.Lock()
	defer l.mu.Unlock()
	element, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxKeys {
			l.evictOldest()
		}
		element = l.lru.PushBack(&rateBucket{key: key, tokens: l.burst, updated: now})
		l.buckets[key] = element
	}
	l.lru.MoveToBack(element)
	bucket := element.Value.(*rateBucket)
	if now.After(bucket.updated) {
		bucket.tokens = math.Min(l.burst, bucket.tokens+now.Sub(bucket.updated).Seconds()*rate)
		bucket.updated = now
	}
	bucket.lastSeen = now
	if bucket.tokens < 1 {
		wait := time.Duration(math.Ceil((1 - bucket.tokens) / rate * float64(time.Second)))
		return false, wait
	}
	bucket.tokens--
	return true, 0
}

func (l *rateLimiter) evictOldest() {
	if oldest := l.lru.Front(); oldest != nil {
		delete(l.buckets, oldest.Value.(*rateBucket).key)
		l.lru.Remove(oldest)
	}
}
