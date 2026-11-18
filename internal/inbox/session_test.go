package inbox

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestREQAgentMessaging001_LookupFindsRecord(t *testing.T) {
	// REQ-agent-messaging-001
	dir := t.TempDir()
	socket := filepath.Join(dir, "inbox.sock")
	aWriteRecord(t, dir, 5001, map[string]any{
		"pid":                 9999, // ignored: the file name is authoritative
		"name":                "api-worker",
		"status":              "busy",
		"messagingSocketPath": socket,
	})

	s, err := Lookup(dir, socket)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if s.PID != 5001 {
		t.Fatalf("PID = %d, want 5001 from the file name", s.PID)
	}
	if s.Name != "api-worker" || s.Status != "busy" || s.SocketPath != socket {
		t.Fatalf("record fields not returned verbatim: %+v", s)
	}
	if s.ConfigDir != dir {
		t.Fatalf("ConfigDir = %q, want %q", s.ConfigDir, dir)
	}
}

func TestREQAgentMessaging005_RereadPicksUpRename(t *testing.T) {
	// REQ-agent-messaging-005
	dir := t.TempDir()
	socket := filepath.Join(dir, "inbox.sock")
	aWriteRecord(t, dir, 5002, map[string]any{
		"name":                "api-worker",
		"messagingSocketPath": socket,
	})
	before, err := Lookup(dir, socket)
	if err != nil {
		t.Fatalf("Lookup before rename: %v", err)
	}

	aWriteRecord(t, dir, 5002, map[string]any{
		"name":                "api-v2",
		"messagingSocketPath": socket,
	})
	after, err := Reread(before)
	if err != nil {
		t.Fatalf("Reread after rename: %v", err)
	}
	if after.Name != "api-v2" {
		t.Fatalf("Reread Name = %q, want api-v2", after.Name)
	}
	if after.PID != 5002 || after.SocketPath != socket || after.ConfigDir != dir {
		t.Fatalf("Reread changed unrelated fields: %+v", after)
	}
}

func TestREQAgentMessaging010_StatusVerbatim(t *testing.T) {
	// REQ-agent-messaging-010
	dir := t.TempDir()
	cases := []struct {
		name   string
		status string
	}{
		{"idle", "idle"},
		{"busy", "busy"},
		{"waiting", "waiting"},
		{"shell", "shell"},
		{"unrecognized", "paused"},
		{"missing", ""},
	}
	for i, tc := range cases {
		pid := 5100 + i
		socket := filepath.Join(dir, "status-"+strconv.Itoa(i)+".sock")
		if tc.status == "" {
			aWriteRaw(t, dir, pid, []byte(`{"name":"`+tc.name+`","messagingSocketPath":`+strconv.Quote(socket)+`}`))
		} else {
			aWriteRecord(t, dir, pid, map[string]any{
				"name":                tc.name,
				"status":              tc.status,
				"messagingSocketPath": socket,
			})
		}
		s, err := Lookup(dir, socket)
		if err != nil {
			t.Fatalf("Lookup %s: %v", tc.name, err)
		}
		if s.Status != tc.status {
			t.Fatalf("Lookup Status = %q, want %q", s.Status, tc.status)
		}
		again, err := Reread(s)
		if err != nil {
			t.Fatalf("Reread %s: %v", tc.name, err)
		}
		if again.Status != tc.status {
			t.Fatalf("Reread Status = %q, want %q", again.Status, tc.status)
		}
	}
}

