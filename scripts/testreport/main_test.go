package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// stream encodes the events as the JSON lines `go test -json` writes.
func stream(t *testing.T, events ...event) string {
	t.Helper()
	var b strings.Builder
	enc := json.NewEncoder(&b)
	for _, e := range events {
		if err := enc.Encode(e); err != nil {
			t.Fatalf("encoding event: %v", err)
		}
	}
	return b.String()
}

// raw joins JSON lines written as literals, the shape go test -json emits.
// The build-event tests use it instead of encoding an event struct, so a wrong
// JSON tag cannot make the encoder and the decoder agree on the same mistake.
func raw(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

func run(t *testing.T, input string) string {
	t.Helper()
	return runWithProgress(t, input, false)
}

func runWithProgress(t *testing.T, input string, progress bool) string {
	t.Helper()
	var out bytes.Buffer
	r := newReporter(&out, "test", progress)
	if err := r.read(strings.NewReader(input)); err != nil {
		t.Fatalf("read: %v", err)
	}
	r.summary()
	return out.String()
}

func TestReportListsSkippedTestsWithReasons(t *testing.T) {
	input := stream(t,
		event{Action: "run", Package: "p", Test: "TestPass"},
		event{Action: "output", Package: "p", Test: "TestPass", Output: "=== RUN   TestPass\n"},
		event{Action: "pass", Package: "p", Test: "TestPass"},
		event{Action: "run", Package: "p", Test: "TestSkip"},
		event{Action: "output", Package: "p", Test: "TestSkip", Output: "=== RUN   TestSkip\n"},
		event{Action: "output", Package: "p", Test: "TestSkip", Output: "    skip_test.go:12: no account is available\n"},
		event{Action: "output", Package: "p", Test: "TestSkip", Output: "--- SKIP: TestSkip (0.00s)\n"},
		event{Action: "skip", Package: "p", Test: "TestSkip"},
		event{Action: "run", Package: "p", Test: "TestSkip/child"},
		event{Action: "output", Package: "p", Test: "TestSkip/child", Output: "    skip_test.go:20: child reason\n"},
		event{Action: "output", Package: "p", Test: "TestSkip/child", Output: "--- SKIP: TestSkip/child (0.00s)\n"},
		event{Action: "skip", Package: "p", Test: "TestSkip/child"},
		event{Action: "fail", Package: "p", Test: "TestFail"},
		event{Action: "fail", Package: "p"},
	)

	out := run(t, input)
	for _, want := range []string{
		"test: skipped p TestSkip: skip_test.go:12: no account is available",
		"test: skipped p TestSkip/child: skip_test.go:20: child reason",
		"test: p failed (0.00s)",
		"test: 1 passed, 1 failed, 2 skipped, 1 packages failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not contain %q:\n%s", want, out)
		}
	}
}

func TestReportSaysWhenNothingWasSkipped(t *testing.T) {
	input := stream(t,
		event{Action: "run", Package: "p", Test: "TestPass"},
		event{Action: "pass", Package: "p", Test: "TestPass"},
		event{Action: "pass", Package: "p"},
	)

	out := run(t, input)
	if !strings.Contains(out, "test: p passed (0.00s)") {
		t.Errorf("report does not print the finished package:\n%s", out)
	}
	if !strings.Contains(out, "test: 1 passed, 0 failed, 0 skipped, 0 packages failed") {
		t.Errorf("report has the wrong summary:\n%s", out)
	}
}

func TestReportCountsPackagesWithoutTestsApartFromSkippedTests(t *testing.T) {
	input := stream(t,
		event{Action: "skip", Package: "p"},
		event{Action: "pass", Package: "ok", Test: "TestPass"},
	)

	out := run(t, input)
	for _, want := range []string{
		"test: p has no test files",
		"test: 1 passed, 0 failed, 0 skipped, 0 packages failed, 1 packages without test files",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not contain %q:\n%s", want, out)
		}
	}
}

func TestReportPrintsRunningTestsWithProgress(t *testing.T) {
	input := stream(t,
		event{Action: "run", Package: "p", Test: "TestSlow"},
		event{Action: "pass", Package: "p", Test: "TestSlow"},
		event{Action: "pass", Package: "p"},
	)

	quiet := run(t, input)
	if strings.Contains(quiet, "test: running") {
		t.Errorf("report prints running tests without -progress:\n%s", quiet)
	}

	out := runWithProgress(t, input, true)
	if !strings.Contains(out, "test: running p TestSlow") {
		t.Errorf("report does not print a running test with -progress:\n%s", out)
	}
}

