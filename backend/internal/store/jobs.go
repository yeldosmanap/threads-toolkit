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
	j.project_id, j.keywords, j.analysis_status, j.analysis_error, j.removed_file_key,
	j.kind, j.parent_job_id, j.post_urls, j.progress_keyword_index, j.progress_alias_seen, j.stop_requested,
	j.created_at, j.started_at, j.finished_at`

func scanJob(row pgx.Row, extra ...any) (Job, error) {
	var j Job
	dest := []any{&j.ID, &j.UserID, &j.Keyword, &j.Filter, &j.MaxItems, &j.Status, &j.PostsSeen,
		&j.PostsCount, &j.NewPostsCount, &j.Error, &j.FileKey, &j.Attempts, &j.ScheduleID,
		&j.ProjectID, &j.Keywords, &j.AnalysisStatus, &j.AnalysisError, &j.RemovedFileKey,
		&j.Kind, &j.ParentJobID, &j.PostURLs, &j.ProgressKeywordIndex, &j.ProgressAliasSeen, &j.StopRequested,
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
		`SELECT count(*) FROM jobs WHERE user_id = $1 AND kind = 'search' AND deleted_at IS NULL AND status IN ('queued', 'running')`, userID).Scan(&n)
	return n, err
}

// CountJobsSince returns how many jobs the user created after `since`.
func (s *Store) CountJobsSince(ctx context.Context, userID int64, since time.Time) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM jobs WHERE user_id = $1 AND kind = 'search' AND created_at > $2`, userID, since).Scan(&n)
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
	),
	CASE WHEN j.project_id IS NULL THEN 0 ELSE
		(SELECT count(*) FROM job_posts x JOIN post_labels pl ON pl.post_id = x.post_id AND pl.project_id = j.project_id
		 WHERE x.job_id = j.id)::int END,
	coalesce((SELECT CASE
			WHEN c.status IN ('queued', 'running') THEN c.status
			WHEN c.status = 'done' AND c.analysis_status IN ('pending', 'running') THEN 'analyzing'
			WHEN c.status = 'done' THEN 'done'
			WHEN c.status = 'canceled' THEN 'canceled'
			ELSE 'failed' END
		FROM jobs c WHERE c.parent_job_id = j.id ORDER BY c.id DESC LIMIT 1), '')`

func (s *Store) ListJobs(ctx context.Context, userID int64, limit int) ([]Job, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+jobColumns+`, `+jobExtraExpr+`
		 FROM jobs j WHERE j.user_id = $1 AND j.kind = 'search' AND j.deleted_at IS NULL ORDER BY j.created_at DESC LIMIT $2`,
		userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := []Job{}
	for rows.Next() {
		var pos *int
		var first bool
		var analyzed int
		var comments string
		j, err := scanJob(rows, &pos, &first, &analyzed, &comments)
		if err != nil {
			return nil, err
		}
		j.QueuePosition, j.IsFirstRun, j.AnalyzedCount, j.CommentsState = pos, first, analyzed, comments
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// GetJob returns a job only if it belongs to the user.
func (s *Store) GetJob(ctx context.Context, userID, jobID int64) (Job, error) {
	var pos *int
	var first bool
	var analyzed int
	var comments string
	j, err := scanJob(s.pool.QueryRow(ctx,
		`SELECT `+jobColumns+`, `+jobExtraExpr+` FROM jobs j WHERE j.id = $1 AND j.user_id = $2 AND j.kind = 'search' AND j.deleted_at IS NULL`,
		jobID, userID), &pos, &first, &analyzed, &comments)
	j.QueuePosition, j.IsFirstRun, j.AnalyzedCount, j.CommentsState = pos, first, analyzed, comments
	return j, err
}

// GetJobByID is for internal (worker) use - no ownership check.
func (s *Store) GetJobByID(ctx context.Context, jobID int64) (Job, error) {
	var pos *int
	var first bool
	var analyzed int
	var comments string
	j, err := scanJob(s.pool.QueryRow(ctx, `SELECT `+jobColumns+`, `+jobExtraExpr+` FROM jobs j WHERE j.id = $1`, jobID), &pos, &first, &analyzed, &comments)
	j.QueuePosition, j.IsFirstRun, j.AnalyzedCount, j.CommentsState = pos, first, analyzed, comments
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
		    attempts = j.attempts + 1, worker_id = $1, posts_seen = 0, error = NULL, stop_requested = false,
		    progress_keyword_index = NULL, progress_alias_seen = NULL
		FROM next WHERE j.id = next.id
		RETURNING `+jobColumns, workerID))
}

// Heartbeat records progress of a running job.
// keywordIndex (1-based) and aliasSeen are reported for project runs; for them postsSeen is the
// worker's current estimate of unique posts and may go down slightly when duplicates are dropped.
// Without them (plain searches, older workers) postsSeen only grows.
// Returns whether the user asked to stop the job early.
func (s *Store) Heartbeat(ctx context.Context, jobID int64, postsSeen int, keywordIndex, aliasSeen *int) (bool, error) {
	var stop bool
	err := s.pool.QueryRow(ctx,
		`UPDATE jobs SET heartbeat_at = now(),
		        posts_seen = CASE WHEN $3::int IS NULL THEN GREATEST(posts_seen, $2) ELSE $2 END,
		        progress_keyword_index = COALESCE($3, progress_keyword_index),
		        progress_alias_seen = COALESCE($4, progress_alias_seen)
		 WHERE id = $1 AND status = 'running'
		 RETURNING stop_requested`, jobID, postsSeen, keywordIndex, aliasSeen).Scan(&stop)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrJobState
	}
	return stop, err
}

