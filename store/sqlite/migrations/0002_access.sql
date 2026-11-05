CREATE TABLE api_keys (
    org_id TEXT NOT NULL,
    id TEXT NOT NULL,
    name TEXT NOT NULL,
    hash TEXT NOT NULL,
    last4 TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_used_at TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, name),
    UNIQUE (org_id, hash)
);

CREATE TABLE bindings (
    org_id TEXT NOT NULL,
    id TEXT NOT NULL,
    principal TEXT NOT NULL,
    role TEXT NOT NULL,
    target TEXT NOT NULL,
    created_at TEXT NOT NULL,
    version INTEGER NOT NULL,
    PRIMARY KEY (org_id, id),
    UNIQUE (org_id, principal, role, target)
);
