package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/ramtinJ95/tuido/internal/store"
	"github.com/ramtinJ95/tuido/internal/task"
	"github.com/ramtinJ95/tuido/internal/vcs"
)

func vcsFor(st *store.Store) *vcs.Repo {
	return vcs.New(st.Root, store.CacheDir(), st.Cfg.Git.Enabled, st.Cfg.Git.AutoPush)
}

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "sun": time.Sunday,
	"monday": time.Monday, "mon": time.Monday,
	"tuesday": time.Tuesday, "tue": time.Tuesday, "tues": time.Tuesday,
	"wednesday": time.Wednesday, "wed": time.Wednesday,
	"thursday": time.Thursday, "thu": time.Thursday, "thurs": time.Thursday,
	"friday": time.Friday, "fri": time.Friday,
	"saturday": time.Saturday, "sat": time.Saturday,
}

// parseWhen accepts YYYY-MM-DD, today, tomorrow, week, or a weekday name.
//
// A bare weekday always means the next strictly future occurrence: on a Friday,
// `-d friday` is next Friday, not today. That rule is surprising exactly once,
// so it is documented in --help as well as here.
func parseWhen(s string) (task.Date, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	now := time.Now()

	if d, ok := task.ParseDate(s); ok {
		return d, nil
	}
	switch s {
	case "today":
		return task.DateFromTime(now), nil
	case "tomorrow", "tmr":
		return task.DateFromTime(now.AddDate(0, 0, 1)), nil
	case "week":
		return task.DateFromTime(now.AddDate(0, 0, 7)), nil
	}
	if wd, ok := weekdays[s]; ok {
		delta := (int(wd) - int(now.Weekday()) + 7) % 7
		if delta == 0 {
			delta = 7 // strictly future
		}
		return task.DateFromTime(now.AddDate(0, 0, delta)), nil
	}
	return task.Date{}, fmt.Errorf("cannot read %q as a date (YYYY-MM-DD | today | tomorrow | week | <weekday>)", s)
}

func ago(unix int64) string {
	d := time.Since(time.Unix(unix, 0)).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
