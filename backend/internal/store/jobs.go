package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const jobColumns = `j.id, j.user_id, j.keyword, j.filter, j.max_items, j.status, j.posts_seen,
	j.posts_count, j.new_posts_count, j.error, j.file_key, j.attempts, j.schedule_id,
	j.created_at, j.started_at, j.finished_at`

func scanJob(row pgx.Row, extra ...any) (Job, error) {
	var j Job
	dest := []any{&j.ID, &j.UserID, &j.Keyword, &j.Filter, &j.MaxItems, &j.Status, &j.PostsSeen,
		&j.PostsCount, &j.NewPostsCount, &j.Error, &j.FileKey, &j.Attempts, &j.ScheduleID,
		&j.CreatedAt, &j.StartedAt, &j.FinishedAt}
	err := row.Scan(append(dest, extra...)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return j, ErrNotFound
	}
	return j, err
}

// NormalizeKeyword identifies "the same search" across runs.
func NormalizeKeyword(k string) string {
	return strings.ToLower(strings.Join(strings.Fields(k), " "))
}

// CountActiveJobs returns how many of the user's jobs are queued or running.
func (s *Store) CountActiveJobs(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE user_id = $1 AND status IN ('queued', 'running')`, userID).Scan(&n)
	return n, err
}

// CountJobsSince returns how many jobs the user created after `since`.
func (s *Store) CountJobsSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE user_id = $1 AND created_at > $2`, userID, since).Scan(&n)
	return n, err
}

func (s *Store) CreateJob(ctx context.Context, userID int64, keyword, filter string, maxItems int) (Job, error) {
	return scanJob(s.pool.QueryRow(ctx,
		`INSERT INTO jobs AS j (user_id, keyword, keyword_norm, filter, max_items)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING `+jobColumns,
		userID, keyword, NormalizeKeyword(keyword), filter, maxItems))
}

// Extra columns for user-facing job reads: queue position and "first run of this search".
// isFirstRun mirrors the is_new rule in SaveResults: an earlier job of the same search with saved posts.
const jobExtraExpr = `CASE WHEN j.status = 'queued' THEN
	(SELECT count(*) FROM jobs q WHERE q.status = 'queued' AND q.created_at <= j.created_at)::int END,
	NOT EXISTS (
		SELECT 1 FROM jobs pj JOIN job_posts jp ON jp.job_id = pj.id
		WHERE pj.user_id = j.user_id AND pj.keyword_norm = j.keyword_norm AND pj.id < j.id
	)`