func TestREQAgentMessaging033_RereadAfterSessionIDChange(t *testing.T) {
	// REQ-agent-messaging-033
	dir := t.TempDir()
	socket := filepath.Join(dir, "inbox.sock")
	aWriteRecord(t, dir, 5201, map[string]any{
		"name":                "api-worker",
		"status":              "idle",
		"messagingSocketPath": socket,
		"sessionId":           "11111111-1111-4111-8111-111111111111",
	})
	before, err := Lookup(dir, socket)
	if err != nil {
		t.Fatalf("Lookup before session id change: %v", err)
	}

	// The frozen fakesession API has no setter for sessionId, so this test
	// rewrites the record file directly. The record rewrite is what the
	// fixture would do; the assertion is what matters here.
	aWriteRecord(t, dir, 5201, map[string]any{
		"name":                "api-worker",
		"status":              "idle",
		"messagingSocketPath": socket,
		"sessionId":           "22222222-2222-4222-8222-222222222222",
	})
	after, err := Reread(before)
	if err != nil {
		t.Fatalf("Reread after session id change: %v", err)
	}
	if after.Name != "api-worker" || after.PID != 5201 || after.SocketPath != socket || after.ConfigDir != dir {
		t.Fatalf("session id change altered the Agent identity: %+v", after)
	}
	again, err := Lookup(dir, socket)
	if err != nil {
		t.Fatalf("Lookup after session id change: %v", err)
	}
	if again.Name != "api-worker" || again.PID != 5201 {
		t.Fatalf("Lookup changed across a session id change: %+v", again)
	}
}

func TestREQAgentMessaging036_HeadlessRecord(t *testing.T) {
	// REQ-agent-messaging-036
	dir := t.TempDir()
	cases := []struct {
		pid        int
		name       string
		kind       string
		entrypoint string
	}{
		{5301, "headless-agent", "headless", "cli"},
		{5302, "ide-agent", "interactive", "vscode"},
	}
	for _, tc := range cases {
		socket := filepath.Join(dir, tc.name+".sock")
		aWriteRecord(t, dir, tc.pid, map[string]any{
			"name":                tc.name,
			"kind":                tc.kind,
			"entrypoint":          tc.entrypoint,
			"messagingSocketPath": socket,
		})
		s, err := Lookup(dir, socket)
		if err != nil {
			t.Fatalf("Lookup %s: %v", tc.name, err)
		}
		if s.Name != tc.name || s.PID != tc.pid {
			t.Fatalf("record filtered by kind or entrypoint: %+v", s)
		}
		if _, err := Reread(s); err != nil {
			t.Fatalf("Reread %s: %v", tc.name, err)
		}
	}
}

func TestLookup_NewestMtimeWins(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "shared.sock")
	olderPID := aWriteRecord(t, dir, 5401, map[string]any{
		"name":                "older",
		"messagingSocketPath": socket,
	})
	newerPID := aWriteRecord(t, dir, 5402, map[string]any{
		"name":                "newer",
		"messagingSocketPath": socket,
	})
	aSetModTime(t, newerPID, time.Unix(1000, 0))
	aSetModTime(t, olderPID, time.Unix(2000, 0))

	s, err := Lookup(dir, socket)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if s.PID != 5401 || s.Name != "older" {
		t.Fatalf("newest modification time did not win: %+v", s)
	}

	// Equal modification times fall back to the larger process id.
	aSetModTime(t, olderPID, time.Unix(2000, 0))
	aSetModTime(t, newerPID, time.Unix(2000, 0))
	s, err = Lookup(dir, socket)
	if err != nil {
		t.Fatalf("Lookup with equal modification times: %v", err)
	}
	if s.PID != 5402 {
		t.Fatalf("equal modification times chose PID %d, want the larger 5402", s.PID)
	}
}

func TestLookup_SkipsPartialAndBadFiles(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "inbox.sock")
	good := aWriteRecord(t, dir, 5501, map[string]any{
		"name":                "good",
		"messagingSocketPath": socket,
	})
	sessions := aSessionsDir(dir)

	aWriteRaw(t, dir, 5502, []byte(`{"pid":5502,`)) // half written
	aWriteRaw(t, dir, 5503, []byte{})               // empty
	aWriteFile(t, filepath.Join(sessions, "notes.txt"), []byte("unrelated"))
	aWriteFile(t, filepath.Join(sessions, "007.json"), []byte(
		`{"name":"noncanonical","messagingSocketPath":`+strconv.Quote(socket)+`}`))
	if err := os.Symlink(good, filepath.Join(sessions, "5504.json")); err != nil {
		t.Fatalf("creating symlink record: %v", err)
	}
	aWriteFile(t, filepath.Join(sessions, "5505.json"), []byte(strings.Repeat("x", maxRecordSize+1)))
	if err := os.Mkdir(filepath.Join(sessions, "5506.json"), 0o700); err != nil {
		t.Fatalf("creating directory record: %v", err)
	}

	s, err := Lookup(dir, socket)
	if err != nil {
		t.Fatalf("Lookup alongside bad records: %v", err)
	}
	if s.PID != 5501 || s.Name != "good" {
		t.Fatalf("Lookup returned a bad record: %+v", s)
	}
}

