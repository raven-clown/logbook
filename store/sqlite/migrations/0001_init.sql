CREATE TABLE memories (
    rid INTEGER PRIMARY KEY,
    org_id TEXT NOT NULL,
    id TEXT NOT NULL,
    scope TEXT NOT NULL,
    content TEXT NOT NULL,
    kind TEXT NOT NULL,
    subject TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '[]',
    refs TEXT NOT NULL DEFAULT '[]',
    status TEXT NOT NULL,
    source TEXT NOT NULL,
    evidence TEXT NOT NULL DEFAULT '',
    author_subject TEXT NOT NULL DEFAULT '',
    author_channel TEXT NOT NULL DEFAULT '',
    author_client TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    valid_until TEXT NOT NULL DEFAULT '',
    review_after TEXT NOT NULL DEFAULT '',
    last_confirmed_at TEXT NOT NULL DEFAULT '',
    confirmations INTEGER NOT NULL DEFAULT 0,
    disputes INTEGER NOT NULL DEFAULT 0,
    superseded_by TEXT NOT NULL DEFAULT '',
    superseded_at TEXT NOT NULL DEFAULT '',
    forgotten_at TEXT NOT NULL DEFAULT '',
    purged INTEGER NOT NULL DEFAULT 0,
    content_hash TEXT NOT NULL,
    version INTEGER NOT NULL,
    UNIQUE (org_id, id)
);

CREATE INDEX memories_hash ON memories (org_id, scope, content_hash);
CREATE INDEX memories_subject ON memories (org_id, scope, subject);
CREATE INDEX memories_created ON memories (org_id, created_at);

CREATE VIRTUAL TABLE memories_fts USING fts5(content, subject, tags, tokenize = 'unicode61');

CREATE TABLE edges (
    org_id TEXT NOT NULL,
    from_id TEXT NOT NULL,
    to_id TEXT NOT NULL,
    type TEXT NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    UNIQUE (org_id, from_id, to_id, type)
);

CREATE INDEX edges_to ON edges (org_id, to_id);

CREATE TABLE verifications (
    org_id TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    verifier_subject TEXT NOT NULL,
    verifier_channel TEXT NOT NULL,
    verifier_client TEXT NOT NULL DEFAULT '',
    verdict TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    counted INTEGER NOT NULL,
    at TEXT NOT NULL
);

CREATE INDEX verifications_memory ON verifications (org_id, memory_id);

CREATE TABLE events (
    org_id TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    action TEXT NOT NULL,
    actor_subject TEXT NOT NULL DEFAULT '',
    actor_channel TEXT NOT NULL DEFAULT '',
    actor_client TEXT NOT NULL DEFAULT '',
    at TEXT NOT NULL,
    detail TEXT
);

CREATE INDEX events_memory ON events (org_id, memory_id);
