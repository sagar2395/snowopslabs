// SPDX-License-Identifier: Apache-2.0

// Package auth provides optional authentication and per-user RBAC for the
// labctl API/UI server (task 062). It is OFF by default: when LABCTL_AUTH is
// not "true", the server behaves exactly as before (no login, no role checks).
//
// When enabled, users are defined in a static file (.labctl/users.yaml) with
// PBKDF2-HMAC-SHA256 password hashes and one of two roles:
//
//	operator    — full control (platform, runtime, lab, apps, services, …)
//	participant — run challenges/incidents/learn + read status; may NOT mutate
//	              platform/runtime/lab/apps/services.
//
// Password hashing defaults to Argon2id (memory-hard, the modern password-hash
// recommendation) via golang.org/x/crypto/argon2, which is pure Go and builds on
// the Go 1.24 toolchain CI pins. Existing PBKDF2-HMAC-SHA256 hashes remain valid
// — VerifyPassword accepts both schemes — so upgrading is seamless and no stored
// hash has to be rewritten. (x/crypto/bcrypt is deliberately avoided: it needs
// Go >= 1.25.)
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"gopkg.in/yaml.v3"
)

// Roles.
const (
	RoleOperator    = "operator"
	RoleParticipant = "participant"
)

// pbkdf2 parameters (legacy hashes only; still verified, never freshly minted).
const (
	pbkdf2Iterations = 210_000
	pbkdf2KeyLen     = 32
	pbkdf2SaltLen    = 16
	schemePBKDF2     = "pbkdf2-sha256"
)

// Argon2id parameters for freshly minted hashes. These follow the OWASP
// second-recommended profile (64 MiB, t=3, p=4) — a comfortable cost for a
// login path while being memory-hard against GPU cracking. They are encoded
// into each hash, so tuning them later does not invalidate existing hashes.
const (
	schemeArgon2id = "argon2id"
	argonMemoryKiB = 64 * 1024
	argonTime      = 3
	argonThreads   = 4
	argonKeyLen    = 32
	argonSaltLen   = 16
)

// ValidRole reports whether r is a recognised role.
func ValidRole(r string) bool {
	return r == RoleOperator || r == RoleParticipant
}

// User is a single account from the users file.
type User struct {
	Name         string `yaml:"name"`
	PasswordHash string `yaml:"passwordHash"`
	Role         string `yaml:"role"`
}

// UsersFile is the on-disk shape of .labctl/users.yaml.
type UsersFile struct {
	Users []User `yaml:"users"`
}

// Enabled reports whether authentication is turned on (LABCTL_AUTH=true).
func Enabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("LABCTL_AUTH")), "true")
}

// DefaultUsersPath returns the users file location. LABCTL_USERS_FILE overrides
// it (the team-mode Helm chart mounts the users Secret at a fixed path separate
// from the .labctl history PVC); otherwise it falls back to the conventional
// location under the project's .labctl directory.
func DefaultUsersPath(projectRoot string) string {
	if p := os.Getenv("LABCTL_USERS_FILE"); p != "" {
		return p
	}
	return filepath.Join(projectRoot, ".labctl", "users.yaml")
}

// HashPassword returns an encoded Argon2id hash of the form
// "argon2id$<mKiB>$<t>$<p>$<saltB64>$<hashB64>". It generates a fresh random
// salt. Legacy PBKDF2 hashes are still accepted by VerifyPassword, but every new
// hash is Argon2id.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password must not be empty")
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}
	dk := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	return fmt.Sprintf("%s$%d$%d$%d$%s$%s",
		schemeArgon2id,
		argonMemoryKiB, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(dk),
	), nil
}

// VerifyPassword reports whether password matches the encoded hash, dispatching
// on the scheme prefix so both Argon2id (current) and PBKDF2 (legacy) hashes
// verify. It returns false (never an error) for malformed hashes so callers can
// treat any failure as "wrong password" without leaking which part failed.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) == 0 {
		return false
	}
	switch parts[0] {
	case schemeArgon2id:
		return verifyArgon2id(parts, password)
	case schemePBKDF2:
		return verifyPBKDF2(parts, password)
	default:
		return false
	}
}

// verifyArgon2id checks an "argon2id$<mKiB>$<t>$<p>$<saltB64>$<hashB64>" hash.
func verifyArgon2id(parts []string, password string) bool {
	if len(parts) != 6 {
		return false
	}
	mem, err1 := strconv.Atoi(parts[1])
	t, err2 := strconv.Atoi(parts[2])
	p, err3 := strconv.Atoi(parts[3])
	// Bound every parameter so the conversions below can't overflow and a
	// malformed hash can't ask argon2 for an absurd amount of work.
	if err1 != nil || err2 != nil || err3 != nil ||
		mem <= 0 || mem > 1<<24 || // ≤ 16 GiB in KiB
		t <= 0 || t > 1<<16 ||
		p <= 0 || p > 255 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	//nolint:gosec // G115: t, mem, p are range-checked just above.
	got := argon2.IDKey([]byte(password), salt, uint32(t), uint32(mem), uint8(p), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// verifyPBKDF2 checks a legacy "pbkdf2-sha256$<iter>$<saltB64>$<hashB64>" hash.
func verifyPBKDF2(parts []string, password string) bool {
	if len(parts) != 4 {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// Store is an in-memory snapshot of the users file, indexed by name.
type Store struct {
	byName map[string]User
}

// NewStore returns an empty user store (no accounts).
func NewStore() *Store {
	return &Store{byName: map[string]User{}}
}

// LoadStore reads and validates the users file at path. A missing file yields
// an empty store (auth enabled but no users => nobody can log in, which is a
// safe default the server surfaces clearly).
func LoadStore(path string) (*Store, error) {
	s := &Store{byName: map[string]User{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var uf UsersFile
	if err := yaml.Unmarshal(data, &uf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	for i, u := range uf.Users {
		if strings.TrimSpace(u.Name) == "" {
			return nil, fmt.Errorf("user %d: name is required", i+1)
		}
		if !ValidRole(u.Role) {
			return nil, fmt.Errorf("user %q: invalid role %q (want operator|participant)", u.Name, u.Role)
		}
		if u.PasswordHash == "" {
			return nil, fmt.Errorf("user %q: passwordHash is required", u.Name)
		}
		if _, dup := s.byName[u.Name]; dup {
			return nil, fmt.Errorf("user %q: duplicate name", u.Name)
		}
		s.byName[u.Name] = u
	}
	return s, nil
}

// dummyHash is a valid Argon2id hash of a random password, computed once. An
// unknown-username login is verified against it so the request does the same
// memory-hard work as a real one — keeping response timing (and cost) uniform,
// which denies an attacker a username-enumeration oracle.
var dummyHash = func() string {
	h, err := HashPassword("unused-placeholder-password")
	if err != nil { // HashPassword only fails on empty input; never here.
		panic("auth: computing dummy hash: " + err.Error())
	}
	return h
}()

// Authenticate returns the user if name exists and password verifies.
func (s *Store) Authenticate(name, password string) (User, bool) {
	u, ok := s.byName[name]
	if !ok {
		// Dummy verification against a real Argon2id hash: same work as the hit
		// path, so timing does not reveal whether the username exists.
		VerifyPassword(dummyHash, password)
		return User{}, false
	}
	if !VerifyPassword(u.PasswordHash, password) {
		return User{}, false
	}
	return u, true
}

// Count returns the number of loaded users.
func (s *Store) Count() int { return len(s.byName) }