func (s *Store) ListJobs(ctx context.Context, userID int64, limit int) ([]Job, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+jobColumns+`, `+jobExtraExpr+`
		 FROM jobs j WHERE j.user_id = $1 ORDER BY j.created_at DESC LIMIT $2`,
		userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []Job{}
	for rows.Next() {
		var pos *int
		var first bool
		j, err := scanJob(rows, &pos, &first)
		if err != nil {
			return nil, err
		}
		j.QueuePosition, j.IsFirstRun = pos, first
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// GetJob returns a job only if it belongs to the user.
func (s *Store) GetJob(ctx context.Context, userID, jobID int64) (Job, error) {
	var pos *int
	var first bool
	j, err := scanJob(s.pool.QueryRow(ctx,
		`SELECT `+jobColumns+`, `+jobExtraExpr+` FROM jobs j WHERE j.id = $1 AND j.user_id = $2`,
		jobID, userID), &pos, &first)
	j.QueuePosition, j.IsFirstRun = pos, first
	return j, err
}

// GetJobByID is for internal (worker) use - no ownership check.
func (s *Store) GetJobByID(ctx context.Context, jobID int64) (Job, error) {
	var pos *int
	var first bool
	j, err := scanJob(s.pool.QueryRow(ctx, `SELECT `+jobColumns+`, `+jobExtraExpr+` FROM jobs j WHERE j.id = $1`, jobID), &pos, &first)
	j.QueuePosition, j.IsFirstRun = pos, first
	return j, err
}

// CancelJob cancels a queued job. Running jobs cannot be canceled (the browser is already working).
func (s *Store) CancelJob(ctx context.Context, userID, jobID int64) (Job, error) {
	j, err := scanJob(s.pool.QueryRow(ctx,
		`UPDATE jobs j SET status = 'canceled', finished_at = now()
		 WHERE j.id = $1 AND j.user_id = $2 AND j.status = 'queued'
		 RETURNING `+jobColumns, jobID, userID))
	if errors.Is(err, ErrNotFound) {
		if _, getErr := s.GetJob(ctx, userID, jobID); getErr == nil {
			return j, ErrJobState
		}
	}
	return j, err
}

// ClaimJob atomically takes the next queued job and marks it running.
//
// Fair scheduling: the job of the user who was served least recently goes first,
// so one user with many jobs cannot block everybody else. SKIP LOCKED makes it
// safe with several workers.
func (s *Store) ClaimJob(ctx context.Context, workerID string) (Job, error) {
	return scanJob(s.pool.QueryRow(ctx,
		`WITH next AS (
			SELECT q.id FROM jobs q
			WHERE q.status = 'queued'
			ORDER BY (SELECT max(r.started_at) FROM jobs r WHERE r.user_id = q.user_id) ASC NULLS FIRST,
			         q.created_at ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE jobs j
		SET status = 'running', started_at = now(), heartbeat_at = now(),
		    attempts = j.attempts + 1, worker_id = $1, posts_seen = 0, error = NULL
		FROM next WHERE j.id = next.id
		RETURNING `+jobColumns, workerID))
}

// Heartbeat records progress of a running job.
func (s *Store) Heartbeat(ctx context.Context, jobID int64, postsSeen int) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs SET heartbeat_at = now(), posts_seen = GREATEST(posts_seen, $2)
		 WHERE id = $1 AND status = 'running'`, jobID, postsSeen)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrJobState
	}
	return err
}

// SaveResults stores the posts of a running job and marks which ones are new
// for this search (same user + same normalized keyword). Returns posts in the
// order the worker sent them.
func (s *Store) SaveResults(ctx context.Context, jobID int64, posts []Post) ([]SavedPost, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var userID int64
	var keywordNorm, status string
	err = tx.QueryRow(ctx, `SELECT user_id, keyword_norm, status FROM jobs WHERE id = $1 FOR UPDATE`, jobID).
		Scan(&userID, &keywordNorm, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != JobRunning {
		return nil, ErrJobState
	}
	// A retried job may already have partial rows from a previous attempt
	if _, err := tx.Exec(ctx, `DELETE FROM job_posts WHERE job_id = $1`, jobID); err != nil {
		return nil, err
	}

	batch := &pgx.Batch{}
	for _, p := range posts {
		images, _ := json.Marshal(nonNil(p.Images))
		videos, _ := json.Marshal(nonNil(p.Videos))
		links, _ := json.Marshal(nonNil(p.Links))
		batch.Queue(
			`INSERT INTO posts (id, url, author_username, author_name, author_verified, author_url, content,
				published_at, likes, replies, reposts, shares, images, videos, links, quoted_post_url)
			 VALUES ($1, $2, $3, NULLIF($4, ''), $5, NULLIF($6, ''), $7, $8, $9, $10, $11, $12, $13, $14, $15, NULLIF($16, ''))
			 ON CONFLICT (id) DO UPDATE SET
				likes = EXCLUDED.likes, replies = EXCLUDED.replies, reposts = EXCLUDED.reposts,
				shares = EXCLUDED.shares, content = EXCLUDED.content,
				author_name = COALESCE(EXCLUDED.author_name, posts.author_name),
				author_verified = EXCLUDED.author_verified, images = EXCLUDED.images,
				videos = EXCLUDED.videos, links = EXCLUDED.links, updated_at = now()`,
			p.ID, p.URL, p.AuthorUsername, p.AuthorName, p.AuthorVerified, p.AuthorURL, p.Content,
			p.PublishedAt, p.Likes, p.Replies, p.Reposts, p.Shares, images, videos, links, p.QuotedPostURL,
		)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return nil, fmt.Errorf("upsert posts: %w", err)
	}

	ids := make([]string, len(posts))
	for i, p := range posts {
		ids[i] = p.ID
	}
	rows, err := tx.Query(ctx,
		`INSERT INTO job_posts (job_id, post_id, is_new, position)
		 SELECT $1, t.id,
		        NOT EXISTS (
		            SELECT 1 FROM job_posts jp JOIN jobs pj ON pj.id = jp.job_id
		            WHERE jp.post_id = t.id AND pj.user_id = $2 AND pj.keyword_norm = $3 AND pj.id <> $1
		        ),
		        t.pos
		 FROM unnest($4::text[]) WITH ORDINALITY AS t(id, pos)
		 RETURNING post_id, is_new`,
		jobID, userID, keywordNorm, ids)
	if err != nil {
		return nil, fmt.Errorf("link posts: %w", err)
	}
	isNew := make(map[string]bool, len(posts))
	for rows.Next() {
		var id string
		var n bool
		if err := rows.Scan(&id, &n); err != nil {
			rows.Close()
			return nil, err
		}
		isNew[id] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	saved := make([]SavedPost, len(posts))
	for i, p := range posts {
		saved[i] = SavedPost{Post: p, IsNew: isNew[p.ID]}
	}
	return saved, nil
}

func (s *Store) MarkDone(ctx context.Context, jobID int64, fileKey string, total, newCount int) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs SET status = 'done', finished_at = now(), file_key = $2,
		        posts_count = $3, new_posts_count = $4, posts_seen = GREATEST(posts_seen, $3)
		 WHERE id = $1 AND status = 'running'`, jobID, fileKey, total, newCount)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrJobState
	}
	return err
}

func (s *Store) MarkFailed(ctx context.Context, jobID int64, message string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs SET status = 'failed', finished_at = now(), error = $2
		 WHERE id = $1 AND status = 'running'`, jobID, message)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrJobState
	}
	return err
}

// RecoverStaleJobs handles running jobs whose worker stopped sending heartbeats
// (crash, container restart): requeue them, or fail them after maxAttempts.
func (s *Store) RecoverStaleJobs(ctx context.Context, staleAfter time.Duration, maxAttempts int) (requeued, failed int64, err error) {
	cutoff := time.Now().Add(-staleAfter)
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs SET status = 'queued', started_at = NULL, heartbeat_at = NULL, worker_id = NULL, posts_seen = 0
		 WHERE status = 'running' AND heartbeat_at < $1 AND attempts < $2`, cutoff, maxAttempts)
	if err != nil {
		return 0, 0, err
	}
	requeued = tag.RowsAffected()
	tag, err = s.pool.Exec(ctx,
		`UPDATE jobs SET status = 'failed', finished_at = now(),
		        error = 'Воркер перестал отвечать во время выполнения задачи'
		 WHERE status = 'running' AND heartbeat_at < $1 AND attempts >= $2`, cutoff, maxAttempts)
	if err != nil {
		return requeued, 0, err
	}
	return requeued, tag.RowsAffected(), nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
