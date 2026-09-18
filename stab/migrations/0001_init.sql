-- 0001_init.sql — initial stab schema (local MVP).
-- All timestamps are RFC3339 UTC text. JSON columns hold structured documents.

CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS projects (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    root_path        TEXT NOT NULL UNIQUE,
    image            TEXT,
    profile          TEXT,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS deployments (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL,
    kind           TEXT NOT NULL DEFAULT 'local',
    container_name TEXT,
    tmux_session   TEXT,
    image          TEXT,
    desired_state  TEXT NOT NULL DEFAULT 'pending',
    observed_state TEXT NOT NULL DEFAULT 'pending',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS containers (
    id            TEXT PRIMARY KEY,
    deployment_id TEXT NOT NULL,
    name          TEXT NOT NULL,
    status        TEXT,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tmux_sessions (
    id            TEXT PRIMARY KEY,
    deployment_id TEXT NOT NULL,
    name          TEXT NOT NULL,
    windows       TEXT,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS audit_events (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    at           TEXT NOT NULL,
    actor        TEXT,
    action       TEXT NOT NULL,
    subject_type TEXT,
    subject_id   TEXT,
    detail       TEXT
);

CREATE INDEX IF NOT EXISTS idx_projects_root ON projects(root_path);
CREATE INDEX IF NOT EXISTS idx_deployments_project ON deployments(project_id);
CREATE INDEX IF NOT EXISTS idx_containers_deployment ON containers(deployment_id);
