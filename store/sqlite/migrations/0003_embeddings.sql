CREATE TABLE embeddings (
    org_id TEXT NOT NULL,
    memory_id TEXT NOT NULL,
    model TEXT NOT NULL,
    dims INTEGER NOT NULL,
    vector BLOB NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (org_id, memory_id, model)
);

CREATE INDEX embeddings_model ON embeddings (org_id, model, created_at);
