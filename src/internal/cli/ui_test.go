// SPDX-License-Identifier: Apache-2.0

package cli

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ipNet(s string) *net.IPNet {
	ip, n, _ := net.ParseCIDR(s)
	n.IP = ip
	return n
}

// The machine's interfaces: loopback, a LAN address, link-local and a
// global IPv6 address.
var testAddrs = []net.Addr{ipNet("127.0.0.1/8"), ipNet("fe80::1/64"), ipNet("2001:db8::7/64"), ipNet("192.168.1.20/24")}

func TestPlanUI(t *testing.T) {
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		opts      uiOptions
		auth      bool
		wantURLs  []string
		wantNotes []string
		wantTLS   bool
	}{
		{"the default, this machine only", uiOptions{port: "3939", bind: "127.0.0.1"}, false,
			[]string{"http://localhost:3939"}, nil, false},
		{"a network bind lists the LAN addresses and warns about plain HTTP", uiOptions{port: "3939", bind: "0.0.0.0"}, true,
			[]string{"http://192.168.1.20:3939", "http://[2001:db8::7]:3939", "http://localhost:3939"}, []string{"plain HTTP"}, false},
		{"one address", uiOptions{port: "8443", bind: "192.168.1.20", selfSigned: true}, true,
			[]string{"https://192.168.1.20:8443"}, []string{"self-signed", "fingerprint"}, true},
		{"a team server with a self-signed certificate", uiOptions{port: "3939", bind: "0.0.0.0", selfSigned: true}, true,
			[]string{"https://192.168.1.20:3939", "https://[2001:db8::7]:3939", "https://localhost:3939"}, []string{"SHA-256 fingerprint"}, true},
		{"the operator's own certificate", uiOptions{port: "3939", bind: "0.0.0.0", tlsCert: "c.pem", tlsKey: "k.pem"}, true,
			[]string{"https://192.168.1.20:3939", "https://[2001:db8::7]:3939", "https://localhost:3939"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := t.TempDir()
			plan, err := planUI(tt.opts, tt.auth, state, "lab-host", testAddrs, now)
			if err != nil {
				t.Fatalf("planUI: %v", err)
			}
			if strings.Join(plan.urls, " ") != strings.Join(tt.wantURLs, " ") {
				t.Errorf("urls = %v, want %v", plan.urls, tt.wantURLs)
			}
			notes := strings.Join(plan.notes, "\n")
			for _, w := range tt.wantNotes {
				if !strings.Contains(notes, w) {
					t.Errorf("notes do not say %q:\n%s", w, notes)
				}
			}
			if len(tt.wantNotes) == 0 && notes != "" {
				t.Errorf("unexpected notes:\n%s", notes)
			}
			if (plan.certFile != "") != tt.wantTLS {
				t.Errorf("certFile = %q, want TLS %v", plan.certFile, tt.wantTLS)
			}
			if tt.opts.selfSigned && filepath.Dir(plan.certFile) != filepath.Join(state, "tls") {
				t.Errorf("self-signed certificate at %s, want it in the lab state", plan.certFile)
			}
		})
	}
}

// A self-signed certificate covers every URL the server prints, so a
// participant who checks the fingerprint gets no name mismatch.
func TestPlanUI_SelfSignedCoversEveryURL(t *testing.T) {
	plan, err := planUI(uiOptions{port: "3939", bind: "0.0.0.0", selfSigned: true}, true, t.TempDir(), "lab-host", testAddrs, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cert := loadCert(t, plan.certFile, plan.keyFile)
	for _, u := range append(plan.urls, "https://lab-host:3939", "https://127.0.0.1:3939") {
		host, _, _ := net.SplitHostPort(strings.TrimPrefix(u, "https://"))
		if err := cert.VerifyHostname(host); err != nil {
			t.Errorf("certificate does not cover %s: %v", u, err)
		}
	}
}

func TestPlanUI_Failures(t *testing.T) {
	tests := []struct {
		name    string
		opts    uiOptions
		auth    bool
		wantErr string
	}{
		{"a network bind without auth", uiOptions{bind: "0.0.0.0"}, false, "LABCTL_AUTH=true"},
		{"self-signed with the operator's certificate", uiOptions{bind: "127.0.0.1", selfSigned: true, tlsCert: "c.pem", tlsKey: "k.pem"}, false, "drop --tls-cert and --tls-key"},
		{"a certificate without its key", uiOptions{bind: "127.0.0.1", tlsCert: "c.pem"}, false, "must be provided together"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := planUI(tt.opts, tt.auth, t.TempDir(), "h", testAddrs, time.Now())
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("planUI = %v, want it to say %q", err, tt.wantErr)
			}
		})
	}
}

// The ways `labctl ui` refuses to start, as a user sees them.
func TestUI_Failures(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"expose the API with auth off", []string{"ui", "--bind", "0.0.0.0"}, "refusing to bind 0.0.0.0 without authentication"},
		{"two certificates at once", []string{"ui", "--tls-self-signed", "--tls-cert", "c.pem", "--tls-key", "k.pem"}, "drop --tls-cert and --tls-key"},
		{"a key without its certificate", []string{"ui", "--tls-key", "k.pem"}, "must be provided together"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := newTerminal(t)
			t.Setenv("LABCTL_AUTH", "")
			r := l.run(tt.args...)
			if r.exitCode() != 1 || !strings.Contains(r.stderr, tt.wantErr) {
				t.Errorf("exit %d, stderr %q; want 1 and %q", r.exitCode(), r.stderr, tt.wantErr)
			}
		})
	}
}

func loadCert(t *testing.T, certFile, keyFile string) *x509.Certificate {
	t.Helper()
	kp, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(kp.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return cert
}