// aCreateFIFO creates a FIFO record file. Opening it for reading would block
// until a writer appears, so Lookup and Reread must not open it at all.
func aCreateFIFO(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("creating FIFO %s: %v", path, err)
	}
}

// aCreateSocketFile leaves a Unix socket file at path, as a crashed or
// still-running session's inbox would.
func aCreateSocketFile(t *testing.T, path string) {
	t.Helper()
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatalf("creating Unix socket file %s: %v", path, err)
	}
	ln.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatalf("closing Unix socket %s: %v", path, err)
	}
}

func TestLookup_SkipsFIFOAndSocketFiles(t *testing.T) {
	// The socket record needs a path short enough for macOS's ~104-byte Unix
	// socket limit, which t.TempDir() under /var/folders does not provide.
	dir := bShortTempDir(t)
	socket := filepath.Join(dir, "inbox.sock")
	aWriteRecord(t, dir, 5507, map[string]any{
		"name":                "good",
		"messagingSocketPath": socket,
	})
	sessions := aSessionsDir(dir)
	aCreateFIFO(t, filepath.Join(sessions, "5508.json"))
	aCreateSocketFile(t, filepath.Join(sessions, "5509.json"))

	type lookupResult struct {
		s   Session
		err error
	}
	done := make(chan lookupResult, 1)
	go func() {
		s, err := Lookup(dir, socket)
		done <- lookupResult{s: s, err: err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Lookup alongside a FIFO and a socket record: %v", got.err)
		}
		if got.s.PID != 5507 || got.s.Name != "good" {
			t.Fatalf("Lookup returned a non-regular record: %+v", got.s)
		}
	case <-time.After(aWaitTimeout):
		t.Fatal("Lookup blocked on a non-regular record")
	}
}

func TestReread_NonRegularAndOversizedRecordsAreNoRecord(t *testing.T) {
	// REQ-agent-messaging-005
	cases := []struct {
		name  string
		pid   int
		build func(t *testing.T, configDir, path string)
	}{
		{"symlink", 5801, func(t *testing.T, configDir, path string) {
			target := aWriteRecord(t, configDir, 5890, map[string]any{"name": "target"})
			if err := os.Symlink(target, path); err != nil {
				t.Fatalf("creating symlink record: %v", err)
			}
		}},
		{"fifo", 5802, func(t *testing.T, configDir, path string) { aCreateFIFO(t, path) }},
		{"socket", 5803, func(t *testing.T, configDir, path string) { aCreateSocketFile(t, path) }},
		{"directory", 5804, func(t *testing.T, configDir, path string) {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatalf("creating directory record: %v", err)
			}
		}},
		{"oversized", 5805, func(t *testing.T, configDir, path string) {
			aWriteFile(t, path, []byte(strings.Repeat("x", maxRecordSize+1)))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A Unix socket record must fit macOS's path limit; keep the
			// config directory under /tmp rather than /var/folders.
			dir := bShortTempDir(t)
			if err := os.MkdirAll(aSessionsDir(dir), 0o700); err != nil {
				t.Fatalf("creating sessions directory: %v", err)
			}
			tc.build(t, dir, filepath.Join(aSessionsDir(dir), strconv.Itoa(tc.pid)+".json"))

			type rereadResult struct {
				s   Session
				err error
			}
			done := make(chan rereadResult, 1)
			go func() {
				s, err := Reread(Session{ConfigDir: dir, PID: tc.pid})
				done <- rereadResult{s: s, err: err}
			}()
			select {
			case got := <-done:
				if !errors.Is(got.err, ErrNoRecord) {
					t.Fatalf("Reread of a %s record error = %v, want ErrNoRecord", tc.name, got.err)
				}
				if got.s != (Session{}) {
					t.Fatalf("Reread returned a non-zero Session with ErrNoRecord: %+v", got.s)
				}
			case <-time.After(aWaitTimeout):
				t.Fatalf("Reread blocked on a %s record", tc.name)
			}
		})
	}
}

