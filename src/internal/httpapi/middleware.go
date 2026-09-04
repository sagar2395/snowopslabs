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

// requestIDMiddleware ensures every request carries a correlation ID. It honours
// a sane inbound X-Request-ID (so a proxy or the UI can thread one through) and
// otherwise mints a UUIDv4. The ID is stored in the request context and echoed
// in the response header, giving clients and the access log a shared handle.
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

// sanitizeRequestID accepts a caller-supplied correlation ID only when it is
// short and printable ASCII; anything else is dropped so we mint our own. This
// keeps header-injection and log-forging out of the correlation path.
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

// accessLogMiddleware emits one structured slog line per request: method, path,
// the matched route template (bounded cardinality), status, duration, response
// size, client address and the correlation ID. Websocket upgrades are logged
// once at open and passed through untouched, because their lifetime is the
// socket's, not the handler's — timing them would record minutes, not millis.
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

// routeTemplate returns the mux path template for the matched route (e.g.
// /scenarios/{name}), falling back to "unmatched" so the access log never
// carries an unbounded set of concrete paths as a structured field.
func routeTemplate(r *http.Request) string {
	if cr := mux.CurrentRoute(r); cr != nil {
		if tmpl, err := cr.GetPathTemplate(); err == nil {
			return tmpl
		}
	}
	return "unmatched"
}

// clientAddr is the request's remote address without the ephemeral port, which
// carries no diagnostic value and only adds noise to the log.
func clientAddr(r *http.Request) string {
	addr := r.RemoteAddr
	if i := strings.LastIndex(addr, ":"); i > 0 {
		return addr[:i]
	}
	return addr
}
