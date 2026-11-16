package store_test

// Integration tests against a real Postgres. Skipped unless TEST_DATABASE_URL is set, e.g.:
//   TEST_DATABASE_URL=postgres://threads:threads@127.0.0.1:55432/threads_test?sslmode=disable go test ./...
// The database is wiped on every run.

import (
	"context"
	"errors"
	"os"
	"sort"
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
	if _, err := s.Heartbeat(ctx, a1.ID, 5, nil, nil); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveResults(ctx, a1.ID, []store.Post{post("p1"), post("p2")})
	if err != nil || len(saved) != 2 || !saved[0].IsNew || !saved[1].IsNew {
		t.Fatalf("first run: %+v %v", saved, err)
	}
	if err := s.MarkDone(ctx, a1.ID, "key.xlsx", 2, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Heartbeat(ctx, a1.ID, 6, nil, nil); !errors.Is(err, store.ErrJobState) {
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

func TestProjectAnalysisFlow(t *testing.T) {
	s, pool := setup(t)
	ctx := context.Background()
	u := mustUser(t, s, "proj@example.com")

	p, err := s.CreateProject(ctx, u.ID, store.ProjectInput{Name: "Beeline KZ", Market: "Казахстан",
		Aliases: []string{"билайн", "@beeline_kz"}, OfficialAccounts: []string{"beeline_kz"}})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountProjects(ctx, u.ID); n != 1 {
		t.Fatalf("projects = %d", n)
	}

	job, err := s.CreateProjectJob(ctx, p, "recent", 50, nil)
	if err != nil || job.ProjectID == nil || strings.Join(job.Keywords, ",") != "билайн,@beeline_kz" {
		t.Fatalf("project job: %+v %v", job, err)
	}
	_, _ = s.ClaimJob(ctx, "w")
	withCtx := post("c1")
	withCtx.Context = "1/2 интернет не работает"
	saved, err := s.SaveResults(ctx, job.ID, []store.Post{post("a1"), post("a2"), withCtx})
	if err != nil || len(saved) != 3 {
		t.Fatalf("save: %v", err)
	}
	// Project progress: alias 2 of 2, value may go down (dedup); plain heartbeat keeps the max
	one, two, ten := 1, 2, 10
	_, _ = s.Heartbeat(ctx, job.ID, 361, &one, &ten)
	_, _ = s.Heartbeat(ctx, job.ID, 313, &two, &ten)
	if g, _ := s.GetJobByID(ctx, job.ID); g.PostsSeen != 313 || g.ProgressKeywordIndex == nil || *g.ProgressKeywordIndex != 2 {
		t.Fatalf("project progress: seen %d idx %v", g.PostsSeen, g.ProgressKeywordIndex)
	}
	_, _ = s.Heartbeat(ctx, job.ID, 100, nil, nil)
	if g, _ := s.GetJobByID(ctx, job.ID); g.PostsSeen != 313 {
		t.Fatalf("plain heartbeat must not lower progress: %d", g.PostsSeen)
	}
	if err := s.MarkScraped(ctx, job.ID, 3, 3); err != nil {
		t.Fatal(err)
	}

	// Analyzer picks it up
	claimed, err := s.ClaimAnalysis(ctx)
	if err != nil || claimed.ID != job.ID || *claimed.AnalysisStatus != store.AnalysisRunning {
		t.Fatalf("claim analysis: %+v %v", claimed, err)
	}
	if _, err := s.ClaimAnalysis(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second claim must find nothing, got %v", err)
	}
	todo, _ := s.PostsToLabel(ctx, job.ID, p.ID, "v1")
	if len(todo) != 3 {
		t.Fatalf("to label: %d", len(todo))
	}
	for _, tp := range todo {
		if tp.ID == "c1" && tp.Context != "1/2 интернет не работает" {
			t.Errorf("context not stored: %q", tp.Context)
		}
	}
	_ = s.SaveLabel(ctx, p.ID, "a1", store.PostLabel{Category: "review", Market: "target", Sentiment: "negative", Aspects: []string{"mobile_internet"}, Language: "ru", Reason: "r"}, "m", "v1")
	_ = s.SaveLabel(ctx, p.ID, "a2", store.PostLabel{Category: "review", Market: "target", Sentiment: "positive", Language: "kk", Reason: "r"}, "m", "v1")
	_ = s.SaveLabel(ctx, p.ID, "c1", store.PostLabel{Category: "ad", Market: "target", Sentiment: "neutral", Language: "ru", Reason: "r", Excluded: true}, "m", "v1")
	if todo, _ := s.PostsToLabel(ctx, job.ID, p.ID, "v1"); len(todo) != 0 {
		t.Fatalf("all labeled, still %d to do", len(todo))
	}
	if todo, _ := s.PostsToLabel(ctx, job.ID, p.ID, "v2"); len(todo) != 3 {
		t.Fatalf("new prompt version must relabel, got %d", len(todo))
	}

	if err := s.FinishAnalysis(ctx, job.ID, "k.xlsx", "r.xlsx"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetJob(ctx, u.ID, job.ID)
	if got.AnalyzedCount != 3 || *got.AnalysisStatus != store.AnalysisDone || got.RemovedFileKey == nil {
		t.Fatalf("job after analysis: %+v", got)
	}

	sum, err := s.JobSummary(ctx, job.ID)
	if err != nil || sum.Total != 3 || sum.Kept != 2 || sum.Sentiment["negative"] != 1 || sum.Sentiment["positive"] != 1 ||
		sum.Excluded["ad"] != 1 || sum.Aspects["mobile_internet"]["negative"] != 1 {
		t.Fatalf("summary: %+v %v", sum, err)
	}

	list := func(q store.PostQuery) []string {
		q.Limit = 50
		ps, _, err := s.ListJobPosts(ctx, job.ID, q)
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, p := range ps {
			ids = append(ids, p.ID)
		}
		sort.Strings(ids)
		return ids
	}
	if ids := list(store.PostQuery{View: "kept"}); strings.Join(ids, ",") != "a1,a2" {
		t.Errorf("kept: %v", ids)
	}
	if ids := list(store.PostQuery{View: "removed"}); strings.Join(ids, ",") != "c1" {
		t.Errorf("removed: %v", ids)
	}
	if ids := list(store.PostQuery{View: "kept", Sentiment: "negative"}); strings.Join(ids, ",") != "a1" {
		t.Errorf("negative: %v", ids)
	}
	if ids := list(store.PostQuery{View: "all", Aspect: "mobile_internet"}); strings.Join(ids, ",") != "a1" {
		t.Errorf("aspect: %v", ids)
	}

	// The next run reuses existing labels: only the new post needs the classifier
	job2, _ := s.CreateProjectJob(ctx, p, "recent", 50, nil)
	_, _ = s.ClaimJob(ctx, "w")
	_, _ = s.SaveResults(ctx, job2.ID, []store.Post{post("a1"), post("n1")})
	if todo, _ := s.PostsToLabel(ctx, job2.ID, p.ID, "v1"); len(todo) != 1 || todo[0].ID != "n1" {
		t.Fatalf("second run to label: %+v", todo)
	}

	// Comments: a1 is negative but has no replies on Threads -> give it some, then queue collection
	if _, err := pool.Exec(ctx, `UPDATE posts SET replies = 5 WHERE id = 'a1'`); err != nil {
		t.Fatal(err)
	}
	urls, err := s.PostsForComments(ctx, job.ID, p.ID, 10)
	if err != nil || len(urls) != 1 || !strings.HasSuffix(urls[0], "/a1") {
		t.Fatalf("posts for comments: %v %v", urls, err)
	}
	full, _ := s.GetJobByID(ctx, job.ID)
	cj, err := s.CreateCommentsJob(ctx, full, urls, 30)
	if err != nil || cj.Kind != store.JobKindComments || cj.ParentJobID == nil || len(cj.PostURLs) != 1 {
		t.Fatalf("comments job: %+v %v", cj, err)
	}
	if n, _ := s.CountActiveJobs(ctx, u.ID); n != 1 { // only job2 (search) counts
		t.Fatalf("system comments job must not count against quotas, active = %d", n)
	}
	if g, _ := s.GetJob(ctx, u.ID, job.ID); g.CommentsState != "queued" {
		t.Fatalf("comments state: %q", g.CommentsState)
	}
	if _, err := s.GetJob(ctx, u.ID, cj.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("comments job must not be visible as a user job: %v", err)
	}
	// job2 is still queued ahead of the comments job (fair order: user served least recently)
	for {
		c, err := s.ClaimJob(ctx, "w")
		if err != nil {
			t.Fatal(err)
		}
		if c.ID == cj.ID {
			break
		}
		_ = s.MarkFailed(ctx, c.ID, "skip")
	}
	cm1, cm2 := post("cm1"), post("cm2")
	cm1.ParentID, cm2.ParentID = "a1", "a1"
	if _, err := s.SaveResults(ctx, cj.ID, []store.Post{cm1, cm2}); err != nil {
		t.Fatal(err)
	}
	_ = s.SaveLabel(ctx, p.ID, "cm1", store.PostLabel{Category: "review", Market: "target", Sentiment: "negative", Language: "ru", Reason: "r"}, "m", "v1")
	_ = s.SaveLabel(ctx, p.ID, "cm2", store.PostLabel{Category: "review", Market: "target", Sentiment: "positive", Language: "ru", Reason: "r"}, "m", "v1")
	comments, err := s.ListComments(ctx, &p.ID, "a1", 50)
	if err != nil || len(comments) != 2 || comments[0].Label == nil {
		t.Fatalf("list comments: %+v %v", comments, err)
	}
	ps, _, _ := s.ListJobPosts(ctx, job.ID, store.PostQuery{View: "all", PostID: "a1", Limit: 5})
	if len(ps) != 1 || ps[0].Comments["total"] != 2 || ps[0].Comments["negative"] != 1 || ps[0].Comments["positive"] != 1 {
		t.Fatalf("comment counts: %+v", ps)
	}
	if sum, _ := s.JobSummary(ctx, job.ID); sum.Comments["negative"] != 1 || sum.Comments["positive"] != 1 {
		t.Fatalf("summary comments: %+v", sum.Comments)
	}
	jc, err := s.JobComments(ctx, job.ID)
	if err != nil || len(jc) != 2 || jc[0].Parent == nil || !strings.HasSuffix(jc[0].Parent.URL, "/a1") || jc[0].Label == nil {
		t.Fatalf("job comments: %+v %v", jc, err)
	}
	if err := s.SetJobFiles(ctx, job.ID, "new.xlsx", ""); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.GetJob(ctx, u.ID, job.ID); *g.FileKey != "new.xlsx" || g.RemovedFileKey != nil {
		t.Fatalf("files not replaced: %+v", g)
	}
	if urls, _ := s.PostsForComments(ctx, job.ID, p.ID, 10); len(urls) != 0 {
		t.Fatalf("comments already collected, still queued: %v", urls)
	}

	// Deleting the project keeps the runs as history
	if err := s.DeleteProject(ctx, u.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if j, err := s.GetJob(ctx, u.ID, job.ID); err != nil || j.ProjectID != nil {
		t.Fatalf("job after project delete: %+v %v", j, err)
	}
}

func TestProjectSchedule(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	u := mustUser(t, s, "psched@example.com")
	p, _ := s.CreateProject(ctx, u.ID, store.ProjectInput{Name: "Beeline KZ", Market: "Казахстан", Aliases: []string{"билайн", "@beeline_kz"}})
	t0 := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

	sc, err := s.CreateSchedule(ctx, u.ID, store.ScheduleInput{ProjectID: &p.ID, Keyword: p.Name, Filter: "recent", MaxItems: 30, Enabled: true,
		Spec: schedule.Spec{Kind: schedule.KindEveryHours, EveryHours: 24, Timezone: "UTC"}}, t0)
	if err != nil || sc.ProjectID == nil || *sc.ProjectName != "Beeline KZ" || len(sc.ProjectAliases) != 2 {
		t.Fatalf("project schedule: %+v %v", sc, err)
	}
	// Aliases edited after the schedule was created are picked up at run time
	_, _ = s.UpdateProject(ctx, u.ID, p.ID, store.ProjectInput{Name: "Beeline KZ", Market: "Казахстан", Aliases: []string{"билайн", "@beeline_kz", "beeline"}})

	res, err := s.RunDueSchedules(ctx, t0, store.ScheduleLimits{ActiveJobs: 3, DailyJobs: 20})
	if err != nil || res.Started != 1 {
		t.Fatalf("run: %+v %v", res, err)
	}
	sc, _ = s.GetSchedule(ctx, u.ID, sc.ID)
	job, _ := s.GetJob(ctx, u.ID, *sc.LastJobID)
	if job.ProjectID == nil || *job.ProjectID != p.ID || len(job.Keywords) != 3 || job.ScheduleID == nil || job.Keyword != "Beeline KZ" {
		t.Fatalf("scheduled project job: %+v", job)
	}
	// A manual run and a scheduled run of the same project share new-post tracking
	manual, _ := s.CreateProjectJob(ctx, p, "recent", 30, nil)
	var a, b string
	_ = s.Pool().QueryRow(ctx, `SELECT keyword_norm FROM jobs WHERE id = $1`, job.ID).Scan(&a)
	_ = s.Pool().QueryRow(ctx, `SELECT keyword_norm FROM jobs WHERE id = $1`, manual.ID).Scan(&b)
	if a != b {
		t.Fatalf("keyword_norm differs: %q vs %q", a, b)
	}

	// Deleting the project deletes its schedules
	_ = s.DeleteProject(ctx, u.ID, p.ID)
	if _, err := s.GetSchedule(ctx, u.ID, sc.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("schedule must be deleted with its project: %v", err)
	}
}

func TestStopJob(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	u := mustUser(t, s, "stop@example.com")
	other := mustUser(t, s, "stop-other@example.com")

	j, _ := s.CreateJob(ctx, u.ID, "билайн", "recent", 300)
	if err := s.RequestStop(ctx, u.ID, j.ID); !errors.Is(err, store.ErrJobState) {
		t.Fatalf("queued job: want ErrJobState (use cancel), got %v", err)
	}
	_, _ = s.ClaimJob(ctx, "w")
	if stop, err := s.Heartbeat(ctx, j.ID, 10, nil, nil); err != nil || stop {
		t.Fatalf("no stop yet: %v %v", stop, err)
	}
	if err := s.RequestStop(ctx, other.ID, j.ID); !errors.Is(err, store.ErrJobState) {
		t.Fatalf("another user must not stop the job: %v", err)
	}
	if err := s.RequestStop(ctx, u.ID, j.ID); err != nil {
		t.Fatal(err)
	}
	if stop, _ := s.Heartbeat(ctx, j.ID, 20, nil, nil); !stop {
		t.Fatal("worker must see the stop request")
	}
	// The worker sends what it has: the job completes normally
	if _, err := s.SaveResults(ctx, j.ID, []store.Post{post("s1")}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDone(ctx, j.ID, "k", 1, 1); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.GetJob(ctx, u.ID, j.ID); g.Status != store.JobDone || !g.StopRequested {
		t.Fatalf("stopped job: %+v", g)
	}

	// After scraping, stop applies to the comments collection
	full, _ := s.GetJobByID(ctx, j.ID)
	cj, _ := s.CreateCommentsJob(ctx, full, []string{"https://www.threads.com/@a/post/s1"}, 30)
	if err := s.RequestStop(ctx, u.ID, j.ID); err != nil {
		t.Fatalf("stop queued comments: %v", err)
	}
	if c, _ := s.GetJobByID(ctx, cj.ID); c.Status != store.JobCanceled {
		t.Fatalf("queued comments job must be canceled: %s", c.Status)
	}
	if g, _ := s.GetJob(ctx, u.ID, j.ID); g.CommentsState != "canceled" {
		t.Fatalf("comments state: %q", g.CommentsState)
	}
	if err := s.RequestStop(ctx, u.ID, j.ID); !errors.Is(err, store.ErrJobState) {
		t.Fatalf("nothing left to stop: %v", err)
	}
}

func TestDeleteJob(t *testing.T) {
	s, _ := setup(t)
	ctx := context.Background()
	u := mustUser(t, s, "del@example.com")
	other := mustUser(t, s, "del-other@example.com")

	// Finished job: deleted at once, file keys returned, comments job cascades
	done, _ := s.CreateJob(ctx, u.ID, "done", "recent", 10)
	_, _ = s.ClaimJob(ctx, "w")
	_, _ = s.SaveResults(ctx, done.ID, []store.Post{post("d1")})
	_ = s.MarkDone(ctx, done.ID, "users/1/jobs/1/posts.xlsx", 1, 1)
	full, _ := s.GetJobByID(ctx, done.ID)
	cj, _ := s.CreateCommentsJob(ctx, full, []string{"https://www.threads.com/@a/post/d1"}, 10)
	if _, err := s.DeleteJob(ctx, other.ID, done.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("foreign delete: %v", err)
	}
	keys, err := s.DeleteJob(ctx, u.ID, done.ID)
	if err != nil || len(keys) != 1 || keys[0] != "users/1/jobs/1/posts.xlsx" {
		t.Fatalf("delete done: %v %v", keys, err)
	}
	if _, err := s.GetJobByID(ctx, cj.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("comments job must be deleted with its run: %v", err)
	}

	// Running job: hidden at once, worker told to stop, results discarded on complete
	run, _ := s.CreateJob(ctx, u.ID, "run", "recent", 10)
	_, _ = s.ClaimJob(ctx, "w")
	if keys, err := s.DeleteJob(ctx, u.ID, run.ID); err != nil || keys != nil {
		t.Fatalf("delete running: %v %v", keys, err)
	}
	if _, err := s.GetJob(ctx, u.ID, run.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted running job must be hidden: %v", err)
	}
	if jobs, _ := s.ListJobs(ctx, u.ID, 10); len(jobs) != 0 {
		t.Fatalf("list must hide deleted jobs: %d", len(jobs))
	}
	if n, _ := s.CountActiveJobs(ctx, u.ID); n != 0 {
		t.Fatalf("deleted job must not count as active: %d", n)
	}
	if stop, err := s.Heartbeat(ctx, run.ID, 5, nil, nil); err != nil || !stop {
		t.Fatalf("worker must be asked to stop: %v %v", stop, err)
	}
	if dropped, err := s.DropIfDeleted(ctx, run.ID); err != nil || !dropped {
		t.Fatalf("drop: %v %v", dropped, err)
	}
	if dropped, _ := s.DropIfDeleted(ctx, run.ID); dropped {
		t.Fatal("second drop must be a no-op")
	}
}
