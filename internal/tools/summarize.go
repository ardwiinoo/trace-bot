package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// matrixSeries is the shared shape of Loki and Prometheus range-query results.
type matrixSeries struct {
	Metric map[string]string `json:"metric"`
	Values [][2]any          `json:"values"` // [unix seconds (float), "value"]
}

type point struct {
	t time.Time
	v float64
}

func (s matrixSeries) points() []point {
	pts := make([]point, 0, len(s.Values))
	for _, kv := range s.Values {
		ts, ok1 := kv[0].(float64)
		vs, ok2 := kv[1].(string)
		if !ok1 || !ok2 {
			continue
		}
		v, err := strconv.ParseFloat(vs, 64)
		if err != nil || math.IsNaN(v) {
			continue
		}
		sec := math.Floor(ts)
		pts = append(pts, point{t: time.Unix(int64(sec), int64((ts-sec)*1e9)), v: v})
	}
	return pts
}

func parseMatrix(raw json.RawMessage) ([]matrixSeries, error) {
	var ss []matrixSeries
	if err := json.Unmarshal(raw, &ss); err != nil {
		return nil, err
	}
	return ss, nil
}

func fmtLabels(m map[string]string) string {
	if len(m) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%q", k, m[k])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func fmtNum(v float64) string {
	return strconv.FormatFloat(v, 'g', 6, 64)
}

// summarizeMatrix reduces range-query results to stats plus a downsampled
// series, so the LLM sees min/max/peak time without thousands of raw points.
func (e *env) summarizeMatrix(ss []matrixSeries) string {
	const maxSeries, maxPoints = 8, 20

	type stat struct {
		s             matrixSeries
		pts           []point
		min, max, avg float64
		maxAt         time.Time
		last          float64
	}
	var stats []stat
	for _, s := range ss {
		pts := s.points()
		if len(pts) == 0 {
			continue
		}
		st := stat{s: s, pts: pts, min: math.Inf(1), max: math.Inf(-1), last: pts[len(pts)-1].v}
		sum := 0.0
		for _, p := range pts {
			sum += p.v
			st.min = math.Min(st.min, p.v)
			if p.v > st.max {
				st.max, st.maxAt = p.v, p.t
			}
		}
		st.avg = sum / float64(len(pts))
		stats = append(stats, st)
	}
	if len(stats) == 0 {
		return "No data points returned (empty result)."
	}
	sort.Slice(stats, func(i, j int) bool { return stats[i].max > stats[j].max })

	var b strings.Builder
	fmt.Fprintf(&b, "%d series (times in %s)\n", len(stats), e.Loc)
	for i, st := range stats {
		if i == maxSeries {
			fmt.Fprintf(&b, "... %d more series omitted (sorted by max desc)\n", len(stats)-maxSeries)
			break
		}
		fmt.Fprintf(&b, "\nseries %s\n  n=%d min=%s max=%s (at %s) avg=%s last=%s\n  points: ",
			fmtLabels(st.s.Metric), len(st.pts), fmtNum(st.min), fmtNum(st.max), e.fmtTime(st.maxAt), fmtNum(st.avg), fmtNum(st.last))
		step := 1
		if len(st.pts) > maxPoints {
			step = (len(st.pts) + maxPoints - 1) / maxPoints
		}
		var parts []string
		for j := 0; j < len(st.pts); j += step {
			parts = append(parts, fmt.Sprintf("%s=%s", st.pts[j].t.In(e.Loc).Format("01-02 15:04"), fmtNum(st.pts[j].v)))
		}
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("\n")
	}
	return b.String()
}
