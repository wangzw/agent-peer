package syscontainer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPairRole(t *testing.T) {
	cases := []struct {
		value string
		want  Role
	}{
		{"", ""},
		{"A", RoleA},
		{"B", RoleB},
		{"C", ""},
		{"a", ""},
	}
	for _, tc := range cases {
		t.Setenv(PairRoleEnv, tc.value)
		if got := PairRole(); got != tc.want {
			t.Errorf("PairRole() with %s=%q = %q, want %q", PairRoleEnv, tc.value, got, tc.want)
		}
		if got := InPair(); got != (tc.want != "") {
			t.Errorf("InPair() with %s=%q = %t, want %t", PairRoleEnv, tc.value, got, tc.want != "")
		}
	}
}

func TestPeerRole(t *testing.T) {
	if got := peerRole(RoleA); got != RoleB {
		t.Errorf("peerRole(A) = %q, want %q", got, RoleB)
	}
	if got := peerRole(RoleB); got != RoleA {
		t.Errorf("peerRole(B) = %q, want %q", got, RoleA)
	}
}

func TestPairFailureName(t *testing.T) {
	if got := pairFailureName(RoleA); got != "failed-A" {
		t.Errorf("pairFailureName(A) = %q, want failed-A", got)
	}
	if got := pairFailureName(RoleB); got != "failed-B" {
		t.Errorf("pairFailureName(B) = %q, want failed-B", got)
	}
}

func TestWritePairFile(t *testing.T) {
	dir := t.TempDir()
	if err := writePairFile(dir, "pairing", []byte("hostA:17443\nkey\n")); err != nil {
		t.Fatalf("writePairFile: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "pairing"))
	if err != nil {
		t.Fatalf("reading the written file: %v", err)
	}
	if got, want := string(data), "hostA:17443\nkey\n"; got != want {
		t.Errorf("written file = %q, want %q", got, want)
	}

	// The temporary file must be gone: rename is the only file the exchange
	// leaves behind, and a poller that sees a half-written temp would race.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading the directory: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("the directory holds %d entries, want only the written file", len(entries))
	}
}

func TestWritePairFileRejectsInvalidNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"", ".", "..", "a/b", "../escape"} {
		if err := writePairFile(dir, name, []byte("x")); err == nil {
			t.Errorf("writePairFile accepted the name %q", name)
		}
	}
	if err := writePairFile("", "pairing", []byte("x")); err == nil {
		t.Error("writePairFile accepted an empty directory")
	}
}

func TestPairAwaitReadsTheOtherHostsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(PairRoleEnv, string(RoleA))
	t.Setenv(pairSharedEnv, dir)
	if err := writePairFile(dir, "connected", []byte("connected\n")); err != nil {
		t.Fatalf("writePairFile: %v", err)
	}
	if got, want := string(PairAwait(t, "connected")), "connected\n"; got != want {
		t.Errorf("PairAwait returned %q, want %q", got, want)
	}
}
