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

func TestScheduleInput(t *testing.T) {
	s := &Server{cfg: config.Config{MaxItemsLimit: 500, DefaultMaxItems: 100, MinScheduleHours: 6}}

	in, msg := s.scheduleInput(scheduleRequest{Keyword: " билайн ", Kind: "every_hours", EveryHours: 12, DailyTime: "09:00"})
	if msg != "" || in.Keyword != "билайн" || in.MaxItems != 100 || !in.Enabled || in.Spec.DailyTime != "" || in.Spec.Timezone != "UTC" {
		t.Fatalf("every_hours: %+v %q", in, msg)
	}
	off := false
	in, msg = s.scheduleInput(scheduleRequest{Keyword: "kcell", Kind: "daily", DailyTime: "21:15", EveryHours: 6, Timezone: "Asia/Almaty", Enabled: &off})
	if msg != "" || in.Enabled || in.Spec.EveryHours != 0 || in.Spec.DailyTime != "21:15" {
		t.Fatalf("daily: %+v %q", in, msg)
	}
	for _, bad := range []scheduleRequest{
		{Keyword: "x", Kind: "every_hours", EveryHours: 1},
		{Keyword: "x", Kind: "daily", DailyTime: "25:00"},
		{Keyword: "x", Kind: "weekly"},
		{Keyword: "", Kind: "every_hours", EveryHours: 6},
		{Keyword: "x", Kind: "every_hours", EveryHours: 6, MaxItems: 10000},
	} {
		if _, msg := s.scheduleInput(bad); msg == "" {
			t.Errorf("expected error for %+v", bad)
		}
	}
}

func TestProjectInput(t *testing.T) {
	in, msg := projectInput(projectRequest{
		Name:             "  Beeline   KZ ",
		Aliases:          []string{"билайн", " Билайн ", "", "@beeline_kz"},
		OfficialAccounts: []string{"@beeline_kz", "beeline_kz"},
		Competitors:      []string{"Activ", "activ", "Kcell"},
		Notes:            "Starlink Mobile — услуга Beeline",
	})
	if msg != "" {
		t.Fatalf("unexpected error: %s", msg)
	}
	if in.Name != "Beeline KZ" || in.Market != "Казахстан" || len(in.Aliases) != 2 || len(in.OfficialAccounts) != 1 ||
		in.OfficialAccounts[0] != "beeline_kz" || len(in.Competitors) != 2 {
		t.Fatalf("normalized: %+v", in)
	}
	many := make([]string, 11)
	for i := range many {
		many[i] = strings.Repeat("a", i+1)
	}
	for _, bad := range []projectRequest{
		{Name: "", Aliases: []string{"x"}},
		{Name: "x"},
		{Name: "x", Aliases: many},
		{Name: "x", Aliases: []string{"x"}, OfficialAccounts: []string{"bad account!"}},
		{Name: "x", Aliases: []string{"x"}, Notes: strings.Repeat("я", 2001)},
	} {
		if _, msg := projectInput(bad); msg == "" {
			t.Errorf("expected error for %+v", bad)
		}
	}
}
