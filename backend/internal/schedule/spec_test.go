package schedule

import (
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	ok := []Spec{
		{Kind: KindEveryHours, EveryHours: 6},
		{Kind: KindEveryHours, EveryHours: 168},
		{Kind: KindDaily, DailyTime: "09:30", Timezone: "Asia/Almaty"},
		{Kind: KindDaily, DailyTime: "00:00", Timezone: "UTC"},
	}
	for _, s := range ok {
		if err := s.Validate(6); err != nil {
			t.Errorf("%+v: unexpected error %v", s, err)
		}
	}
	bad := []Spec{
		{Kind: KindEveryHours, EveryHours: 1},
		{Kind: KindEveryHours, EveryHours: 7},
		{Kind: KindDaily, DailyTime: "24:00", Timezone: "UTC"},
		{Kind: KindDaily, DailyTime: "9:30", Timezone: "UTC"},
		{Kind: KindDaily, DailyTime: "09:30", Timezone: "Mars/Olympus"},
		{Kind: KindDaily, DailyTime: "09:30", Timezone: ""},
		{Kind: "cron"},
	}
	for _, s := range bad {
		if err := s.Validate(6); err == nil {
			t.Errorf("%+v: expected error", s)
		}
	}
	if err := (Spec{Kind: KindEveryHours, EveryHours: 6}).Validate(12); err == nil {
		t.Error("6h must be rejected when the minimum is 12h")
	}
}

func TestNextEveryHours(t *testing.T) {
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	s := Spec{Kind: KindEveryHours, EveryHours: 12}
	if got := s.First(now); !got.Equal(now) {
		t.Errorf("interval schedules start immediately, got %v", got)
	}
	if got, want := s.Next(now), now.Add(12*time.Hour); !got.Equal(want) {
		t.Errorf("next = %v, want %v", got, want)
	}
}

func TestNextDaily(t *testing.T) {
	almaty, _ := time.LoadLocation("Asia/Almaty") // UTC+5
	s := Spec{Kind: KindDaily, DailyTime: "09:00", Timezone: "Asia/Almaty"}

	// 08:00 Almaty -> today 09:00 Almaty
	now := time.Date(2026, 11, 17, 8, 0, 0, 0, almaty)
	if got, want := s.Next(now), time.Date(2026, 11, 17, 9, 0, 0, 0, almaty); !got.Equal(want) {
		t.Errorf("before time: got %v, want %v", got, want)
	}
	// exactly 09:00 -> tomorrow (strictly after)
	now = time.Date(2026, 11, 17, 9, 0, 0, 0, almaty)
	if got, want := s.Next(now), time.Date(2026, 11, 18, 9, 0, 0, 0, almaty); !got.Equal(want) {
		t.Errorf("at time: got %v, want %v", got, want)
	}
	// 23:30 UTC on the 17th is already 04:30 on the 18th in Almaty -> 09:00 on the 18th
	now = time.Date(2026, 11, 17, 23, 30, 0, 0, time.UTC)
	if got, want := s.Next(now), time.Date(2026, 11, 18, 9, 0, 0, 0, almaty); !got.Equal(want) {
		t.Errorf("timezone boundary: got %v, want %v", got, want)
	}
	if got := s.First(now); !got.Equal(s.Next(now)) {
		t.Error("daily schedules start at the next occurrence")
	}

	// DST: Berlin switches to summer time on 2026-03-29; 09:00 wall clock is kept
	berlin, _ := time.LoadLocation("Europe/Berlin")
	sb := Spec{Kind: KindDaily, DailyTime: "09:00", Timezone: "Europe/Berlin"}
	now = time.Date(2026, 3, 28, 10, 0, 0, 0, berlin)
	got := sb.Next(now)
	if got.In(berlin).Hour() != 9 || got.In(berlin).Day() != 29 {
		t.Errorf("DST: got %v", got.In(berlin))
	}
}
