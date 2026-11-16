// Package store holds all SQL. Handlers never talk to the database directly.
package store

import (
	"errors"
	"time"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrEmailTaken = errors.New("email already registered")
	// ErrJobState: the job is not in a state that allows the operation (e.g. completing a canceled job)
	ErrJobState = errors.New("job is not in the expected state")
)

type User struct {
	ID        int64
	Email     string
	IsAdmin   bool
	CreatedAt time.Time
}

const (
	JobQueued   = "queued"
	JobRunning  = "running"
	JobDone     = "done"
	JobFailed   = "failed"
	JobCanceled = "canceled"
)

type Job struct {
	ID            int64
	UserID        int64
	Keyword       string
	Filter        string
	MaxItems      int
	Status        string
	PostsSeen     int
	PostsCount    *int
	NewPostsCount *int
	Error         *string
	FileKey       *string
	Attempts      int
	ScheduleID    *int64
	CreatedAt     time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	// Approximate place in the global queue (1 = next), only for queued jobs
	QueuePosition *int
	// No earlier run of the same search (user + keyword) has results, so "new" flags carry no information
	IsFirstRun bool
}

// Post is one scraped post as received from the worker.
type Post struct {
	ID             string
	URL            string
	AuthorUsername string
	AuthorName     string
	AuthorVerified bool
	AuthorURL      string
	Content        string
	PublishedAt    *time.Time
	Likes          int
	Replies        int
	Reposts        int
	Shares         int
	Images         []string
	Videos         []string
	Links          []string
	QuotedPostURL  string
}

// SavedPost is a post together with whether this run is the first to find it.
type SavedPost struct {
	Post
	IsNew bool
}
