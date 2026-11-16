// Package config loads backend settings from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ListenAddr  string
	DatabaseURL string
	// Shared secret the worker sends as "Authorization: Bearer <token>" to /internal/*
	WorkerToken string

	S3Endpoint  string // empty = real AWS
	S3Region    string
	S3Bucket    string
	S3AccessKey string
	S3SecretKey string
	S3PathStyle bool

	// Secure flag on the session cookie - must be true behind HTTPS
	CookieSecure bool
	SessionTTL   time.Duration

	// Per-user limits (public service: one Threads account serves everyone)
	QuotaActiveJobs int // queued + running at the same time
	QuotaDailyJobs  int // created in the last 24h
	MaxItemsLimit   int // upper bound for maxItems per job
	DefaultMaxItems int

	// A running job without a heartbeat for this long is considered abandoned
	JobStaleAfter  time.Duration
	JobMaxAttempts int
}

func Load() (Config, error) {
	c := Config{
		ListenAddr:      env("LISTEN_ADDR", ":8080"),
		DatabaseURL:     env("DATABASE_URL", ""),
		WorkerToken:     env("WORKER_TOKEN", ""),
		S3Endpoint:      env("S3_ENDPOINT", ""),
		S3Region:        env("S3_REGION", "us-east-1"),
		S3Bucket:        env("S3_BUCKET", "threads-exports"),
		S3AccessKey:     env("S3_ACCESS_KEY", ""),
		S3SecretKey:     env("S3_SECRET_KEY", ""),
		S3PathStyle:     envBool("S3_PATH_STYLE", true),
		CookieSecure:    envBool("COOKIE_SECURE", false),
		SessionTTL:      envDuration("SESSION_TTL", 30*24*time.Hour),
		QuotaActiveJobs: envInt("QUOTA_ACTIVE_JOBS", 3),
		QuotaDailyJobs:  envInt("QUOTA_DAILY_JOBS", 20),
		MaxItemsLimit:   envInt("MAX_ITEMS_LIMIT", 500),
		DefaultMaxItems: envInt("DEFAULT_MAX_ITEMS", 100),
		JobStaleAfter:   envDuration("JOB_STALE_AFTER", 3*time.Minute),
		JobMaxAttempts:  envInt("JOB_MAX_ATTEMPTS", 2),
	}

	var missing []string
	for name, v := range map[string]string{
		"DATABASE_URL":  c.DatabaseURL,
		"WORKER_TOKEN":  c.WorkerToken,
		"S3_ACCESS_KEY": c.S3AccessKey,
		"S3_SECRET_KEY": c.S3SecretKey,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return c, fmt.Errorf("missing required env vars: %v", missing)
	}
	if len(c.WorkerToken) < 16 {
		return c, fmt.Errorf("WORKER_TOKEN must be at least 16 characters")
	}
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}
