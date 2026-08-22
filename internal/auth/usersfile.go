// SPDX-License-Identifier: Apache-2.0
package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// LoadUsersFile reads .labctl/users.yaml for editing. A missing file yields an
// empty UsersFile (so `users add` works on first use).
func LoadUsersFile(path string) (*UsersFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &UsersFile{}, nil
		}
		return nil, err
	}
	var uf UsersFile
	if err := yaml.Unmarshal(data, &uf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &uf, nil
}

// SaveUsersFile writes the users file atomically (write temp + rename) with
// 0o600 permissions, since it contains password hashes.
func SaveUsersFile(path string, uf *UsersFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Stable order so diffs are clean.
	sort.SliceStable(uf.Users, func(i, j int) bool {
		return uf.Users[i].Name < uf.Users[j].Name
	})
	data, err := yaml.Marshal(uf)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Find returns the index of the named user, or -1.
func (uf *UsersFile) Find(name string) int {
	for i := range uf.Users {
		if uf.Users[i].Name == name {
			return i
		}
	}
	return -1
}

// Upsert adds or replaces a user by name.
func (uf *UsersFile) Upsert(u User) {
	if i := uf.Find(u.Name); i >= 0 {
		uf.Users[i] = u
		return
	}
	uf.Users = append(uf.Users, u)
}

// Remove deletes the named user, reporting whether it existed.
func (uf *UsersFile) Remove(name string) bool {
	i := uf.Find(name)
	if i < 0 {
		return false
	}
	uf.Users = append(uf.Users[:i], uf.Users[i+1:]...)
	return true
}
