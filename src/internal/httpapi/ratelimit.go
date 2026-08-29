// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"sync"
	"time"
)

// Login rate-limit tuning. A fixed window of a handful of attempts per client is
// enough to stop credential stuffing while never getting in a real user's way —
// a person fat-fingering a password a few times stays well under the cap.
const (
	loginMaxAttempts = 5
	loginWindow      = 1 * time.Minute
)

// loginLimiter is a per-key fixed-window counter guarding the login endpoint. A
// successful login resets the caller's window, so legitimate use never
// accumulates toward the cap. Keys are client addresses; the map is swept lazily
// as windows expire, so it does not grow without bound under a stuffing attack
// from rotating source ports.
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

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{
		windows:  make(map[string]*limitWindow),
		max:      max,
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
