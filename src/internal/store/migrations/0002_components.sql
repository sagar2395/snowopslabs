-- 0002_components — the installed-component inventory (W3-T04).
--
-- The runs table records *operations*; this records their lasting effect: what
-- is currently on the cluster because labctl put it there. It is the authority
-- behind exact teardown — `down` uninstalls precisely what was installed and can
-- report anything it could not remove, instead of exiting 0 and hoping.
--
-- A component is identified by a stable string id ("platform:ingress/traefik").
-- Installs upsert it to 'installed'; uninstalls mark it 'removed' rather than
-- deleting the row, so the history of what was here survives a teardown.

CREATE TABLE components (
    id           TEXT    PRIMARY KEY,           -- stable identity, e.g. "platform:ingress/traefik"
    kind         TEXT    NOT NULL,              -- platform | scenario (extensible)
    owner        TEXT    NOT NULL DEFAULT '',   -- the scenario a component belongs to; '' for platform
    ref          TEXT    NOT NULL,              -- category/provider (platform) or component name (scenario)
    namespace    TEXT    NOT NULL DEFAULT '',   -- where it lives, when known — for display and exact teardown
    status       TEXT    NOT NULL,              -- installed | removed
    install_run  TEXT,                          -- the run that installed it (no FK: runs are pruned, inventory is not)
    remove_run   TEXT,                          -- the run that removed it
    installed_at INTEGER,
    removed_at   INTEGER,
    updated_at   INTEGER NOT NULL,

    CHECK (status IN ('installed', 'removed'))
);

-- Teardown lists the currently-installed set; keep that scan cheap.
CREATE INDEX components_status_idx ON components (status);
CREATE INDEX components_owner_idx  ON components (owner);
