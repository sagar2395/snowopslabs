// SPDX-License-Identifier: Apache-2.0
package auth

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestHashPassword_UsesArgon2id(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(h, schemeArgon2id+"$") {
		t.Fatalf("new hash should use argon2id, got %q", h)
	}
	if !VerifyPassword(h, "correct horse battery staple") {
		t.Error("argon2id hash should verify its own password")
	}
	if VerifyPassword(h, "wrong") {
		t.Error("argon2id hash must reject the wrong password")
	}
}

// A legacy PBKDF2 hash written by a prior version must still verify, so enabling
// Argon2id does not lock existing users out.
func TestVerifyPassword_AcceptsLegacyPBKDF2(t *testing.T) {
	const pw = "legacy-secret"
	legacy := legacyPBKDF2Hash(t, pw)

	if !strings.HasPrefix(legacy, schemePBKDF2+"$") {
		t.Fatalf("test fixture is not a pbkdf2 hash: %q", legacy)
	}
	if !VerifyPassword(legacy, pw) {
		t.Error("legacy PBKDF2 hash should still verify")
	}
	if VerifyPassword(legacy, "nope") {
		t.Error("legacy PBKDF2 hash must reject the wrong password")
	}
}

func TestVerifyPassword_MalformedArgon2id(t *testing.T) {
	bad := []string{
		"argon2id$65536$3$4$onlyfive",    // too few fields
		"argon2id$abc$3$4$AAAA$AAAA",     // non-numeric memory
		"argon2id$65536$0$4$AAAA$AAAA",   // zero time
		"argon2id$65536$3$999$AAAA$AAAA", // threads out of range
		"argon2id$65536$3$4$!!!!$AAAA",   // bad salt base64
		"argon2id$65536$3$4$AAAA$",       // empty hash
	}
	for _, enc := range bad {
		if VerifyPassword(enc, "anything") {
			t.Errorf("VerifyPassword(%q) = true, want false", enc)
		}
	}
}

// legacyPBKDF2Hash reconstructs the exact encoding a prior version produced, so
// the backward-compatibility test does not depend on HashPassword still emitting
// PBKDF2 (it no longer does).
func legacyPBKDF2Hash(t *testing.T, password string) string {
	t.Helper()
	salt := []byte("0123456789abcdef")
	dk, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyLen)
	if err != nil {
		t.Fatalf("pbkdf2: %v", err)
	}
	return fmt.Sprintf("%s$%d$%s$%s",
		schemePBKDF2, pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(dk),
	)
}
