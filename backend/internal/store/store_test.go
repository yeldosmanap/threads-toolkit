package store_test

// Integration tests against a real Postgres. Skipped unless TEST_DATABASE_URL is set, e.g.:
//   TEST_DATABASE_URL=postgres://threads:threads@127.0.0.1:55432/threads_test?sslmode=disable go test ./...
// The database is wiped on every run.

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/db"
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