func TestLookup_LenientFields(t *testing.T) {
	dir := t.TempDir()

	missingSocket := filepath.Join(dir, "missing.sock")
	aWriteRaw(t, dir, 5601, []byte(`{"messagingSocketPath":`+strconv.Quote(missingSocket)+`}`))
	s, err := Lookup(dir, missingSocket)
	if err != nil {
		t.Fatalf("Lookup a record with missing fields: %v", err)
	}
	if s.PID != 5601 || s.Name != "" || s.Status != "" {
		t.Fatalf("missing fields were not read as empty: %+v", s)
	}

	wrongSocket := filepath.Join(dir, "wrong.sock")
	aWriteRaw(t, dir, 5602, []byte(
		`{"name":42,"status":true,"messagingSocketPath":`+strconv.Quote(wrongSocket)+`,"extra":{"nested":[1,2]}}`))
	s, err = Lookup(dir, wrongSocket)
	if err != nil {
		t.Fatalf("Lookup a record with mistyped fields: %v", err)
	}
	if s.Name != "" || s.Status != "" || s.SocketPath != wrongSocket {
		t.Fatalf("mistyped fields were not read as empty: %+v", s)
	}
}

func TestLookup_SocketPathComparedVerbatim(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "inbox.sock")
	aWriteRecord(t, dir, 5701, map[string]any{
		"name":                "agent",
		"messagingSocketPath": socket,
	})

	s, err := Lookup(dir, socket)
	if err != nil {
		t.Fatalf("Lookup with the exact socket path: %v", err)
	}
	if s.PID != 5701 {
		t.Fatalf("Lookup with the exact socket path returned %+v", s)
	}

	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("resolving working directory: %v", err)
	}
	relative, err := filepath.Rel(cwd, socket)
	if err != nil {
		t.Fatalf("making the socket path relative: %v", err)
	}
	// Another spelling of the same path is another string: the plan requires
	// a plain string comparison, without path normalization.
	for _, spelling := range []string{
		relative,
		socket + string(filepath.Separator),
		dir + string(filepath.Separator) + "." + string(filepath.Separator) + "inbox.sock",
	} {
		if _, err := Lookup(dir, spelling); !errors.Is(err, ErrNoRecord) {
			t.Fatalf("Lookup with socket path %q error = %v, want ErrNoRecord", spelling, err)
		}
	}
}

func TestReread_NoRecordAndPartial(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "inbox.sock")
	path := aWriteRecord(t, dir, 5801, map[string]any{
		"name":                "agent",
		"messagingSocketPath": socket,
	})
	s, err := Lookup(dir, socket)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("removing record: %v", err)
	}
	removed, err := Reread(s)
	if !errors.Is(err, ErrNoRecord) {
		t.Fatalf("Reread of a removed record error = %v, want ErrNoRecord", err)
	}
	if removed != (Session{}) {
		t.Fatalf("Reread returned a non-zero Session with ErrNoRecord: %+v", removed)
	}

	aWriteRaw(t, dir, 5801, []byte(`{"pid":5801,`))
	partial, err := Reread(s)
	if !errors.Is(err, ErrPartial) {
		t.Fatalf("Reread of a half-written record error = %v, want ErrPartial", err)
	}
	if partial != (Session{}) {
		t.Fatalf("Reread returned a non-zero Session with ErrPartial: %+v", partial)
	}

	// An empty file is a regular file Claude Code has truncated before writing
	// the first byte; it is partial, not gone.
	aWriteRaw(t, dir, 5801, []byte{})
	empty, err := Reread(s)
	if !errors.Is(err, ErrPartial) {
		t.Fatalf("Reread of an empty record error = %v, want ErrPartial", err)
	}
	if empty != (Session{}) {
		t.Fatalf("Reread returned a non-zero Session with ErrPartial: %+v", empty)
	}

	aWriteRecord(t, dir, 5801, map[string]any{
		"name":                "recovered",
		"messagingSocketPath": socket,
	})
	recovered, err := Reread(s)
	if err != nil {
		t.Fatalf("Reread of a recovered record: %v", err)
	}
	if recovered.Name != "recovered" {
		t.Fatalf("Reread Name = %q, want recovered", recovered.Name)
	}

	if _, err := Reread(Session{ConfigDir: dir, PID: 0}); !errors.Is(err, ErrNoRecord) {
		t.Fatalf("Reread of a non-positive PID error = %v, want ErrNoRecord", err)
	}
}

