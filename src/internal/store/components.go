// SPDX-License-Identifier: Apache-2.0

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ComponentStatus is an inventory row's lifecycle state.
type ComponentStatus string

const (
	// ComponentInstalled means the component is on the cluster (labctl put it there).
	ComponentInstalled ComponentStatus = "installed"
	// ComponentRemoved means it was uninstalled; the row is kept as history.
	ComponentRemoved ComponentStatus = "removed"
)

// Component is one row of the installed-component inventory: the
// lasting effect of an install/uninstall operation, as opposed to the operation
// itself (a run). Teardown reads this to remove exactly what was installed.
type Component struct {
	ID          string // stable identity, e.g. "platform:ingress/traefik"
	Kind        string // platform | scenario
	Owner       string // the scenario a component belongs to; "" for platform
	Ref         string // category/provider (platform) or component name (scenario)
	Namespace   string // where it lives, when known
	Status      ComponentStatus
	InstallRun  string
	RemoveRun   string
	InstalledAt time.Time
	RemovedAt   time.Time
	UpdatedAt   time.Time
}

// ErrComponentNotFound is returned when no inventory row matches an id.
var ErrComponentNotFound = errors.New("component not found")

// RecordComponentInstalled upserts a component as installed. Installing the same
// component again (idempotent re-install, or a re-install after a removal) keeps
// one row and refreshes its install run and timestamp — the inventory tracks
// what is here now, not every time it was touched.
func (s *Store) RecordComponentInstalled(ctx context.Context, c Component) error {
	if c.ID == "" || c.Kind == "" {
		return errors.New("store: component id and kind are required")
	}
	at := c.InstalledAt
	if at.IsZero() {
		at = s.now()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO components (id, kind, owner, ref, namespace, status,
		                        install_run, installed_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 'installed', ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			kind         = excluded.kind,
			owner        = excluded.owner,
			ref          = excluded.ref,
			namespace    = excluded.namespace,
			status       = 'installed',
			install_run  = excluded.install_run,
			remove_run   = NULL,
			removed_at   = NULL,
			installed_at = excluded.installed_at,
			updated_at   = excluded.updated_at`,
		c.ID, c.Kind, c.Owner, c.Ref, c.Namespace,
		nullString(c.InstallRun), at.UnixMicro(), at.UnixMicro())
	if err != nil {
		return fmt.Errorf("recording component %s: %w", c.ID, err)
	}
	return nil
}

// MarkComponentRemoved flags a component removed, keeping the row as history. It
// is a no-op error (ErrComponentNotFound) if the component was never recorded,
// so a teardown of something installed outside labctl is reported, not hidden.
func (s *Store) MarkComponentRemoved(ctx context.Context, id, removeRun string, at time.Time) error {
	if at.IsZero() {
		at = s.now()
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE components
		   SET status = 'removed', remove_run = ?, removed_at = ?, updated_at = ?
		 WHERE id = ?`,
		nullString(removeRun), at.UnixMicro(), at.UnixMicro(), id)
	if err != nil {
		return fmt.Errorf("removing component %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrComponentNotFound, id)
	}
	return nil
}

// ComponentFilter narrows ListComponents. The zero value lists every component.
type ComponentFilter struct {
	Status ComponentStatus // "" for any
	Kind   string          // "" for any
	Owner  string          // "" for any
}

// ListComponents returns inventory rows matching the filter, ordered by id.
func (s *Store) ListComponents(ctx context.Context, f ComponentFilter) ([]Component, error) {
	var (
		where []string
		args  []any
	)
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, string(f.Status))
	}
	if f.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, f.Kind)
	}
	if f.Owner != "" {
		where = append(where, "owner = ?")
		args = append(args, f.Owner)
	}
	query := selectComponentColumns
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY id ASC"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("listing components: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Component
	for rows.Next() {
		c, err := scanComponent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetComponent loads one inventory row.
func (s *Store) GetComponent(ctx context.Context, id string) (Component, error) {
	row := s.db.QueryRowContext(ctx, selectComponentColumns+" WHERE id = ?", id)
	c, err := scanComponent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Component{}, fmt.Errorf("%w: %s", ErrComponentNotFound, id)
	}
	return c, err
}

const selectComponentColumns = `
	SELECT id, kind, owner, ref, namespace, status, install_run, remove_run,
	       installed_at, removed_at, updated_at
	  FROM components`

func scanComponent(sc scanner) (Component, error) {
	var (
		c           Component
		status      string
		installRun  sql.NullString
		removeRun   sql.NullString
		installedAt sql.NullInt64
		removedAt   sql.NullInt64
		updatedAt   int64
	)
	if err := sc.Scan(&c.ID, &c.Kind, &c.Owner, &c.Ref, &c.Namespace, &status,
		&installRun, &removeRun, &installedAt, &removedAt, &updatedAt); err != nil {
		return Component{}, err
	}
	c.Status = ComponentStatus(status)
	c.InstallRun = installRun.String
	c.RemoveRun = removeRun.String
	c.InstalledAt = fromMicro(installedAt)
	c.RemovedAt = fromMicro(removedAt)
	c.UpdatedAt = time.UnixMicro(updatedAt).UTC()
	return c, nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
