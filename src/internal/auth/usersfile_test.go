// SPDX-License-Identifier: Apache-2.0
package auth

import (
	"path/filepath"
	"testing"
)

func TestUsersFile_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.yaml")

	// Missing file loads empty.
	uf, err := LoadUsersFile(path)
	if err != nil {
		t.Fatalf("LoadUsersFile (missing): %v", err)
	}
	if len(uf.Users) != 0 {
		t.Fatalf("expected empty, got %d users", len(uf.Users))
	}

	hash, _ := HashPassword("pw")
	uf.Upsert(User{Name: "alice", PasswordHash: hash, Role: RoleOperator})
	uf.Upsert(User{Name: "bob", PasswordHash: hash, Role: RoleParticipant})
	if err := SaveUsersFile(path, uf); err != nil {
		t.Fatalf("SaveUsersFile: %v", err)
	}

	// Reload and verify it parses as a valid store (hashes intact).
	store, err := LoadStore(path)
	if err != nil {
		t.Fatalf("LoadStore after save: %v", err)
	}
	if store.Count() != 2 {
		t.Errorf("Count() = %d, want 2", store.Count())
	}
	if _, ok := store.Authenticate("alice", "pw"); !ok {
		t.Error("alice should authenticate after round-trip")
	}
}

func TestUsersFile_Upsert(t *testing.T) {
	uf := &UsersFile{}
	uf.Upsert(User{Name: "alice", Role: RoleParticipant, PasswordHash: "h1"})
	uf.Upsert(User{Name: "alice", Role: RoleOperator, PasswordHash: "h2"})

	if len(uf.Users) != 1 {
		t.Fatalf("upsert should replace, got %d users", len(uf.Users))
	}
	if uf.Users[0].Role != RoleOperator || uf.Users[0].PasswordHash != "h2" {
		t.Errorf("upsert did not replace fields: %+v", uf.Users[0])
	}
}

func TestUsersFile_RemoveAndFind(t *testing.T) {
	uf := &UsersFile{}
	uf.Upsert(User{Name: "alice", Role: RoleOperator, PasswordHash: "h"})
	uf.Upsert(User{Name: "bob", Role: RoleParticipant, PasswordHash: "h"})

	if uf.Find("alice") < 0 {
		t.Error("Find(alice) should succeed")
	}
	if uf.Find("nobody") != -1 {
		t.Error("Find(nobody) should be -1")
	}
	if !uf.Remove("alice") {
		t.Error("Remove(alice) should report true")
	}
	if uf.Remove("alice") {
		t.Error("Remove(alice) twice should report false")
	}
	if len(uf.Users) != 1 || uf.Users[0].Name != "bob" {
		t.Errorf("after remove, expected only bob, got %+v", uf.Users)
	}
}
