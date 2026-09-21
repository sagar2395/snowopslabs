// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"sync"
	"time"
)

// Login rate limit: a few attempts per client per window, which stops password
// guessing without affecting someone who mistypes a few times.
const (
	loginMaxAttempts = 5
	loginWindow      = 1 * time.Minute
)

// loginLimiter counts login attempts per client address in fixed windows. A
// successful login resets the client's count. Expired windows are removed
// periodically, so the map cannot grow without bound.
type loginLimiter struct {
	mu       sync.Mutex
	windows  map[string]*limitWindow
	max      int
	window   time.Duration
	now      func() time.Time // injectable for tests
	lastGC   time.Time
	gcPeriod time.Duration
}

type limitWindow struct {
	count int
	start time.Time
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	return &loginLimiter{
		windows:  make(map[string]*limitWindow),
		max:      limit,
		window:   window,
		now:      time.Now,
		gcPeriod: 10 * window,
	}
}

// allow records an attempt for key and reports whether it is permitted. When it
// is not, retryAfter is how long the caller should wait before the window rolls
// over.
func (l *loginLimiter) allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.gcLocked(now)

	w, exists := l.windows[key]
	if !exists || now.Sub(w.start) >= l.window {
		l.windows[key] = &limitWindow{count: 1, start: now}
		return true, 0
	}
	if w.count >= l.max {
		return false, l.window - now.Sub(w.start)
	}
	w.count++
	return true, 0
}

// reset clears a key's window, called after a successful authentication.
func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.windows, key)
	l.mu.Unlock()
}

// gcLocked drops expired windows periodically. Caller holds l.mu.
func (l *loginLimiter) gcLocked(now time.Time) {
	if now.Sub(l.lastGC) < l.gcPeriod {
		return
	}
	l.lastGC = now
	for k, w := range l.windows {
		if now.Sub(w.start) >= l.window {
			delete(l.windows, k)
		}
	}
}
