CREATE TABLE IF NOT EXISTS kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    thread_id         INTEGER NOT NULL UNIQUE,
    project           TEXT    NOT NULL,
    cwd               TEXT    NOT NULL,
    title             TEXT    NOT NULL,
    claude_session_id TEXT    NOT NULL DEFAULT '',
    state             TEXT    NOT NULL,
    mode              TEXT    NOT NULL DEFAULT 'default',
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS permission_rules (
    project    TEXT    NOT NULL,
    tool       TEXT    NOT NULL,
    pattern    TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (project, tool, pattern)
);

CREATE TABLE IF NOT EXISTS usage (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    at           INTEGER NOT NULL, -- unix milliseconds
    thread_id    INTEGER NOT NULL,
    project      TEXT    NOT NULL,
    model        TEXT    NOT NULL,
    input        INTEGER NOT NULL,
    output       INTEGER NOT NULL,
    cache_read   INTEGER NOT NULL,
    cache_create INTEGER NOT NULL,
    cost         REAL    NOT NULL
);
CREATE INDEX IF NOT EXISTS usage_at ON usage(at);
CREATE INDEX IF NOT EXISTS usage_thread ON usage(thread_id);

CREATE TABLE IF NOT EXISTS rate_limits (
    name        TEXT PRIMARY KEY,
    status      TEXT    NOT NULL,
    utilization REAL    NOT NULL,
    resets_at   INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL
);

-- Messages in the control topic that the node deletes later.
CREATE TABLE IF NOT EXISTS control_msgs (
    msg_id  INTEGER PRIMARY KEY,
    sent_at INTEGER NOT NULL
);
