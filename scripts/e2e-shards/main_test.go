package main

import (
	"strings"
	"testing"
)

func fixture() ([]shard, []string, map[string][]string) {
	const (
		top    = "example.com/peers/test/e2e"
		helper = "example.com/peers/test/e2e/harness"
		system = "example.com/peers/test/e2e/system"
	)
	shards := []shard{
		{name: "top-a", packages: []string{top}, tests: []string{"TestA"}},
		{name: "top-b", packages: []string{top, helper}, tests: []string{"TestB", "TestC"}},
		{name: "system", packages: []string{system}},
	}
	all := []string{top, helper, system}
	tests := map[string][]string{
		top:    {"TestA", "TestB", "TestC"},
		helper: {"TestHelper"},
		system: {"TestSystem"},
	}
	return shards, all, tests
}

func TestParseTable(t *testing.T) {
	got, err := parseTable(`
# a comment
shard a ./test/e2e -- TestA TestB
shard b ./test/e2e/harness
`)
	if err != nil {
		t.Fatalf("parseTable: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("shards = %d, want 2", len(got))
	}
	if got[0].name != "a" || strings.Join(got[0].packages, " ") != "./test/e2e" ||
		strings.Join(got[0].tests, " ") != "TestA TestB" {
		t.Errorf("first shard = %+v", got[0])
	}
	if got[1].name != "b" || strings.Join(got[1].packages, " ") != "./test/e2e/harness" || len(got[1].tests) != 0 {
		t.Errorf("second shard = %+v", got[1])
	}
}

func TestParseTableRejectsDuplicates(t *testing.T) {
	for name, text := range map[string]string{
		"shard": "shard a ./test/e2e\nshard a ./test/e2e/system",
		"test":  "shard a ./test/e2e -- TestA\nshard b ./test/e2e -- TestA",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseTable(text); err == nil {
				t.Fatal("parseTable accepted a duplicate")
			}
		})
	}
}

func TestRunRegex(t *testing.T) {
	if got := runRegex(nil); got != "" {
		t.Errorf("runRegex(nil) = %q, want empty", got)
	}
	if got, want := runRegex([]string{"TestB", "TestA", "TestB"}), "^(TestA|TestB)$"; got != want {
		t.Errorf("runRegex = %q, want %q", got, want)
	}
}

func TestVerifyAcceptsCompleteAssignment(t *testing.T) {
	shards, all, tests := fixture()
	if err := verify(shards, all, tests); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyRejectsUnassignedTest(t *testing.T) {
	shards, all, tests := fixture()
	shards[1].tests = []string{"TestB"}
	err := verify(shards, all, tests)
	if err == nil || !strings.Contains(err.Error(), "TestC is assigned to no shard") {
		t.Fatalf("verify error = %v, want an unassigned TestC", err)
	}
}

func TestVerifyRejectsTestAssignedTwice(t *testing.T) {
	shards, all, tests := fixture()
	shards[0].tests = []string{"TestA", "TestB"}
	err := verify(shards, all, tests)
	if err == nil || !strings.Contains(err.Error(), "TestB is assigned to top-a, top-b") {
		t.Fatalf("verify error = %v, want TestB assigned twice", err)
	}
}

func TestVerifyRejectsHelperNameCollision(t *testing.T) {
	shards, all, tests := fixture()
	tests["example.com/peers/test/e2e/harness"] = []string{"TestHelper", "TestA"}
	err := verify(shards, all, tests)
	if err == nil || !strings.Contains(err.Error(), "TestA is assigned to top-a, top-b") {
		t.Fatalf("verify error = %v, want a collision on TestA", err)
	}
}

func TestVerifyRejectsUnknownDeclaredTest(t *testing.T) {
	shards, all, tests := fixture()
	shards[0].tests = []string{"TestA", "TestMissing"}
	err := verify(shards, all, tests)
	if err == nil || !strings.Contains(err.Error(), "declares TestMissing") {
		t.Fatalf("verify error = %v, want an unknown declared test", err)
	}
}

func TestVerifyRejectsUnassignedPackage(t *testing.T) {
	shards, all, tests := fixture()
	all = append(all, "example.com/peers/test/e2e/ctl")
	tests["example.com/peers/test/e2e/ctl"] = []string{"TestCtl"}
	err := verify(shards, all, tests)
	if err == nil || !strings.Contains(err.Error(), "package example.com/peers/test/e2e/ctl is assigned to no shard") {
		t.Fatalf("verify error = %v, want an unassigned package", err)
	}
}

func TestVerifyRejectsSharedHelperPackage(t *testing.T) {
	shards, all, tests := fixture()
	shards[2].packages = []string{
		"example.com/peers/test/e2e/system",
		"example.com/peers/test/e2e/harness",
	}
	err := verify(shards, all, tests)
	if err == nil || !strings.Contains(err.Error(), "package example.com/peers/test/e2e/harness is assigned to top-b, system") {
		t.Fatalf("verify error = %v, want a shared helper package", err)
	}
}

func TestRunNamesForWithoutTopPackage(t *testing.T) {
	names, err := runNamesFor(shard{name: "system", packages: []string{"example.com/peers/test/e2e/system"}}, "example.com/peers/test/e2e", func(string) ([]string, error) {
		t.Fatal("runNamesFor listed a package for a shard without the top-level package")
		return nil, nil
	})
	if err != nil || len(names) != 0 {
		t.Fatalf("runNamesFor = %v, %v; want no names", names, err)
	}
}
