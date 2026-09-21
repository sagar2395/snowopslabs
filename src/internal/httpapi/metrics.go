// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// metricsMiddleware records one HTTP request metric per handled request: a
// counter by method/route/status and a latency histogram by method/route. It
// is only installed when the server has a metric set (WithMetrics).
//
// The route label is the mux path *template* (e.g. /api/apps/{name}/build), not
// the concrete path, so cardinality stays bounded no matter how many app or
// scenario names exist.
func (s *Server) metricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The WebSocket stream is long-lived and would distort the latency
		// histogram, so it is not measured.
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			next.ServeHTTP(w, r)
			return
		}

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rec, r)

		route := "unmatched"
		if cr := mux.CurrentRoute(r); cr != nil {
			if tmpl, err := cr.GetPathTemplate(); err == nil {
				route = tmpl
			}
		}
		s.metrics.HTTPObserve(r.Method, route, rec.status, time.Since(start))
	})
}

// statusRecorder records the response status code and passes everything else
// through, including http.Hijacker and http.Flusher, so streaming responses
// keep working.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
	bytes   int
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.written {
		r.status = code
		r.written = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	// An implicit 200: a handler that writes a body without calling WriteHeader.
	r.written = true
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

func (r *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := r.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("httpapi: underlying ResponseWriter does not support hijacking")
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
