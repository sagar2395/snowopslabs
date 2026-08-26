// SPDX-License-Identifier: Apache-2.0
package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	tests := []struct {
		name     string
		password string
		want     bool
	}{
		{"correct password", "correct horse battery staple", true},
		{"wrong password", "Tr0ub4dor&3", false},
		{"empty password", "", false},
		{"case mismatch", "Correct Horse Battery Staple", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VerifyPassword(hash, tt.password); got != tt.want {
				t.Errorf("VerifyPassword(%q) = %v, want %v", tt.password, got, tt.want)
			}
		})
	}
}

func TestHashPassword_EmptyRejected(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Fatal("expected error hashing empty password")
	}
}

func TestHashPassword_SaltIsRandom(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Fatal("two hashes of the same password should differ (random salt)")
	}
}

func TestVerifyPassword_MalformedHashes(t *testing.T) {
	tests := []struct {
		name    string
		encoded string
	}{
		{"empty", ""},
		{"too few parts", "pbkdf2-sha256$1000$onlythree"},
		{"wrong scheme", "bcrypt$1000$abc$def"},
		{"non-numeric iterations", "pbkdf2-sha256$abc$AAAA$AAAA"},
		{"zero iterations", "pbkdf2-sha256$0$AAAA$AAAA"},
		{"bad base64 salt", "pbkdf2-sha256$1000$!!!!$AAAA"},
		{"bad base64 hash", "pbkdf2-sha256$1000$AAAA$!!!!"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if VerifyPassword(tt.encoded, "anything") {
				t.Errorf("VerifyPassword(%q) = true, want false", tt.encoded)
			}
		})
	}
}

func TestDefaultUsersPath(t *testing.T) {
	t.Run("default under project root", func(t *testing.T) {
		t.Setenv("LABCTL_USERS_FILE", "")
		got := DefaultUsersPath("/proj")
		if got != filepath.Join("/proj", ".labctl", "users.yaml") {
			t.Errorf("DefaultUsersPath = %q", got)
		}
	})
	t.Run("env override wins", func(t *testing.T) {
		t.Setenv("LABCTL_USERS_FILE", "/etc/labctl/users.yaml")
		if got := DefaultUsersPath("/proj"); got != "/etc/labctl/users.yaml" {
			t.Errorf("DefaultUsersPath = %q, want override", got)
		}
	})
}

func TestValidRole(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{RoleOperator, true},
		{RoleParticipant, true},
		{"admin", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := ValidRole(tt.role); got != tt.want {
			t.Errorf("ValidRole(%q) = %v, want %v", tt.role, got, tt.want)
		}
	}
}

func TestEnabled(t *testing.T) {
	tests := []struct {
		val  string
		want bool
	}{
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"false", false},
		{"1", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Setenv("LABCTL_AUTH", tt.val)
		if got := Enabled(); got != tt.want {
			t.Errorf("Enabled() with LABCTL_AUTH=%q = %v, want %v", tt.val, got, tt.want)
		}
	}
}

func writeUsers(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "users.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadStore(t *testing.T) {
	goodHash, _ := HashPassword("pw")

	tests := []struct {
		name      string
		content   string
		wantErr   bool
		wantCount int
	}{
		{
			name: "valid two users",
			content: "users:\n" +
				"  - name: alice\n    role: operator\n    passwordHash: " + goodHash + "\n" +
				"  - name: bob\n    role: participant\n    passwordHash: " + goodHash + "\n",
			wantCount: 2,
		},
		{
			name:    "invalid role",
			content: "users:\n  - name: alice\n    role: admin\n    passwordHash: " + goodHash + "\n",
			wantErr: true,
		},
		{
			name:    "missing name",
			content: "users:\n  - role: operator\n    passwordHash: " + goodHash + "\n",
			wantErr: true,
		},
		{
			name:    "missing passwordHash",
			content: "users:\n  - name: alice\n    role: operator\n",
			wantErr: true,
		},
		{
			name: "duplicate name",
			content: "users:\n" +
				"  - name: alice\n    role: operator\n    passwordHash: " + goodHash + "\n" +
				"  - name: alice\n    role: participant\n    passwordHash: " + goodHash + "\n",
			wantErr: true,
		},
		{
			name:    "malformed yaml",
			content: "users: [this is not valid",
			wantErr: true,
		},
		{
			name:      "empty file",
			content:   "",
			wantCount: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeUsers(t, tt.content)
			store, err := LoadStore(path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if store.Count() != tt.wantCount {
				t.Errorf("Count() = %d, want %d", store.Count(), tt.wantCount)
			}
		})
	}
}

func TestLoadStore_MissingFileIsEmpty(t *testing.T) {
	store, err := LoadStore(filepath.Join(t.TempDir(), "does-not-exist.yaml"))
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if store.Count() != 0 {
		t.Errorf("Count() = %d, want 0", store.Count())
	}
}

func TestStore_Authenticate(t *testing.T) {
	hash, _ := HashPassword("s3cret")
	path := writeUsers(t, "users:\n  - name: alice\n    role: operator\n    passwordHash: "+hash+"\n")
	store, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("valid credentials", func(t *testing.T) {
		u, ok := store.Authenticate("alice", "s3cret")
		if !ok || u.Name != "alice" || u.Role != RoleOperator {
			t.Errorf("Authenticate valid = %+v, %v", u, ok)
		}
	})
	t.Run("wrong password", func(t *testing.T) {
		if _, ok := store.Authenticate("alice", "nope"); ok {
			t.Error("expected auth failure for wrong password")
		}
	})
	t.Run("unknown user", func(t *testing.T) {
		if _, ok := store.Authenticate("mallory", "whatever"); ok {
			t.Error("expected auth failure for unknown user")
		}
	})
}
