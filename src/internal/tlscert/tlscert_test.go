// SPDX-License-Identifier: Apache-2.0

package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

func load(t *testing.T, p Pair) *x509.Certificate {
	t.Helper()
	kp, err := tls.LoadX509KeyPair(p.CertFile, p.KeyFile)
	if err != nil {
		t.Fatalf("the pair does not load as a TLS key pair: %v", err)
	}
	cert, err := x509.ParseCertificate(kp.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestEnsure_CreatesACertificateForEveryHost(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tls")
	hosts := []string{"192.168.1.20", "localhost", "lab-host", "::1"}
	p, err := Ensure(dir, hosts, now)
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !p.Created {
		t.Error("Created = false for a new certificate")
	}
	cert := load(t, p)
	for _, h := range hosts {
		if err := cert.VerifyHostname(h); err != nil {
			t.Errorf("the certificate does not cover %s: %v", h, err)
		}
	}
	if err := cert.VerifyHostname("192.168.1.21"); err == nil {
		t.Error("the certificate covers an address it was not asked for")
	}
	if !p.NotAfter.Equal(now.Add(validity)) || len(strings.Split(p.Fingerprint, ":")) != 32 {
		t.Errorf("NotAfter %v, fingerprint %q", p.NotAfter, p.Fingerprint)
	}
	info, err := os.Stat(p.KeyFile)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("key file mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
}

func TestEnsure_ReusesOrReplaces(t *testing.T) {
	hosts := []string{"192.168.1.20", "localhost"}
	tests := []struct {
		name        string
		later       time.Duration
		hosts       []string
		damage      func(t *testing.T, p Pair)
		wantCreated bool
	}{
		{"still valid for the same hosts", 24 * time.Hour, hosts, nil, false},
		{"a subset of the hosts", 0, hosts[:1], nil, false},
		{"a host it does not cover", 0, append(hosts, "10.0.0.5"), nil, true},
		{"expires within a week", validity - 6*24*time.Hour, hosts, nil, true},
		{"already expired", validity + time.Hour, hosts, nil, true},
		{"the key is missing", 0, hosts, func(t *testing.T, p Pair) { _ = os.Remove(p.KeyFile) }, true},
		{"the certificate is damaged", 0, hosts, func(t *testing.T, p Pair) {
			if err := os.WriteFile(p.CertFile, []byte("not a certificate"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			first, err := Ensure(dir, hosts, now)
			if err != nil {
				t.Fatal(err)
			}
			if tt.damage != nil {
				tt.damage(t, first)
			}
			second, err := Ensure(dir, tt.hosts, now.Add(tt.later))
			if err != nil {
				t.Fatalf("second Ensure: %v", err)
			}
			if second.Created != tt.wantCreated || (second.Fingerprint == first.Fingerprint) == tt.wantCreated {
				t.Errorf("Created = %v (fingerprint changed: %v), want %v", second.Created, second.Fingerprint != first.Fingerprint, tt.wantCreated)
			}
			load(t, second)
		})
	}
}

func TestEnsure_Errors(t *testing.T) {
	if _, err := Ensure(t.TempDir(), nil, now); err == nil {
		t.Error("Ensure with no hosts succeeded")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(filepath.Join(file, "tls"), []string{"localhost"}, now); err == nil {
		t.Error("Ensure under a file instead of a directory succeeded")
	}
}
