package store_test

// Integration tests against a real Postgres. Skipped unless TEST_DATABASE_URL is set, e.g.:
//   TEST_DATABASE_URL=postgres://threads:threads@127.0.0.1:55432/threads_test?sslmode=disable go test ./...
// The database is wiped on every run.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/db"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/schedule"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/store"
)

func setup(t *testing.T) (*store.Store, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(pool); err != nil {
		t.Fatal(err)
	}
	return store.New(pool), pool
}

func mustUser(t *testing.T, s *store.Store, email string) store.User {
	t.Helper()
	u, err := s.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func post(id string) store.Post {
	now := time.Now().UTC().Truncate(time.Second)
	return store.Post{ID: id, URL: "https://www.threads.com/@a/post/" + id, AuthorUsername: "a", Content: "text " + id, PublishedAt: &now}
}

func TestUsersAndSessions(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()

	u := mustUser(t, s, "User@Example.com")
	if _, err := s.CreateUser(ctx, "user@example.com", "x"); !errors.Is(err, store.ErrEmailTaken) {
		t.Fatalf("duplicate email (case-insensitive) should fail, got %v", err)
	}
	got, hash, err := s.GetUserByEmail(ctx, "USER@example.com")
	if err != nil || got.ID != u.ID || hash != "hash" {
		t.Fatalf("lookup: %+v %q %v", got, hash, err)
	}

	token := []byte("token-hash")
	if err := s.CreateSession(ctx, token, u.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if su, err := s.GetSessionUser(ctx, token); err != nil || su.ID != u.ID {
		t.Fatalf("session user: %+v %v", su, err)
	}
	expired := []byte("expired")
	_ = s.CreateSession(ctx, expired, u.ID, time.Now().Add(-time.Minute))
	if _, err := s.GetSessionUser(ctx, expired); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired session must not authenticate, got %v", err)
	}
}

func TestQueueFairnessAndLifecycle(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	alice := mustUser(t, s, "alice@example.com")
	bob := mustUser(t, s, "bob@example.com")

	// Alice queues 2 jobs before Bob queues 1
	a1, _ := s.CreateJob(ctx, alice.ID, "билайн", "recent", 10)
	a2, _ := s.CreateJob(ctx, alice.ID, "beeline", "recent", 10)
	b1, _ := s.CreateJob(ctx, bob.ID, "kcell", "top", 10)

	if n, _ := s.CountActiveJobs(ctx, alice.ID); n != 2 {
		t.Fatalf("alice active = %d", n)
	}

	first, err := s.ClaimJob(ctx, "w1")
	if err != nil || first.ID != a1.ID || first.Status != store.JobRunning || first.Attempts != 1 {
		t.Fatalf("first claim: %+v %v", first, err)
	}
	// Alice was just served, so Bob goes next even though a2 is older
	second, err := s.ClaimJob(ctx, "w2")
	if err != nil || second.ID != b1.ID {
		t.Fatalf("second claim should be bob's job %d, got %+v %v", b1.ID, second, err)
	}

	// Canceling: only queued jobs
	if _, err := s.CancelJob(ctx, alice.ID, a1.ID); !errors.Is(err, store.ErrJobState) {
		t.Fatalf("running job cancel: want ErrJobState, got %v", err)
	}
	if _, err := s.CancelJob(ctx, bob.ID, a2.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign job cancel: want ErrNotFound, got %v", err)
	}
	if j, err := s.CancelJob(ctx, alice.ID, a2.ID); err != nil || j.Status != store.JobCanceled {
		t.Fatalf("cancel queued: %+v %v", j, err)
	}
	if _, err := s.ClaimJob(ctx, "w1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("queue should be empty, got %v", err)
	}

	// Progress + results
	if err := s.Heartbeat(ctx, a1.ID, 5); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveResults(ctx, a1.ID, []store.Post{post("p1"), post("p2")})
	if err != nil || len(saved) != 2 || !saved[0].IsNew || !saved[1].IsNew {
		t.Fatalf("first run: %+v %v", saved, err)
	}
	if err := s.MarkDone(ctx, a1.ID, "key.xlsx", 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.Heartbeat(ctx, a1.ID, 6); !errors.Is(err, store.ErrJobState) {
		t.Fatalf("heartbeat on done job: want ErrJobState, got %v", err)
	}

	// Second run of the same search (keyword differs only in case/spaces): p1, p2 are old, p3 is new
	a3, _ := s.CreateJob(ctx, alice.ID, "  БИЛАЙН ", "recent", 10)
	if c, err := s.ClaimJob(ctx, "w1"); err != nil || c.ID != a3.ID {
		t.Fatalf("claim a3: %+v %v", c, err)
	}
	saved, err = s.SaveResults(ctx, a3.ID, []store.Post{post("p2"), post("p3"), post("p1")})
	if err != nil {
		t.Fatal(err)
	}
	newByID := map[string]bool{}
	for _, p := range saved {
		newByID[p.ID] = p.IsNew
	}
	if newByID["p1"] || newByID["p2"] || !newByID["p3"] {
		t.Fatalf("new flags = %v", newByID)
	}

	// The same posts are "new" for a different user's search
	b2, _ := s.CreateJob(ctx, bob.ID, "билайн", "recent", 10)
	_ = s.MarkFailed(ctx, b1.ID, "boom")
	if c, err := s.ClaimJob(ctx, "w1"); err != nil || c.ID != b2.ID {
		t.Fatalf("claim b2: %+v %v", c, err)
	}
	saved, _ = s.SaveResults(ctx, b2.ID, []store.Post{post("p1")})
	if !saved[0].IsNew {
		t.Fatal("post must be new for another user's search")
	}

	jobs, err := s.ListJobs(ctx, alice.ID, 10)
	if err != nil || len(jobs) != 3 {
		t.Fatalf("list: %d %v", len(jobs), err)
	}
}

func TestRecoverStaleJobs(t *testing.T) {
	s, pool := setup(t)
	ctx := context.Background()
	u := mustUser(t, s, "u@example.com")
	j, _ := s.CreateJob(ctx, u.ID, "x", "recent", 10)

	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := s.ClaimJob(ctx, "w"); err != nil {
			t.Fatal(err)
		}
		// Simulate a worker that died long ago
		if _, err := pool.Exec(ctx, `UPDATE jobs SET heartbeat_at = now() - interval '1 hour' WHERE id = $1`, j.ID); err != nil {
			t.Fatal(err)
		}
		requeued, failed, err := s.RecoverStaleJobs(ctx, 3*time.Minute, 2)
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 1 && (requeued != 1 || failed != 0) {
			t.Fatalf("attempt 1: requeued=%d failed=%d", requeued, failed)
		}
		if attempt == 2 && (requeued != 0 || failed != 1) {
			t.Fatalf("attempt 2: requeued=%d failed=%d", requeued, failed)
		}
	}
	got, _ := s.GetJob(ctx, u.ID, j.ID)
	if got.Status != store.JobFailed || got.Error == nil {
		t.Fatalf("job after max attempts: %+v", got)
	}
}

func TestListJobPosts(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	u := mustUser(t, s, "posts@example.com")

	mk := func(id, author, content string, likes int, hoursAgo int) store.Post {
		ts := time.Now().UTC().Add(-time.Duration(hoursAgo) * time.Hour).Truncate(time.Second)
		return store.Post{ID: id, URL: "https://www.threads.com/@" + author + "/post/" + id, AuthorUsername: author,
			AuthorName: "Name " + author, Content: content, PublishedAt: &ts, Likes: likes, Images: []string{"https://img/1"}}
	}

	// First run finds p1; second run finds p1 (old) + p2, p3, p4 (new)
	j1, _ := s.CreateJob(ctx, u.ID, "билайн", "recent", 10)
	_, _ = s.ClaimJob(ctx, "w")
	if _, err := s.SaveResults(ctx, j1.ID, []store.Post{mk("p1", "alice", "Билайн тормозит", 1, 5)}); err != nil {
		t.Fatal(err)
	}
	_ = s.MarkDone(ctx, j1.ID, "k", 1, 1)
	j2, _ := s.CreateJob(ctx, u.ID, "билайн", "recent", 10)
	_, _ = s.ClaimJob(ctx, "w")
	if _, err := s.SaveResults(ctx, j2.ID, []store.Post{
		mk("p1", "alice", "Билайн тормозит", 1, 5),
		mk("p2", "bob", "Скидка 50% на тариф", 30, 1),
		mk("p3", "carol_kz", "интернет_ок", 7, 3),
		mk("p4", "dave", "просто пост", 12, 2),
	}); err != nil {
		t.Fatal(err)
	}

	ids := func(ps []store.JobPost) []string {
		out := []string{}
		for _, p := range ps {
			out = append(out, p.ID)
		}
		return out
	}
	check := func(name string, q store.PostQuery, wantIDs []string, wantTotal int) {
		t.Helper()
		if q.Limit == 0 {
			q.Limit = 50
		}
		got, total, err := s.ListJobPosts(ctx, j2.ID, q)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if total != wantTotal || strings.Join(ids(got), ",") != strings.Join(wantIDs, ",") {
			t.Errorf("%s: got %v (total %d), want %v (total %d)", name, ids(got), total, wantIDs, wantTotal)
		}
	}

	check("newest", store.PostQuery{}, []string{"p2", "p4", "p3", "p1"}, 4)
	check("oldest", store.PostQuery{Sort: "oldest"}, []string{"p1", "p3", "p4", "p2"}, 4)
	check("likes", store.PostQuery{Sort: "likes"}, []string{"p2", "p4", "p3", "p1"}, 4)
	check("only new", store.PostQuery{OnlyNew: true}, []string{"p2", "p4", "p3"}, 3)
	check("search content, case-insensitive cyrillic", store.PostQuery{Search: "БИЛАЙН"}, []string{"p1"}, 1)
	check("search author", store.PostQuery{Search: "Name bob"}, []string{"p2"}, 1)
	// LIKE wildcards in user input are literal
	check("literal percent", store.PostQuery{Search: "50%"}, []string{"p2"}, 1)
	check("literal underscore", store.PostQuery{Search: "_"}, []string{"p3"}, 1)
	check("page 2", store.PostQuery{Limit: 3, Offset: 3}, []string{"p1"}, 4)
	check("unknown sort falls back to newest", store.PostQuery{Sort: "x; DROP TABLE posts"}, []string{"p2", "p4", "p3", "p1"}, 4)

	if g1, _ := s.GetJob(ctx, u.ID, j1.ID); !g1.IsFirstRun {
		t.Error("first run of a search must be flagged IsFirstRun")
	}
	if g2, _ := s.GetJob(ctx, u.ID, j2.ID); g2.IsFirstRun {
		t.Error("second run must not be flagged IsFirstRun")
	}

	got, _, _ := s.ListJobPosts(ctx, j2.ID, store.PostQuery{Limit: 1})
	if len(got) != 1 || len(got[0].Images) != 1 || got[0].AuthorName != "Name bob" || !got[0].IsNew {
		t.Errorf("post fields not loaded: %+v", got)
	}
}

func TestSchedules(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	u := mustUser(t, s, "sched@example.com")
	limits := store.ScheduleLimits{ActiveJobs: 3, DailyJobs: 20}
	t0 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

	every6 := store.ScheduleInput{Keyword: "билайн", Filter: "recent", MaxItems: 50, Enabled: true,
		Spec: schedule.Spec{Kind: schedule.KindEveryHours, EveryHours: 6, Timezone: "UTC"}}
	sc, err := s.CreateSchedule(ctx, u.ID, every6, t0)
	if err != nil {
		t.Fatal(err)
	}
	if !sc.NextRunAt.Equal(t0) {
		t.Fatalf("interval schedule must be due immediately, next=%v", sc.NextRunAt)
	}
	daily, _ := s.CreateSchedule(ctx, u.ID, store.ScheduleInput{Keyword: "kcell", Filter: "top", MaxItems: 20, Enabled: true,
		Spec: schedule.Spec{Kind: schedule.KindDaily, DailyTime: "09:00", Timezone: "Asia/Almaty"}}, t0)
	if n, _ := s.CountSchedules(ctx, u.ID); n != 2 {
		t.Fatalf("count = %d", n)
	}

	// Run at t0: only the interval schedule is due (daily is at 09:00 Almaty = 04:00 UTC tomorrow)
	res, err := s.RunDueSchedules(ctx, t0, limits)
	if err != nil || res.Started != 1 || res.Skipped != 0 {
		t.Fatalf("first run: %+v %v", res, err)
	}
	sc, _ = s.GetSchedule(ctx, u.ID, sc.ID)
	if sc.LastJobID == nil || sc.LastJobStatus == nil || *sc.LastJobStatus != store.JobQueued || !sc.NextRunAt.Equal(t0.Add(6*time.Hour)) {
		t.Fatalf("after run: %+v", sc)
	}
	job, _ := s.GetJob(ctx, u.ID, *sc.LastJobID)
	if job.ScheduleID == nil || *job.ScheduleID != sc.ID || job.Keyword != "билайн" || job.MaxItems != 50 {
		t.Fatalf("created job: %+v", job)
	}
	daily, _ = s.GetSchedule(ctx, u.ID, daily.ID)
	if want := time.Date(2026, 11, 18, 4, 0, 0, 0, time.UTC); !daily.NextRunAt.Equal(want) || daily.LastRunAt != nil {
		t.Fatalf("daily must stay untouched until 04:00 UTC: %+v", daily)
	}

	// 6h later the previous job is still queued -> skipped, not piled up
	t1 := t0.Add(6 * time.Hour)
	res, _ = s.RunDueSchedules(ctx, t1, limits)
	sc, _ = s.GetSchedule(ctx, u.ID, sc.ID)
	if res.Started != 0 || res.Skipped != 1 || sc.LastError == nil || !sc.NextRunAt.Equal(t1.Add(6*time.Hour)) {
		t.Fatalf("overlap skip: %+v %+v", res, sc)
	}

	// Previous job finishes; quota of active jobs is full with manual searches -> skipped
	_, _ = s.ClaimJob(ctx, "w")
	_ = s.MarkFailed(ctx, job.ID, "x")
	for i := 0; i < 3; i++ {
		_, _ = s.CreateJob(ctx, u.ID, "manual", "recent", 10)
	}
	t2 := t1.Add(6 * time.Hour)
	res, _ = s.RunDueSchedules(ctx, t2, limits)
	sc, _ = s.GetSchedule(ctx, u.ID, sc.ID)
	if res.Skipped != 1 || sc.LastError == nil || !strings.Contains(*sc.LastError, "очереди") {
		t.Fatalf("quota skip: %+v %v", res, sc.LastError)
	}

	// Disable: not due anymore. Re-enable later: next run recomputed from "now", error cleared
	off := every6
	off.Enabled = false
	if _, err := s.UpdateSchedule(ctx, u.ID, sc.ID, off, t2); err != nil {
		t.Fatal(err)
	}
	if res, _ := s.RunDueSchedules(ctx, t2.Add(48*time.Hour), store.ScheduleLimits{ActiveJobs: 99, DailyJobs: 99}); res.Started+res.Skipped != 1 {
		// only the daily schedule may run
		t.Fatalf("disabled schedule ran: %+v", res)
	}
	t3 := t2.Add(72 * time.Hour)
	sc, err = s.UpdateSchedule(ctx, u.ID, sc.ID, every6, t3)
	if err != nil || !sc.Enabled || !sc.NextRunAt.Equal(t3) || sc.LastError != nil {
		t.Fatalf("re-enable: %+v %v", sc, err)
	}

	// Quick pause/resume keeps the planned slot (no extra run)
	_, _ = s.RunDueSchedules(ctx, t3, store.ScheduleLimits{ActiveJobs: 99, DailyJobs: 99})
	sc, _ = s.GetSchedule(ctx, u.ID, sc.ID)
	planned := sc.NextRunAt
	_, _ = s.UpdateSchedule(ctx, u.ID, sc.ID, off, t3.Add(time.Minute))
	sc, _ = s.UpdateSchedule(ctx, u.ID, sc.ID, every6, t3.Add(2*time.Minute))
	if !sc.NextRunAt.Equal(planned) {
		t.Fatalf("pause/resume moved the slot: %v -> %v", planned, sc.NextRunAt)
	}

	// Another user cannot touch it; deleting keeps the jobs (schedule_id -> NULL)
	other := mustUser(t, s, "other-sched@example.com")
	if _, err := s.GetSchedule(ctx, other.ID, sc.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign get: %v", err)
	}
	if err := s.DeleteSchedule(ctx, other.ID, sc.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}
	if err := s.DeleteSchedule(ctx, u.ID, sc.ID); err != nil {
		t.Fatal(err)
	}
	if j, err := s.GetJob(ctx, u.ID, job.ID); err != nil || j.ScheduleID != nil {
		t.Fatalf("job after schedule delete: %+v %v", j, err)
	}
}
