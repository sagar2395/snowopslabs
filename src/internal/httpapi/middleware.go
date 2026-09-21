// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// ctxKey is an unexported context-key type so values stored here can't collide
// with keys from other packages.
type ctxKey int

const (
	requestIDKey ctxKey = iota
)

// requestIDHeader is both read (to honour a caller-supplied correlation ID) and
// written (so the client can tie its request to server logs).
const requestIDHeader = "X-Request-ID"

// maxInboundRequestID caps how long a caller-supplied ID we will echo back, so a
// hostile client can't bloat our logs or response headers with a giant value.
const maxInboundRequestID = 200

// requestIDMiddleware gives every request a correlation ID: the incoming
// X-Request-ID if it is acceptable, otherwise a new UUIDv4. The ID is stored
// in the request context and returned in the response header.
func (s *Server) requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitizeRequestID(r.Header.Get(requestIDHeader))
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(requestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// sanitizeRequestID accepts a caller's request ID only if it is short and
// printable ASCII, so it cannot inject headers or forge log lines.
func sanitizeRequestID(v string) string {
	if v == "" || len(v) > maxInboundRequestID {
		return ""
	}
	for _, c := range v {
		if c < 0x20 || c > 0x7e {
			return ""
		}
	}
	return v
}

// requestIDFrom returns the correlation ID stored on the context, or "" if the
// request did not pass through requestIDMiddleware (e.g. a unit test hitting a
// handler directly).
func requestIDFrom(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey).(string); ok {
		return id
	}
	return ""
}

// accessLogMiddleware writes one slog line per request with the method, path,
// route template, status, duration, response size, client address and request
// ID. A WebSocket upgrade is logged once when it opens and is not timed.
func (s *Server) accessLogMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := requestIDFrom(r.Context())

		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			slog.Info("http_ws_open",
				"method", r.Method,
				"path", r.URL.Path,
				"remote", clientAddr(r),
				"request_id", reqID,
			)
			next.ServeHTTP(w, r)
			return
		}

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)

		slog.Info("http_request",
			"method", r.Method,
			"path", r.URL.Path,
			"route", routeTemplate(r),
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes", rec.bytes,
			"remote", clientAddr(r),
			"request_id", reqID,
		)
	})
}

// routeTemplate returns the matched route's path template, such as
// /scenarios/{name}, or "unmatched".
func routeTemplate(r *http.Request) string {
	if cr := mux.CurrentRoute(r); cr != nil {
		if tmpl, err := cr.GetPathTemplate(); err == nil {
			return tmpl
		}
	}
	return "unmatched"
}

// clientAddr returns the request's remote address without the port.
func clientAddr(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}
