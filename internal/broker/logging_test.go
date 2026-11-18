package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// decodeLogLines splits raw into lines and decodes each one as a JSON object.
func decodeLogLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	raw = strings.TrimSuffix(raw, "\n")
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	records := make([]map[string]any, len(lines))
	for i, line := range lines {
		if err := json.Unmarshal([]byte(line), &records[i]); err != nil {
			t.Fatalf("line %d is not a JSON object: %v\nline: %s", i, err, line)
		}
	}
	return records
}

func TestRunLogLinesAreJSON(t *testing.T) {
	// REQ-broker-config-011: structured runtime logs.
	var buf bytes.Buffer
	var level slog.LevelVar
	logger := NewLogger(&buf, &level)

	logger.Info(EvBrokerStarted, "pid", 42)
	logger.Warn(EvConnectionRejected, "uid", 1000)
	logger.Error(EvStartupFailed,
		slog.Group("cause",
			slog.String("phase", "listen"),
			slog.Int("port", 9000),
		),
	)

	records := decodeLogLines(t, buf.String())
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3", len(records))
	}
	wantEvents := []string{EvBrokerStarted, EvConnectionRejected, EvStartupFailed}
	wantLevels := []string{"INFO", "WARN", "ERROR"}
	for i, rec := range records {
		if _, ok := rec["msg"]; ok {
			t.Errorf("record %d has a msg key, want event: %v", i, rec)
		}
		if _, ok := rec["time"]; !ok {
			t.Errorf("record %d has no time key: %v", i, rec)
		}
		if _, ok := rec["level"]; !ok {
			t.Errorf("record %d has no level key: %v", i, rec)
		}
		if _, ok := rec["event"]; !ok {
			t.Errorf("record %d has no event key: %v", i, rec)
		}

		ts, ok := rec["time"].(string)
		if !ok {
			t.Fatalf("record %d time is %T, want string", i, rec["time"])
		}
		if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
			t.Errorf("record %d time %q is not RFC 3339 with nanoseconds: %v", i, ts, err)
		}
		if !strings.HasSuffix(ts, "Z") {
			t.Errorf("record %d time %q is not UTC", i, ts)
		}

		if lvl, ok := rec["level"].(string); !ok || lvl != wantLevels[i] {
			t.Errorf("record %d level = %v, want %q", i, rec["level"], wantLevels[i])
		}
		if ev, ok := rec["event"].(string); !ok || ev != wantEvents[i] {
			t.Errorf("record %d event = %v, want %q", i, rec["event"], wantEvents[i])
		}
	}

	cause, ok := records[2]["cause"].(map[string]any)
	if !ok {
		t.Fatalf("record 2 cause = %T, want a JSON object", records[2]["cause"])
	}
	if got, want := cause["phase"], "listen"; got != want {
		t.Errorf("nested attribute phase = %v, want %q", got, want)
	}
}

// TestLogTimeFormat pins the time format: a record timestamp in a fixed
// non-UTC zone must be rendered in UTC with nanosecond precision.
func TestLogTimeFormat(t *testing.T) {
	var buf bytes.Buffer
	var level slog.LevelVar
	logger := NewLogger(&buf, &level)

	zone := time.FixedZone("UTC+2", 2*60*60)
	record := slog.NewRecord(time.Date(2026, 1, 2, 4, 4, 5, 6, zone), slog.LevelInfo, "time_format", 0)
	if err := logger.Handler().Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	const want = `"time":"2026-01-02T02:04:05.000000006Z"`
	if got := buf.String(); !strings.Contains(got, want) {
		t.Fatalf("log line %q does not contain %s", got, want)
	}
}

func TestLogLevelVarFilters(t *testing.T) {
	var buf bytes.Buffer
	var level slog.LevelVar
	level.Set(slog.LevelWarn)
	logger := NewLogger(&buf, &level)

	logger.Info("info_event")
	logger.Warn("warn_event")
	records := decodeLogLines(t, buf.String())
	if len(records) != 1 {
		t.Fatalf("records at warn level = %d, want 1 (info filtered)", len(records))
	}
	if got := records[0]["event"]; got != "warn_event" {
		t.Errorf("event = %v, want %q", got, "warn_event")
	}

	level.Set(slog.LevelDebug)
	logger.Debug("debug_event")
	records = decodeLogLines(t, buf.String())
	if len(records) != 2 {
		t.Fatalf("records after raising verbosity = %d, want 2", len(records))
	}
	if got := records[1]["event"]; got != "debug_event" {
		t.Errorf("event = %v, want %q", got, "debug_event")
	}
}

func TestLoggerConcurrentWrites(t *testing.T) {
	var buf bytes.Buffer
	var level slog.LevelVar
	logger := NewLogger(&buf, &level)

	const goroutines = 16
	const perGoroutine = 100
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				logger.Info("concurrent_write", "goroutine", g, "iteration", i)
			}
		}(g)
	}
	wg.Wait()

	records := decodeLogLines(t, buf.String())
	if want := goroutines * perGoroutine; len(records) != want {
		t.Fatalf("records = %d, want %d", len(records), want)
	}
}

func TestParseLogLevel(t *testing.T) {
	valid := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
	}
	for in, want := range valid {
		got, err := ParseLogLevel(in)
		if err != nil {
			t.Errorf("ParseLogLevel(%q) returned error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseLogLevel(%q) = %v, want %v", in, got, want)
		}
	}

	for _, in := range []string{"INFO", "", "trace", "Debug", " warning "} {
		if got, err := ParseLogLevel(in); err == nil {
			t.Errorf("ParseLogLevel(%q) = %v, want an error", in, got)
		}
	}
}