func TestSessionsPathIsRegularFile(t *testing.T) {
	// When sessions is a regular file rather than a directory, Lookup and
	// Reread agree: no record was found.
	dir := t.TempDir()
	if err := os.WriteFile(aSessionsDir(dir), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("writing a regular file at the sessions path: %v", err)
	}
	if _, err := Lookup(dir, "/tmp/inbox.sock"); !errors.Is(err, ErrNoRecord) {
		t.Fatalf("Lookup with a regular file at the sessions path error = %v, want ErrNoRecord", err)
	}
	if _, err := Reread(Session{ConfigDir: dir, PID: 6001}); !errors.Is(err, ErrNoRecord) {
		t.Fatalf("Reread with a regular file at the sessions path error = %v, want ErrNoRecord", err)
	}
}

func TestReread_DoesNotCompareSocketPath(t *testing.T) {
	dir := t.TempDir()
	aWriteRecord(t, dir, 5901, map[string]any{
		"name":                "old-session",
		"messagingSocketPath": "/tmp/old-inbox.sock",
	})
	old, err := Lookup(dir, "/tmp/old-inbox.sock")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	// The process exited and its id was reused by a new session, which
	// rewrote the same record file with its own socket. Reread returns the
	// record as it stands: ErrNoRecord means the record is gone, not that its
	// socket path changed.
	aWriteRecord(t, dir, 5901, map[string]any{
		"name":                "new-session",
		"messagingSocketPath": "/tmp/new-inbox.sock",
	})
	reused, err := Reread(old)
	if err != nil {
		t.Fatalf("Reread after the socket path changed: %v", err)
	}
	if reused.Name != "new-session" || reused.SocketPath != "/tmp/new-inbox.sock" {
		t.Fatalf("Reread did not return the rewritten record verbatim: %+v", reused)
	}

	// Binding an inbox for the first time and unbinding are returned as-is.
	aWriteRecord(t, dir, 5902, map[string]any{
		"name":                "binding",
		"messagingSocketPath": "",
	})
	unbound := Session{ConfigDir: dir, PID: 5902}
	aWriteRecord(t, dir, 5902, map[string]any{
		"name":                "binding",
		"messagingSocketPath": "/tmp/bound-inbox.sock",
	})
	bound, err := Reread(unbound)
	if err != nil {
		t.Fatalf("Reread after the first bind: %v", err)
	}
	if bound.SocketPath != "/tmp/bound-inbox.sock" {
		t.Fatalf("first bind was not reported: %+v", bound)
	}

	aWriteRecord(t, dir, 5903, map[string]any{
		"name":                "unbinding",
		"messagingSocketPath": "/tmp/unbinding-inbox.sock",
	})
	wasBound, err := Lookup(dir, "/tmp/unbinding-inbox.sock")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	aWriteRecord(t, dir, 5903, map[string]any{
		"name":                "unbinding",
		"messagingSocketPath": "",
	})
	nowUnbound, err := Reread(wasBound)
	if err != nil {
		t.Fatalf("Reread after unbinding: %v", err)
	}
	if nowUnbound.SocketPath != "" {
		t.Fatalf("unbinding was not reported: %+v", nowUnbound)
	}
}

func TestParseRecordName(t *testing.T) {
	cases := []struct {
		name string
		pid  int
		ok   bool
	}{
		{"1.json", 1, true},
		{"4201.json", 4201, true},
		{"007.json", 0, false},
		{"0.json", 0, false},
		{".json", 0, false},
		{"x.json", 0, false},
		{"123", 0, false},
		{"123.JSON", 0, false},
		{"123.json.bak", 0, false},
	}
	for _, tc := range cases {
		pid, ok := parseRecordName(tc.name)
		if ok != tc.ok || pid != tc.pid {
			t.Fatalf("parseRecordName(%q) = (%d, %v), want (%d, %v)", tc.name, pid, ok, tc.pid, tc.ok)
		}
	}
}
