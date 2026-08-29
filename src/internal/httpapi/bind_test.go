// SPDX-License-Identifier: Apache-2.0
package httpapi

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":   true,
		"::1":         true,
		"localhost":   true,
		"0.0.0.0":     false,
		"":            false, // all interfaces
		"192.168.1.5": false,
		"example.com": false,
	}
	for host, want := range cases {
		if got := IsLoopbackHost(host); got != want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestCheckBind(t *testing.T) {
	// Non-loopback without auth is refused.
	if err := CheckBind("0.0.0.0", false); err == nil {
		t.Error("binding all interfaces without auth should be refused")
	}
	// Loopback is always fine.
	if err := CheckBind("127.0.0.1", false); err != nil {
		t.Errorf("loopback bind should be allowed without auth: %v", err)
	}
	// Non-loopback WITH auth is allowed.
	if err := CheckBind("0.0.0.0", true); err != nil {
		t.Errorf("network bind with auth enabled should be allowed: %v", err)
	}
}

func TestIsSecureRequest(t *testing.T) {
	plain := httptest.NewRequest(http.MethodGet, "/", nil)
	if isSecureRequest(plain) {
		t.Error("plain HTTP request should not be considered secure")
	}

	tlsReq := httptest.NewRequest(http.MethodGet, "/", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	if !isSecureRequest(tlsReq) {
		t.Error("request with a TLS connection state should be secure")
	}

	proxied := httptest.NewRequest(http.MethodGet, "/", nil)
	proxied.Header.Set("X-Forwarded-Proto", "https")
	if !isSecureRequest(proxied) {
		t.Error("request forwarded as https should be considered secure")
	}
}
