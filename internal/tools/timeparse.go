package tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var relRe = regexp.MustCompile(`^now(?:\s*-\s*(\d+)\s*([smhdw]))?$`)

var unitDur = map[string]time.Duration{
	"s": time.Second,
	"m": time.Minute,
	"h": time.Hour,
	"d": 24 * time.Hour,
	"w": 7 * 24 * time.Hour,
}

// parseTime accepts "now", "now-30m", RFC3339, or a local wall-clock time such
// as "2026-11-01 02:00" / "2026-11-01T02:00:00" (interpreted in loc).
func parseTime(s string, now time.Time, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if m := relRe.FindStringSubmatch(strings.ToLower(s)); m != nil {
		if m[1] == "" {
			return now, nil
		}
		n, _ := strconv.Atoi(m[1])
		return now.Add(-time.Duration(n) * unitDur[m[2]]), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse time %q: use RFC3339 (2026-11-01T02:00:00+07:00), 'YYYY-MM-DD HH:MM' in local time, or 'now-1h'", s)
}

// timeRange resolves optional start/end strings; defaults to the last hour.
func (e *env) timeRange(start, end string) (time.Time, time.Time, error) {
	now := e.now()
	if end == "" {
		end = "now"
	}
	if start == "" {
		start = "now-1h"
	}
	t1, err := parseTime(start, now, e.Loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	t2, err := parseTime(end, now, e.Loc)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if !t2.After(t1) {
		return time.Time{}, time.Time{}, fmt.Errorf("end (%s) must be after start (%s)", t2.Format(time.RFC3339), t1.Format(time.RFC3339))
	}
	if t2.Sub(t1) > 31*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("time range too large (max 31 days)")
	}
	return t1, t2, nil
}

func (e *env) fmtTime(t time.Time) string {
	return t.In(e.Loc).Format("2006-01-02 15:04:05")
}
