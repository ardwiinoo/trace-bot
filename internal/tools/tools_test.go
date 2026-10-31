package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var jkt = time.FixedZone("WIB", 7*3600)

func TestParseTime(t *testing.T) {
	now := time.Date(2026, 11, 1, 10, 0, 0, 0, jkt)
	cases := []struct {
		in   string
		want time.Time
	}{
		{"now", now},
		{"now-30m", now.Add(-30 * time.Minute)},
		{"now-2h", now.Add(-2 * time.Hour)},
		{"now-1d", now.Add(-24 * time.Hour)},
		{"2026-11-01T02:00:00+07:00", time.Date(2026, 11, 1, 2, 0, 0, 0, jkt)},
		{"2026-11-01 02:00", time.Date(2026, 11, 1, 2, 0, 0, 0, jkt)},
	}
	for _, c := range cases {
		got, err := parseTime(c.in, now, jkt)
		if err != nil || !got.Equal(c.want) {
			t.Errorf("parseTime(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	if _, err := parseTime("yesterday-ish", now, jkt); err == nil {
		t.Error("expected error for garbage input")
	}
}

func testEnv(loki, prom string) *env {
	return &env{
		Backends: Backends{LokiURL: loki, PromURL: prom, Loc: jkt},
		http:     http.DefaultClient,
		now:      func() time.Time { return time.Date(2026, 11, 1, 10, 0, 0, 0, jkt) },
	}
}

func TestQueryMetricsSummarizesPeak(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query_range" || r.URL.Query().Get("query") != "sum(rate(x[5m]))" {
			http.Error(w, "unexpected "+r.URL.String(), 400)
			return
		}
		w.Write([]byte(`{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"route":"/api/users"},"values":[[1793498400,"2"],[1793498460,"15.5"],[1793498520,"3"]]}]}}`))
	}))
	defer srv.Close()

	e := testEnv("", srv.URL)
	out, err := e.queryMetricsTool().Run(context.Background(), []byte(`{"query":"sum(rate(x[5m]))","start":"now-1h"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"max=15.5", "min=2", `route="/api/users"`, "n=3"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestQueryLogsStreamsAndErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") == "bad{" {
			http.Error(w, "parse error at line 1, col 4: syntax error", 400)
			return
		}
		w.Write([]byte(`{"status":"success","data":{"resultType":"streams","result":[
			{"stream":{"service_name":"sample-be"},"values":[
				["1793498460000000000","payment gateway error: upstream returned 502"],
				["1793498400000000000","failed to query orders: timeout"]]}]}}`))
	}))
	defer srv.Close()

	e := testEnv(srv.URL, "")
	out, err := e.queryLogsTool().Run(context.Background(), []byte(`{"query":"{service_name=\"sample-be\"}"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2 log lines") || strings.Index(out, "payment gateway") > strings.Index(out, "orders: timeout") {
		t.Errorf("expected 2 lines, newest first:\n%s", out)
	}

	_, err = e.queryLogsTool().Run(context.Background(), []byte(`{"query":"bad{"}`))
	if err == nil || !strings.Contains(err.Error(), "parse error") {
		t.Errorf("backend error should be surfaced to the LLM, got %v", err)
	}
}
