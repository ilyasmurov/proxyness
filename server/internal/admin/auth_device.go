package admin

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"proxyness/server/internal/db"
)

type ctxKey string

const ctxKeyUserID ctxKey = "user_id"

// DeviceAuth wraps handlers that should be authenticated via a device key
// in Authorization: Bearer <key>. The user_id from the matching device is
// stashed in the request context under ctxKeyUserID.
type DeviceAuth struct {
	db      *db.DB
	limiter *keyRateLimiter
}

func NewDeviceAuth(d *db.DB) *DeviceAuth {
	return &DeviceAuth{db: d, limiter: newKeyRateLimiter()}
}

func (a *DeviceAuth) Wrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHdr := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(authHdr, prefix) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		key := strings.TrimSpace(authHdr[len(prefix):])
		if key == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		if !a.limiter.allow(key) {
			http.Error(w, "rate limit", http.StatusTooManyRequests)
			return
		}

		device, err := a.db.GetDeviceByKey(key)
		if err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), ctxKeyUserID, device.UserID)
		next(w, r.WithContext(ctx))
	}
}

// UserIDFromContext extracts the user_id stashed by Wrap. Returns 0, false
// if the middleware wasn't applied.
func UserIDFromContext(ctx context.Context) (int, bool) {
	v, ok := ctx.Value(ctxKeyUserID).(int)
	return v, ok
}

// keyRateLimiter is a simple sliding-window counter: up to 60 requests
// per rolling minute per key. A janitor goroutine evicts idle entries
// once a minute to keep the map bounded.
type keyRateLimiter struct {
	mu      sync.Mutex
	windows map[string]*window
}

type window struct {
	times []time.Time // timestamps of the last ≤60 requests
}

func newKeyRateLimiter() *keyRateLimiter {
	l := &keyRateLimiter{windows: make(map[string]*window)}
	go l.janitor()
	return l
}

const (
	rateLimitMax    = 60
	rateLimitWindow = time.Minute
)

func (l *keyRateLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	w, ok := l.windows[key]
	if !ok {
		w = &window{}
		l.windows[key] = w
	}

	now := time.Now()
	cutoff := now.Add(-rateLimitWindow)
	kept := w.times[:0]
	for _, t := range w.times {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	w.times = kept

	if len(w.times) >= rateLimitMax {
		return false
	}
	w.times = append(w.times, now)
	return true
}

func (l *keyRateLimiter) janitor() {
	ticker := time.NewTicker(rateLimitWindow)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		cutoff := time.Now().Add(-rateLimitWindow)
		for k, w := range l.windows {
			if len(w.times) == 0 || w.times[len(w.times)-1].Before(cutoff) {
				delete(l.windows, k)
			}
		}
		l.mu.Unlock()
	}
}
