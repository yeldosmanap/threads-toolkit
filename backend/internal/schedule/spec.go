// Package schedule validates recurring-search settings and computes the next run time.
package schedule

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"time"
)

const (
	KindEveryHours = "every_hours"
	KindDaily      = "daily"
)

// AllowedEveryHours are the interval presets offered in the UI.
var AllowedEveryHours = []int{6, 12, 24, 168}

var dailyTimeRe = regexp.MustCompile(`^([01]\d|2[0-3]):([0-5]\d)$`)

type Spec struct {
	Kind       string
	EveryHours int    // for KindEveryHours
	DailyTime  string // "HH:MM" for KindDaily
	Timezone   string // IANA name, e.g. "Asia/Almaty"
}

// Validate returns a user-facing (Russian) error, or nil.
func (s Spec) Validate(minHours int) error {
	switch s.Kind {
	case KindEveryHours:
		if !slices.Contains(AllowedEveryHours, s.EveryHours) {
			return errors.New("интервал должен быть 6, 12, 24 или 168 часов")
		}
		if s.EveryHours < minHours {
			return fmt.Errorf("интервал не может быть меньше %d часов", minHours)
		}
	case KindDaily:
		if !dailyTimeRe.MatchString(s.DailyTime) {
			return errors.New("время должно быть в формате ЧЧ:ММ")
		}
		if _, err := s.location(); err != nil {
			return errors.New("неизвестный часовой пояс")
		}
	default:
		return errors.New("неизвестный тип расписания")
	}
	return nil
}

func (s Spec) location() (*time.Location, error) {
	if s.Timezone == "" || len(s.Timezone) > 64 {
		return nil, errors.New("bad timezone")
	}
	return time.LoadLocation(s.Timezone)
}

// First is when a new (or re-enabled) schedule runs for the first time:
// interval schedules start right away, daily ones at the next occurrence of their time.
func (s Spec) First(now time.Time) time.Time {
	if s.Kind == KindEveryHours {
		return now
	}
	return s.Next(now)
}

// Next returns the first run time strictly after `after`.
// It is computed from "now" rather than from the missed slot, so a backend that was down
// for a day runs a schedule once, not once per missed slot.
func (s Spec) Next(after time.Time) time.Time {
	if s.Kind == KindEveryHours {
		return after.Add(time.Duration(s.EveryHours) * time.Hour)
	}
	loc, err := s.location()
	if err != nil {
		loc = time.UTC
	}
	hh, mm := parseHHMM(s.DailyTime)
	local := after.In(loc)
	candidate := time.Date(local.Year(), local.Month(), local.Day(), hh, mm, 0, 0, loc)
	for !candidate.After(after) {
		// Rebuild from the date so the wall-clock time is kept across DST changes
		candidate = time.Date(candidate.Year(), candidate.Month(), candidate.Day()+1, hh, mm, 0, 0, loc)
	}
	return candidate
}

func parseHHMM(v string) (int, int) {
	m := dailyTimeRe.FindStringSubmatch(v)
	if m == nil {
		return 0, 0
	}
	hh, _ := strconv.Atoi(m[1])
	mm, _ := strconv.Atoi(m[2])
	return hh, mm
}
