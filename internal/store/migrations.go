package store

const schemaSQL = `
CREATE TABLE IF NOT EXISTS exchanges (
    id INTEGER PRIMARY KEY,
    method TEXT NOT NULL,
    scheme TEXT NOT NULL,
    host TEXT NOT NULL,
    path TEXT NOT NULL,
    query TEXT NOT NULL,
    status INTEGER NOT NULL,
    mime_type TEXT NOT NULL,
    request_size INTEGER NOT NULL,
    response_size INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL,
    started_at_unix_nano INTEGER NOT NULL,
    intercepted INTEGER NOT NULL,
    error INTEGER NOT NULL,
    error_message TEXT NOT NULL,
    request_truncated INTEGER NOT NULL,
    response_truncated INTEGER NOT NULL,
    tags_json TEXT NOT NULL,
    note TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS exchange_bodies (
    exchange_id INTEGER PRIMARY KEY REFERENCES exchanges(id) ON DELETE CASCADE,
    request_headers_json TEXT NOT NULL,
    request_body BLOB,
    request_raw BLOB,
    response_headers_json TEXT NOT NULL,
    response_body BLOB,
    response_raw BLOB
);
`
