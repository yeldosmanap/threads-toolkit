-- +goose Up
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT        NOT NULL,
    password_hash TEXT        NOT NULL,
    is_admin      BOOLEAN     NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_lower ON users (lower(email));

-- Only a SHA-256 of the session token is stored; the token itself lives in the user's cookie
CREATE TABLE sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX sessions_user ON sessions (user_id);

-- One row = one search run. Also serves as the work queue (status = 'queued').
CREATE TABLE jobs (
    id              BIGSERIAL PRIMARY KEY,
    user_id         BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    keyword         TEXT        NOT NULL,
    -- lower-cased, trimmed keyword: identifies "the same search" for new-post detection
    keyword_norm    TEXT        NOT NULL,
    filter          TEXT        NOT NULL CHECK (filter IN ('recent', 'top')),
    max_items       INT         NOT NULL CHECK (max_items > 0),
    status          TEXT        NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'done', 'failed', 'canceled')),
    posts_seen      INT         NOT NULL DEFAULT 0,
    posts_count     INT,
    new_posts_count INT,
    error           TEXT,
    file_key        TEXT,
    attempts        INT         NOT NULL DEFAULT 0,
    worker_id       TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at      TIMESTAMPTZ,
    heartbeat_at    TIMESTAMPTZ,
    finished_at     TIMESTAMPTZ
);
CREATE INDEX jobs_user_created ON jobs (user_id, created_at DESC);
CREATE INDEX jobs_user_keyword ON jobs (user_id, keyword_norm);
CREATE INDEX jobs_queued ON jobs (created_at) WHERE status = 'queued';
CREATE INDEX jobs_running ON jobs (heartbeat_at) WHERE status = 'running';

-- Posts are global (a post is the same no matter who searched for it); stats keep the latest values
CREATE TABLE posts (
    id              TEXT PRIMARY KEY,
    url             TEXT        NOT NULL,
    author_username TEXT        NOT NULL,
    author_name     TEXT,
    author_verified BOOLEAN     NOT NULL DEFAULT false,
    author_url      TEXT,
    content         TEXT        NOT NULL,
    published_at    TIMESTAMPTZ,
    likes           INT         NOT NULL DEFAULT 0,
    replies         INT         NOT NULL DEFAULT 0,
    reposts         INT         NOT NULL DEFAULT 0,
    shares          INT         NOT NULL DEFAULT 0,
    images          JSONB       NOT NULL DEFAULT '[]',
    videos          JSONB       NOT NULL DEFAULT '[]',
    links           JSONB       NOT NULL DEFAULT '[]',
    quoted_post_url TEXT,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX posts_published ON posts (published_at DESC);

CREATE TABLE job_posts (
    job_id   BIGINT  NOT NULL REFERENCES jobs (id) ON DELETE CASCADE,
    post_id  TEXT    NOT NULL REFERENCES posts (id),
    -- true if no earlier run of the same search (same user + keyword) found this post
    is_new   BOOLEAN NOT NULL,
    position INT     NOT NULL,
    PRIMARY KEY (job_id, post_id)
);
CREATE INDEX job_posts_post ON job_posts (post_id);

-- +goose Down
DROP TABLE job_posts;
DROP TABLE posts;
DROP TABLE jobs;
DROP TABLE sessions;
DROP TABLE users;
