package api

import (
	"strings"
	"testing"
	"time"

	"github.com/yeldosmanap/threads-toolkit/backend/internal/config"
	"github.com/yeldosmanap/threads-toolkit/backend/internal/store"
)

func TestValidateJob(t *testing.T) {
	s := &Server{cfg: config.Config{MaxItemsLimit: 500, DefaultMaxItems: 100}}

	req := createJobRequest{Keyword: "  билайн   kz ", MaxItems: 0}
	if msg := s.validateJob(&req); msg != "" {
		t.Fatalf("unexpected error: %s", msg)
	}
	if req.Keyword != "билайн kz" || req.Filter != "recent" || req.MaxItems != 100 {
		t.Errorf("normalized = %+v", req)
	}

	bad := []createJobRequest{
		{Keyword: ""},
		{Keyword: "   "},
		{Keyword: strings.Repeat("я", 101)},
		{Keyword: "ok", Filter: "popular"},
		{Keyword: "ok", MaxItems: 501},
		{Keyword: "ok", MaxItems: -1},
		{Keyword: "bad\u0000kw"},
	}
	for _, b := range bad {
		if msg := s.validateJob(&b); msg == "" {
			t.Errorf("expected error for %+v", b)
		}
	}
}

func TestValidateCredentials(t *testing.T) {
	if msg := validateCredentials(credentials{Email: "user@example.com", Password: "longenough"}); msg != "" {
		t.Errorf("valid credentials rejected: %s", msg)
	}
	for _, c := range []credentials{
		{Email: "not-an-email", Password: "longenough"},
		{Email: "Name <user@example.com>", Password: "longenough"},
		{Email: "user@example.com", Password: "short"},
		{Email: "user@example.com", Password: strings.Repeat("x", 73)},
	} {
		if msg := validateCredentials(c); msg == "" {
			t.Errorf("expected error for %+v", c)
		}
	}
}

func TestExportFilename(t *testing.T) {
	j := store.Job{ID: 42, Keyword: "билайн / KZ!", CreatedAt: time.Date(2026, 11, 17, 5, 0, 0, 0, time.UTC)}
	if got, want := exportFilename(j), "threads-билайн_KZ-2026-11-17-42.xlsx"; got != want {
		t.Errorf("exportFilename = %q, want %q", got, want)
	}
}

func TestScrollBudget(t *testing.T) {
	if got := scrollBudgetSecs(10); got != 85 {
		t.Errorf("small job budget = %d", got)
	}
	if got := scrollBudgetSecs(5000); got != 1800 {
		t.Errorf("budget must be capped at 1800, got %d", got)
	}
}
