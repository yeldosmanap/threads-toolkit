package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/schedule"
)

type Schedule struct {
	ID        int64
	UserID    int64
	Keyword   string
	Filter    string
	MaxItems  int
	Spec      schedule.Spec
	Enabled   bool
	NextRunAt time.Time
	LastRunAt *time.Time
	LastError *string
	LastJobID *int64
	CreatedAt time.Time
	// Status of the last created job (joined), nil if none
	LastJobStatus *string
	// Project schedules run the whole project (all aliases + analysis); nil = keyword search
	ProjectID      *int64
	ProjectName    *string
	ProjectAliases []string
}

type ScheduleInput struct {
	// Set for project schedules (Keyword is then the project name, for display)
	ProjectID *int64
	Keyword   string
	Filter    string
	MaxItems  int
	Spec      schedule.Spec
	Enabled   bool
}

const scheduleColumns = `s.id, s.user_id, s.keyword, s.filter, s.max_items, s.kind,
	coalesce(s.every_hours, 0), coalesce(s.daily_time, ''), s.timezone, s.enabled,
	s.next_run_at, s.last_run_at, s.last_error, s.last_job_id, s.created_at,
	(SELECT j.status FROM jobs j WHERE j.id = s.last_job_id),
	s.project_id, (SELECT p.name FROM projects p WHERE p.id = s.project_id),
	(SELECT p.aliases FROM projects p WHERE p.id = s.project_id)`

func scanSchedule(row pgx.Row) (Schedule, error) {
	var s Schedule
	err := row.Scan(&s.ID, &s.UserID, &s.Keyword, &s.Filter, &s.MaxItems, &s.Spec.Kind,
		&s.Spec.EveryHours, &s.Spec.DailyTime, &s.Spec.Timezone, &s.Enabled,
		&s.NextRunAt, &s.LastRunAt, &s.LastError, &s.LastJobID, &s.CreatedAt, &s.LastJobStatus,
		&s.ProjectID, &s.ProjectName, &s.ProjectAliases)
	if errors.Is(err, pgx.ErrNoRows) {
		return s, ErrNotFound
	}
	return s, err
}

func nullIfZero(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

func nullIfEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}

