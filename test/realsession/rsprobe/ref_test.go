package main

import (
	"bytes"
	"strings"
	"testing"
)

// testAgentID pads prefix to the 64 hexadecimal characters of a real Agent ID.
func testAgentID(prefix string) string {
	return prefix + strings.Repeat("0", 64-len(prefix))
}

func TestAgentIDKnownVectors(t *testing.T) {
	// REQ-agent-messaging-006: the ref must use the same algorithm as Claude
	// Code's session list, which is the SHA-256 of "session:" plus the path.
	cases := []struct {
		path string
		want string
	}{
		{"/tmp/rs/a.sock", "37927dce3ed899d8128f42ed152d440c626f62b741fa57d61fd17bf88278de99"},
		{"/tmp/rs/b.sock", "7289300651a19c85879da723d5dfda8be14f80adf0ff5e40bbee880d1775b8d6"},
	}
	for _, tc := range cases {
		got, err := agentID(tc.path)
		if err != nil {
			t.Fatalf("agentID(%q): %v", tc.path, err)
		}
		if got != tc.want {
			t.Errorf("agentID(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestRefRejectsBadPath(t *testing.T) {
	// A path the Broker would refuse must not get an identifier, because the
	// Broker never registers an Agent under it. A bad argument anywhere on the
	// command line fails the whole command before it prints anything.
	for _, args := range [][]string{
		{"relative.sock"},
		{"/tmp/../x.sock"},
		{"/tmp/rs/a.sock", "relative.sock"},
	} {
		var out bytes.Buffer
		if err := runRef(args, &out); err == nil {
			t.Errorf("runRef(%q) accepted a path the Broker rejects", args)
		}
		if out.Len() != 0 {
			t.Errorf("runRef(%q) wrote %q before failing", args, out.String())
		}
	}
}

func TestRefCommandExitsTwoOnBadPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := -1
	if err := runRefCommand([]string{"relative.sock"}, &stdout, &stderr, func(c int) { code = c }); err != nil {
		t.Fatalf("runRefCommand: %v", err)
	}
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.HasPrefix(stderr.String(), "rsprobe: relative.sock: ") {
		t.Errorf("stderr = %q, want the rejected path on standard error", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want no output", stdout.String())
	}
}

func TestRefCommandKeepsExitOnGoodPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exited := false
	if err := runRefCommand([]string{"/tmp/rs/a.sock"}, &stdout, &stderr, func(int) { exited = true }); err != nil {
		t.Fatalf("runRefCommand: %v", err)
	}
	if exited {
		t.Error("runRefCommand exited on a good path")
	}
	if !strings.HasPrefix(stdout.String(), "/tmp/rs/a.sock 37927dce3ed8 ") {
		t.Errorf("stdout = %q, want the path, id prefix and short ref", stdout.String())
	}
}

func TestShortRefs(t *testing.T) {
	id1 := "abcdef0123000000000000000000000000000000000000000000000000000000"
	id2 := "abcdef0199000000000000000000000000000000000000000000000000000000"
	id3 := "123456789a000000000000000000000000000000000000000000000000000000"
	id4 := "abcdef012345aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	id5 := "abcdef012345bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	want := map[string]string{
		id1: "abcdef012",
		id2: "abcdef019",
		id3: "123456",
		// The first 12 characters collide, so both keep all 12.
		id4: "abcdef012345",
		id5: "abcdef012345",
	}
	for name, group := range map[string][]string{
		"distinct prefixes":  {id1, id2, id3},
		"colliding prefixes": {id4, id5},
	} {
		refs := shortRefs(group)
		for _, id := range group {
			if got := refs[id]; got != want[id] {
				t.Errorf("%s: shortRefs[%s] = %q, want %q", name, id, got, want[id])
			}
		}
	}
}
