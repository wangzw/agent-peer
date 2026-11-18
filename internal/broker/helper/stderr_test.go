package helper

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestHelperStderrTagsUIDAndFlushes(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	w := newHelperStderr(log, 4242)

	if _, err := w.Write([]byte("first line\nsecond")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "first line") {
		t.Fatalf("log %q does not contain the complete line", got)
	}
	if got := buf.String(); strings.Contains(got, "second") {
		t.Fatalf("log %q contains an unterminated line before flush", got)
	}

	w.flush()
	out := buf.String()
	for _, want := range []string{"uid=4242", "event=account_helper_stderr", "second"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log %q does not contain %q", out, want)
		}
	}
}

func TestProcessHelperStderrIsTaggedWithAccount(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	acct, err := LookupAccount(uint32(os.Getuid()))
	if err != nil {
		t.Fatalf("LookupAccount: %v", err)
	}

	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	proc, err := startHelperProcessArgs(exe, []string{"internal", "account-helper", "stderr-marker"}, acct, log)
	if err != nil {
		t.Fatalf("startHelperProcessArgs: %v", err)
	}
	client := NewClient(proc.Stdout, proc.Stdin)
	ctx := testContext(t)
	if _, err := client.WaitReady(ctx); err != nil {
		_ = proc.Kill()
		_ = proc.Wait()
		t.Fatalf("WaitReady: %v", err)
	}
	_ = client.Close()
	if err := proc.Wait(); err != nil {
		t.Fatalf("helper exited with %v, want a clean exit", err)
	}

	out := buf.String()
	for _, want := range []string{"helper stderr marker", fmt.Sprintf("uid=%d", acct.UID)} {
		if !strings.Contains(out, want) {
			t.Fatalf("helper stderr log %q does not contain %q", out, want)
		}
	}
}