func (s *Store) CountSchedules(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM schedules WHERE user_id = $1`, userID).Scan(&n)
	return n, err
}

func (s *Store) CreateSchedule(ctx context.Context, userID int64, in ScheduleInput, now time.Time) (Schedule, error) {
	sp := in.Spec
	return scanSchedule(s.pool.QueryRow(ctx,
		`INSERT INTO schedules AS s (user_id, keyword, keyword_norm, filter, max_items, kind, every_hours,
			daily_time, timezone, enabled, next_run_at, project_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING `+scheduleColumns,
		userID, in.Keyword, NormalizeKeyword(in.Keyword), in.Filter, in.MaxItems, sp.Kind,
		nullIfZero(sp.EveryHours), nullIfEmpty(sp.DailyTime), sp.Timezone, in.Enabled, sp.First(now), in.ProjectID))
}

func (s *Store) ListSchedules(ctx context.Context, userID int64) ([]Schedule, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+scheduleColumns+` FROM schedules s WHERE s.user_id = $1 ORDER BY s.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Schedule{}
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *Store) GetSchedule(ctx context.Context, userID, id int64) (Schedule, error) {
	return scanSchedule(s.pool.QueryRow(ctx,
		`SELECT `+scheduleColumns+` FROM schedules s WHERE s.id = $1 AND s.user_id = $2`, id, userID))
}

// UpdateSchedule replaces a schedule's settings. The next run is recomputed when the timing
// changed, or when a paused schedule is resumed after its planned slot has passed.
func (s *Store) UpdateSchedule(ctx context.Context, userID, id int64, in ScheduleInput, now time.Time) (Schedule, error) {
	cur, err := s.GetSchedule(ctx, userID, id)
	if err != nil {
		return cur, err
	}
	next := cur.NextRunAt
	switch {
	case cur.Spec != in.Spec:
		next = in.Spec.First(now)
	case in.Enabled && !cur.Enabled && !cur.NextRunAt.After(now):
		// Resumed after its slot passed: start fresh instead of firing for the missed slot.
		// (A quick pause/resume keeps the planned time, so it can't be used to run extra times.)
		next = in.Spec.First(now)
	}
	sp := in.Spec
	return scanSchedule(s.pool.QueryRow(ctx,
		`UPDATE schedules s SET keyword = $3, keyword_norm = $4, filter = $5, max_items = $6, kind = $7,
			every_hours = $8, daily_time = $9, timezone = $10, enabled = $11, next_run_at = $12,
			last_error = CASE WHEN $11 AND NOT s.enabled THEN NULL ELSE s.last_error END,
			project_id = $13
		 WHERE s.id = $1 AND s.user_id = $2
		 RETURNING `+scheduleColumns,
		id, userID, in.Keyword, NormalizeKeyword(in.Keyword), in.Filter, in.MaxItems, sp.Kind,
		nullIfZero(sp.EveryHours), nullIfEmpty(sp.DailyTime), sp.Timezone, in.Enabled, next, in.ProjectID))
}

func (s *Store) DeleteSchedule(ctx context.Context, userID, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM schedules WHERE id = $1 AND user_id = $2`, id, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

type ScheduleLimits struct {
	ActiveJobs int // per-user queued+running limit
	DailyJobs  int // per-user jobs per 24h limit
}

type ScheduleRunResult struct {
	Started int
	Skipped int
}

// RunDueSchedules turns every due, enabled schedule into a job, applying the same per-user
// quotas as manual searches. A skipped occurrence is recorded in last_error and the schedule
// simply moves on to its next slot (no catch-up). Safe to run from several backends at once.
func (s *Store) RunDueSchedules(ctx context.Context, now time.Time, limits ScheduleLimits) (ScheduleRunResult, error) {
	var res ScheduleRunResult
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	rows, err := tx.Query(ctx,
		`SELECT `+scheduleColumns+` FROM schedules s
		 WHERE s.enabled AND s.next_run_at <= $1
		 ORDER BY s.next_run_at
		 LIMIT 100
		 FOR UPDATE SKIP LOCKED`, now)
	if err != nil {
		return res, err
	}
	var due []Schedule
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			rows.Close()
			return res, err
		}
		due = append(due, sc)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return res, err
	}

	for _, sc := range due {
		var skip string
		var previousActive bool
		var active, daily int
		err := tx.QueryRow(ctx,
			`SELECT
				EXISTS (SELECT 1 FROM jobs WHERE schedule_id = $1 AND status IN ('queued', 'running')),
				(SELECT count(*) FROM jobs WHERE user_id = $2 AND status IN ('queued', 'running')),
				(SELECT count(*) FROM jobs WHERE user_id = $2 AND created_at > $3)`,
			sc.ID, sc.UserID, now.Add(-24*time.Hour)).Scan(&previousActive, &active, &daily)
		if err != nil {
			return res, err
		}
		switch {
		case previousActive:
			skip = "Пропущено: предыдущий запуск ещё не завершён"
		case active >= limits.ActiveJobs:
			skip = "Пропущено: слишком много поисков в очереди"
		case daily >= limits.DailyJobs:
			skip = "Пропущено: достигнут суточный лимит поисков"
		}

		var jobID *int64
		if skip == "" {
			var id int64
			var err error
			if sc.ProjectID != nil {
				// Whole project: every alias is searched, then the analyzer takes over
				err = tx.QueryRow(ctx,
					`INSERT INTO jobs (user_id, keyword, keyword_norm, filter, max_items, schedule_id, project_id, keywords)
					 SELECT $1, p.name, $2, $3, $4, $5, p.id, p.aliases FROM projects p WHERE p.id = $6
					 RETURNING id`,
					sc.UserID, ProjectKeywordNorm(*sc.ProjectID), sc.Filter, sc.MaxItems, sc.ID, *sc.ProjectID).Scan(&id)
			} else {
				err = tx.QueryRow(ctx,
					`INSERT INTO jobs (user_id, keyword, keyword_norm, filter, max_items, schedule_id)
					 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
					sc.UserID, sc.Keyword, NormalizeKeyword(sc.Keyword), sc.Filter, sc.MaxItems, sc.ID).Scan(&id)
			}
			if err != nil {
				return res, err
			}
			jobID = &id
			res.Started++
		} else {
			res.Skipped++
		}

		if _, err := tx.Exec(ctx,
			`UPDATE schedules SET next_run_at = $2, last_run_at = $3, last_error = $4,
				last_job_id = COALESCE($5, last_job_id)
			 WHERE id = $1`,
			sc.ID, sc.Spec.Next(now), now, nullIfEmpty(skip), jobID); err != nil {
			return res, err
		}
	}
	return res, tx.Commit(ctx)
}
