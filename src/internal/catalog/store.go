// SPDX-License-Identifier: Apache-2.0
package catalog

import "sync/atomic"

// Store holds the current Catalog snapshot behind an atomic pointer so readers
// never see a half-built catalog. Reload builds a fresh snapshot from the same
// roots and swaps it in one store; a reader calling Current before, during or
// after a Reload always gets a complete, internally consistent snapshot (W2-T02
// atomic hot-reload). Store is safe for concurrent use.
type Store struct {
	roots []string
	cur   atomic.Pointer[Catalog]
}

// NewStore builds the initial snapshot from roots and returns a Store holding
// it. The initial snapshot is loaded even if it has problems, so callers can
// inspect them via Current().Problems().
func NewStore(roots ...string) (*Store, error) {
	s := &Store{roots: append([]string(nil), roots...)}
	c, err := Load(roots...)
	if err != nil {
		return nil, err
	}
	s.cur.Store(c)
	return s, nil
}

// Current returns the live snapshot. It never returns nil after NewStore.
func (s *Store) Current() *Catalog { return s.cur.Load() }

// Reload rebuilds the snapshot from the store's roots and atomically replaces
// the live one. On a load error the existing snapshot is kept and the error
// returned, so a transient filesystem failure never blanks the catalog.
func (s *Store) Reload() (*Catalog, error) {
	c, err := Load(s.roots...)
	if err != nil {
		return s.Current(), err
	}
	s.cur.Store(c)
	return c, nil
}

// Roots returns the roots this store loads from, in precedence order.
func (s *Store) Roots() []string { return append([]string(nil), s.roots...) }
