-- +goose Up
-- A project is a company a user tracks: several ways people mention it + context for the classifier.
CREATE TABLE projects (
    id                BIGSERIAL PRIMARY KEY,
    user_id           BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name              TEXT        NOT NULL,
    market            TEXT        NOT NULL DEFAULT 'Казахстан',
    aliases           TEXT[]      NOT NULL,
    official_accounts TEXT[]      NOT NULL DEFAULT '{}',
    competitors       TEXT[]      NOT NULL DEFAULT '{}',
    notes             TEXT        NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX projects_user ON projects (user_id, created_at DESC);

-- Project runs search every alias; analysis runs after scraping.
ALTER TABLE jobs
    ADD COLUMN project_id       BIGINT REFERENCES projects (id) ON DELETE SET NULL,
    ADD COLUMN keywords         TEXT[],
    -- NULL = no analysis (plain keyword search); pending -> running -> done | failed
    ADD COLUMN analysis_status  TEXT CHECK (analysis_status IN ('pending', 'running', 'done', 'failed')),
    ADD COLUMN analysis_error   TEXT,
    ADD COLUMN removed_file_key TEXT;
CREATE INDEX jobs_analysis_pending ON jobs (finished_at) WHERE analysis_status = 'pending';

-- Parent post of a comment / other parts of a multi-part post, as scraped
ALTER TABLE posts ADD COLUMN context TEXT;

-- Classifier output per (project, post): a post found again in a later run is not re-billed.
CREATE TABLE post_labels (
    project_id     BIGINT      NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    post_id        TEXT        NOT NULL REFERENCES posts (id),
    category       TEXT        NOT NULL,
    market         TEXT        NOT NULL,
    sentiment      TEXT        NOT NULL,
    aspects        TEXT[]      NOT NULL DEFAULT '{}',
    language       TEXT        NOT NULL,
    reason         TEXT        NOT NULL,
    excluded       BOOLEAN     NOT NULL,
    model          TEXT        NOT NULL,
    prompt_version TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, post_id)
);

-- +goose Down
DROP TABLE post_labels;
ALTER TABLE posts DROP COLUMN context;
ALTER TABLE jobs
    DROP COLUMN project_id,
    DROP COLUMN keywords,
    DROP COLUMN analysis_status,
    DROP COLUMN analysis_error,
    DROP COLUMN removed_file_key;
DROP TABLE projects;