func TestReportReprintsFailedTestOutput(t *testing.T) {
	input := stream(t,
		event{Action: "run", Package: "p", Test: "TestFail"},
		event{Action: "output", Package: "p", Test: "TestFail", Output: "=== RUN   TestFail\n"},
		event{Action: "output", Package: "p", Test: "TestFail", Output: "    fail_test.go:9: got 1, want 2\n"},
		event{Action: "output", Package: "p", Test: "TestFail", Output: "--- FAIL: TestFail (0.00s)\n"},
		event{Action: "fail", Package: "p", Test: "TestFail"},
		event{Action: "fail", Package: "p"},
	)

	out := run(t, input)
	for _, want := range []string{
		"test: failed p TestFail",
		"test:     fail_test.go:9: got 1, want 2",
		"test: 0 passed, 1 failed, 0 skipped, 1 packages failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not contain %q:\n%s", want, out)
		}
	}
}

func TestReportReprintsBuildFailureOfATestPackage(t *testing.T) {
	// These are the shapes `go help buildjson` documents: a build event names
	// ImportPath, and the test event that follows a failed build names that
	// ImportPath in FailedBuild rather than in Package.
	input := raw(
		`{"ImportPath":"p [p.test]","Action":"build-output","Output":"# p [p.test]\n"}`,
		`{"ImportPath":"p [p.test]","Action":"build-output","Output":"./x.go:3:1: syntax error\n"}`,
		`{"ImportPath":"p [p.test]","Action":"build-fail"}`,
		`{"Action":"fail","Package":"p","FailedBuild":"p [p.test]"}`,
	)

	out := run(t, input)
	for _, want := range []string{
		"test: failed package p [p.test]",
		"test: ./x.go:3:1: syntax error",
		"test: 0 passed, 0 failed, 0 skipped, 1 packages failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not contain %q:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "./x.go:3:1: syntax error"); got != 1 {
		t.Errorf("compiler output printed %d times, want once:\n%s", got, out)
	}
	if strings.Contains(out, "test: p failed") {
		t.Errorf("report prints the test package twice:\n%s", out)
	}
}

func TestReportCountsANonTestBuildFailure(t *testing.T) {
	input := raw(
		`{"ImportPath":"p","Action":"build-output","Output":"./y.go:5:2: undefined: x\n"}`,
		`{"ImportPath":"p","Action":"build-fail"}`,
	)

	out := run(t, input)
	for _, want := range []string{
		"test: failed package p",
		"test: ./y.go:5:2: undefined: x",
		"test: 0 passed, 0 failed, 0 skipped, 1 packages failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not contain %q:\n%s", want, out)
		}
	}
}

func TestReportCountsEachDependentPackageWhoseBuildFailed(t *testing.T) {
	input := raw(
		`{"ImportPath":"p","Action":"build-output","Output":"./p.go:1:1: syntax error\n"}`,
		`{"ImportPath":"p","Action":"build-fail"}`,
		`{"Action":"fail","Package":"q","FailedBuild":"p"}`,
		`{"Action":"fail","Package":"r","FailedBuild":"p"}`,
	)

	out := run(t, input)
	for _, want := range []string{
		"test: failed package p",
		"test: q failed (0.00s)",
		"test: r failed (0.00s)",
		"test: 0 passed, 0 failed, 0 skipped, 3 packages failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not contain %q:\n%s", want, out)
		}
	}
}

func TestReportIgnoresATruncatedFinalLine(t *testing.T) {
	input := stream(t,
		event{Action: "run", Package: "p", Test: "TestSkip"},
		event{Action: "skip", Package: "p", Test: "TestSkip"},
	) + `{"Action":"output","Package":"p","Test":"TestSkip"`

	out := run(t, input)
	if !strings.Contains(out, "test: skipped p TestSkip: no reason given") {
		t.Errorf("report lost the skip before the truncated line:\n%s", out)
	}
}

func TestReportReadsAnOutputLineLargerThanFourMegabytes(t *testing.T) {
	long := strings.Repeat("x", 5<<20)
	input := stream(t,
		event{Action: "output", Package: "p", Test: "TestLong", Output: long + "\n"},
		event{Action: "pass", Package: "p", Test: "TestLong"},
	)

	out := run(t, input)
	if !strings.Contains(out, "test: 1 passed, 0 failed, 0 skipped, 0 packages failed") {
		t.Errorf("report did not survive a long line:\n%s", out)
	}
}
