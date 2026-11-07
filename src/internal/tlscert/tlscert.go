// SPDX-License-Identifier: Apache-2.0

// Package tlscert makes and reuses the self-signed certificate that
// `labctl ui --tls-self-signed` serves, so a team server can use HTTPS
// without another tool or a certificate authority.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// File names inside the directory Ensure is given.
const (
	CertFile = "server.crt"
	KeyFile  = "server.key"
)

const (
	validity    = 90 * 24 * time.Hour
	renewBefore = 7 * 24 * time.Hour
)

// Pair is a certificate and key on disk.
type Pair struct {
	CertFile string
	KeyFile  string
	// Fingerprint is the SHA-256 of the certificate, as browsers show it, so
	// people can check the certificate they are asked to trust.
	Fingerprint string
	NotAfter    time.Time
	// Created is true when Ensure wrote a new certificate.
	Created bool
}

// Ensure returns the certificate in dir when it is valid for every one of
// hosts and for at least another week, and otherwise writes a new one that
// is. hosts are DNS names or IP addresses.
func Ensure(dir string, hosts []string, now time.Time) (Pair, error) {
	if len(hosts) == 0 {
		return Pair{}, errors.New("a certificate needs at least one host name or address")
	}
	p := Pair{CertFile: filepath.Join(dir, CertFile), KeyFile: filepath.Join(dir, KeyFile)}
	if cert, ok := reusable(p, hosts, now); ok {
		p.Fingerprint, p.NotAfter = fingerprint(cert.Raw), cert.NotAfter
		return p, nil
	}
	der, notAfter, keyPEM, err := generate(hosts, now)
	if err != nil {
		return Pair{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Pair{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := os.WriteFile(p.KeyFile, keyPEM, 0o600); err != nil {
		return Pair{}, fmt.Errorf("writing the TLS key: %w", err)
	}
	if err := os.WriteFile(p.CertFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return Pair{}, fmt.Errorf("writing the TLS certificate: %w", err)
	}
	p.Fingerprint, p.NotAfter, p.Created = fingerprint(der), notAfter, true
	return p, nil
}

// reusable reports whether the pair on disk loads, matches its key, covers
// every host and does not expire within renewBefore.
func reusable(p Pair, hosts []string, now time.Time) (*x509.Certificate, bool) {
	kp, err := tls.LoadX509KeyPair(p.CertFile, p.KeyFile)
	if err != nil || len(kp.Certificate) == 0 {
		return nil, false
	}
	cert, err := x509.ParseCertificate(kp.Certificate[0])
	if err != nil || now.Before(cert.NotBefore) || now.Add(renewBefore).After(cert.NotAfter) {
		return nil, false
	}
	for _, h := range hosts {
		if cert.VerifyHostname(h) != nil {
			return nil, false
		}
	}
	return cert, true
}

func generate(hosts []string, now time.Time) (der []byte, notAfter time.Time, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, time.Time{}, nil, fmt.Errorf("generating the TLS key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, time.Time{}, nil, fmt.Errorf("generating a certificate serial: %w", err)
	}
	notAfter = now.Add(validity)
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: hosts[0], Organization: []string{"SnowOps Labs team server"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, time.Time{}, nil, fmt.Errorf("creating the TLS certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, time.Time{}, nil, fmt.Errorf("encoding the TLS key: %w", err)
	}
	return der, notAfter, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

// fingerprint is the SHA-256 of der as colon-separated hex pairs.
func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}