// RequestStop asks the worker to finish a running job early with what it has collected.
// For a search that has already finished scraping it stops the follow-up comments collection
// instead (a still-queued comments job is canceled). Returns ErrJobState if nothing can be stopped.
func (s *Store) RequestStop(ctx context.Context, userID, jobID int64) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs SET stop_requested = true
		 WHERE id = $1 AND user_id = $2 AND kind = 'search' AND status = 'running' AND deleted_at IS NULL`, jobID, userID)
	if err != nil || tag.RowsAffected() > 0 {
		return err
	}
	tag, err = s.pool.Exec(ctx,
		`UPDATE jobs SET stop_requested = (status = 'running'),
		        status = CASE WHEN status = 'queued' THEN 'canceled' ELSE status END,
		        finished_at = CASE WHEN status = 'queued' THEN now() ELSE finished_at END
		 WHERE parent_job_id = $1 AND user_id = $2 AND kind = 'comments' AND status IN ('queued', 'running')`, jobID, userID)
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
				published_at, likes, replies, reposts, shares, images, videos, links, quoted_post_url, context, parent_id)
			 VALUES ($1, $2, $3, NULLIF($4, ''), $5, NULLIF($6, ''), $7, $8, $9, $10, $11, $12, $13, $14, $15, NULLIF($16, ''), NULLIF($17, ''), NULLIF($18, ''))
			 ON CONFLICT (id) DO UPDATE SET
				likes = EXCLUDED.likes, replies = EXCLUDED.replies, reposts = EXCLUDED.reposts,
				shares = EXCLUDED.shares, content = EXCLUDED.content,
				author_name = COALESCE(EXCLUDED.author_name, posts.author_name),
				author_verified = EXCLUDED.author_verified, images = EXCLUDED.images,
				videos = EXCLUDED.videos, links = EXCLUDED.links,
				context = COALESCE(EXCLUDED.context, posts.context),
				parent_id = COALESCE(posts.parent_id, EXCLUDED.parent_id), updated_at = now()`,
			p.ID, p.URL, p.AuthorUsername, p.AuthorName, p.AuthorVerified, p.AuthorURL, p.Content,
			p.PublishedAt, p.Likes, p.Replies, p.Reposts, p.Shares, images, videos, links, p.QuotedPostURL, p.Context, p.ParentID,
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

// MarkScraped finishes the scraping part of a project run: the file is built after the analysis.
func (s *Store) MarkScraped(ctx context.Context, jobID int64, total, newCount int) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs SET status = 'done', finished_at = now(), analysis_status = 'pending',
		        posts_count = $2, new_posts_count = $3, posts_seen = GREATEST(posts_seen, $2)
		 WHERE id = $1 AND status = 'running'`, jobID, total, newCount)
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
	// Deleted by the user while running and the worker never reported back: just drop them
	if _, err := s.pool.Exec(ctx,
		`DELETE FROM jobs WHERE status = 'running' AND deleted_at IS NOT NULL AND heartbeat_at < $1`, cutoff); err != nil {
		return 0, 0, err
	}
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

// DeleteJob removes one of the user's searches. A finished (or queued) job is deleted at once with
// its comments job and labels links; the returned keys are its S3 files to remove. A running job is
// hidden immediately and asked to stop; its row is removed when the worker reports back.
func (s *Store) DeleteJob(ctx context.Context, userID, jobID int64) (fileKeys []string, err error) {
	tag, err := s.pool.Exec(ctx,
		`UPDATE jobs SET deleted_at = now(), stop_requested = true
		 WHERE id = $1 AND user_id = $2 AND kind = 'search' AND status = 'running' AND deleted_at IS NULL`, jobID, userID)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() > 0 {
		// Its running comments job (if any) goes too
		_, err = s.pool.Exec(ctx,
			`UPDATE jobs SET deleted_at = now(), stop_requested = true WHERE parent_job_id = $1 AND status = 'running'`, jobID)
		return nil, err
	}
	rows, err := s.pool.Query(ctx,
		`DELETE FROM jobs WHERE id = $1 AND user_id = $2 AND kind = 'search' AND deleted_at IS NULL
		 RETURNING coalesce(file_key, ''), coalesce(removed_file_key, '')`, jobID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		found = true
		var a, b string
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		for _, k := range []string{a, b} {
			if k != "" {
				fileKeys = append(fileKeys, k)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !found {
		return nil, ErrNotFound
	}
	return fileKeys, nil
}

// DropIfDeleted removes a job the user deleted while it was running. Returns true if it was dropped
// (the worker's results must then be discarded).
func (s *Store) DropIfDeleted(ctx context.Context, jobID int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM jobs WHERE id = $1 AND deleted_at IS NOT NULL`, jobID)
	return tag.RowsAffected() > 0, err
}

// ProjectJobIDs lists the user's runs of a project (to delete them together with the project).
func (s *Store) ProjectJobIDs(ctx context.Context, userID, projectID int64) ([]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id FROM jobs WHERE user_id = $1 AND project_id = $2 AND kind = 'search' AND deleted_at IS NULL`, userID, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
