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
// Password hashing uses the Go 1.24 standard library crypto/pbkdf2 rather than
// an external bcrypt dependency: x/crypto/bcrypt requires Go >= 1.25, which
// would break CI (pinned to Go 1.24). PBKDF2-HMAC-SHA256 with a per-user random
// salt and a high iteration count is a sound, FIPS-approved password hash.
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

	"gopkg.in/yaml.v3"
)

// Roles.
const (
	RoleOperator    = "operator"
	RoleParticipant = "participant"
)

// pbkdf2 parameters. iterations is deliberately high; 32-byte salt + key.
const (
	pbkdf2Iterations = 210_000
	pbkdf2KeyLen     = 32
	pbkdf2SaltLen    = 16
	hashScheme       = "pbkdf2-sha256"
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

// HashPassword returns an encoded PBKDF2 hash string of the form
// "pbkdf2-sha256$<iter>$<saltB64>$<hashB64>". It generates a fresh random salt.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password must not be empty")
	}
	salt := make([]byte, pbkdf2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating salt: %w", err)
	}
	dk, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyLen)
	if err != nil {
		return "", fmt.Errorf("deriving key: %w", err)
	}
	return fmt.Sprintf("%s$%d$%s$%s",
		hashScheme,
		pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(dk),
	), nil
}

// VerifyPassword reports whether password matches the encoded hash. It returns
// false (never an error) for malformed hashes so callers can treat any failure
// as "wrong password" without leaking which part failed.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != hashScheme {
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

// Authenticate returns the user if name exists and password verifies.
func (s *Store) Authenticate(name, password string) (User, bool) {
	u, ok := s.byName[name]
	if !ok {
		// Run a dummy verification to keep timing roughly constant whether or
		// not the user exists (mitigates username enumeration via timing).
		VerifyPassword("pbkdf2-sha256$1$AAAA$AAAA", password)
		return User{}, false
	}
	if !VerifyPassword(u.PasswordHash, password) {
		return User{}, false
	}
	return u, true
}

// Count returns the number of loaded users.
func (s *Store) Count() int { return len(s.byName) }
