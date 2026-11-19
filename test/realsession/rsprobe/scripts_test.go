package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	realSessionWorkflowPath = "../../../.github/workflows/real-session.yml"
	probeScriptsDir         = "../probes"
)

// TestRealSessionWorkflowShape checks the parts of the workflow that decide
// when a probe runs, what it may do, and where its evidence may be uploaded.
func TestRealSessionWorkflowShape(t *testing.T) {
	doc := loadRealSessionWorkflow(t)

	triggers := asMap(t, doc["on"], "on")
	if len(triggers) != 1 {
		t.Fatalf("triggers = %v, want only push", mapKeys(triggers))
	}
	push := asMap(t, triggers["push"], "on.push")
	if len(push) != 1 {
		t.Fatalf("push trigger = %v, want only branches", mapKeys(push))
	}
	branches := asSlice(t, push["branches"], "on.push.branches")
	if len(branches) != 1 || branches[0] != "probe/**" {
		t.Errorf("on.push.branches = %v, want [probe/**]", branches)
	}

	permissions := asMap(t, doc["permissions"], "permissions")
	if len(permissions) != 1 || permissions["contents"] != "read" {
		t.Errorf("permissions = %v, want {contents: read}", permissions)
	}

	jobs := asMap(t, doc["jobs"], "jobs")
	if len(jobs) != 1 {
		t.Fatalf("jobs = %v, want only probe", mapKeys(jobs))
	}
	probe := asMap(t, jobs["probe"], "jobs.probe")
	if _, ok := probe["permissions"]; ok {
		t.Errorf("jobs.probe has its own permissions block; the job must rely on the workflow-level contents: read")
	}

	env := asMap(t, probe["env"], "jobs.probe.env")
	if _, ok := env["CLAUDE_CODE_OAUTH_TOKEN"]; ok {
		t.Errorf("jobs.probe.env contains CLAUDE_CODE_OAUTH_TOKEN; the token must be step-scoped")
	}
	if got := asString(t, env["R"], "jobs.probe.env.R"); got != "/tmp/agent-peer-rs" {
		t.Errorf("jobs.probe.env.R = %q, want /tmp/agent-peer-rs", got)
	}

	// The token reaches exactly the steps that need it, and no other step.
	steps := asSlice(t, probe["steps"], "jobs.probe.steps")
	const wantToken = "${{ secrets.CLAUDE_CODE_OAUTH_TOKEN }}"
	var tokenSteps []string
	for _, raw := range steps {
		step := asMap(t, raw, "workflow step")
		rawEnv, ok := step["env"]
		if !ok {
			continue
		}
		stepEnv := asMap(t, rawEnv, "step env")
		value, ok := stepEnv["CLAUDE_CODE_OAUTH_TOKEN"]
		if !ok {
			continue
		}
		name := asString(t, step["name"], "step name")
		tokenSteps = append(tokenSteps, name)
		if value != wantToken {
			t.Errorf("%s: CLAUDE_CODE_OAUTH_TOKEN = %v, want %q", name, value, wantToken)
		}
	}
	wantTokenSteps := []string{"Prepare", "Run the probe", "Scan the evidence for the token"}
	if strings.Join(tokenSteps, ",") != strings.Join(wantTokenSteps, ",") {
		t.Errorf("steps with CLAUDE_CODE_OAUTH_TOKEN = %v, want %v", tokenSteps, wantTokenSteps)
	}

	// The setup steps touch the network and have hung before; each carries a
	// timeout so a stuck download fails the run instead of holding the job
	// until the workflow timeout.
	for name, want := range map[string]int{
		"Install tools":       10,
		"Install Claude Code": 10,
		"Build":               15,
	} {
		step := findWorkflowStepByName(t, steps, name)
		got, ok := step["timeout-minutes"].(int)
		if !ok {
			t.Errorf("%s has no integer timeout-minutes; got %v", name, step["timeout-minutes"])
			continue
		}
		if got != want {
			t.Errorf("%s timeout-minutes = %d, want %d", name, got, want)
		}
	}

	// The scan is the main defense: it runs before the upload, removes the
	// evidence on a hit, and fails when the token is empty. It runs even when
	// an earlier step failed, so a failed probe still leaves evidence.
	scanIndex := findWorkflowStepIndex(t, steps, "Scan the evidence for the token")
	uploadIndex := findWorkflowStepIndexByUses(t, steps, "actions/upload-artifact@")
	if scanIndex > uploadIndex {
		t.Errorf("the scan step is at index %d, after the upload at %d", scanIndex, uploadIndex)
	}
	scan := asMap(t, steps[scanIndex], "scan step")
	if got := asString(t, scan["id"], "scan id"); got != "scan" {
		t.Errorf("the scan step id = %q, want scan", got)
	}
	const wantScanIf = "${{ !cancelled() }}"
	if got := asString(t, scan["if"], "scan if"); got != wantScanIf {
		t.Errorf("the scan step if = %q, want %q", got, wantScanIf)
	}
	scanRun := asString(t, scan["run"], "scan run")
	for _, want := range []string{
		`-z "$CLAUDE_CODE_OAUTH_TOKEN"`,
		`grep -rqF -- "$CLAUDE_CODE_OAUTH_TOKEN" "$R/out"`,
		`rm -rf "$R/out"`,
	} {
		if !strings.Contains(scanRun, want) {
			t.Errorf("the scan step does not contain %q", want)
		}
	}

	// The upload runs only after a passing scan and only when the job was not
	// cancelled; that way a failed probe still uploads its evidence, while a
	// token hit or a cancelled run leaves nothing behind.
	upload := asMap(t, steps[uploadIndex], "upload step")
	const wantUploadIf = "${{ !cancelled() && steps.scan.outcome == 'success' }}"
	if got := asString(t, upload["if"], "upload-artifact if"); got != wantUploadIf {
		t.Errorf("upload-artifact if = %q, want %q", got, wantUploadIf)
	}
	with := asMap(t, upload["with"], "upload-artifact.with")
	const wantPath = "/tmp/agent-peer-rs/out/${{ env.PROBE }}/"
	if got := asString(t, with["path"], "upload-artifact.path"); got != wantPath {
		t.Errorf("upload-artifact path = %q, want %q", got, wantPath)
	}
	if got := asString(t, with["name"], "upload-artifact.name"); got != "probe-${{ env.PROBE }}" {
		t.Errorf("upload-artifact name = %q, want %q", got, "probe-${{ env.PROBE }}")
	}
	if got := asString(t, with["if-no-files-found"], "upload-artifact.if-no-files-found"); got != "error" {
		t.Errorf("upload-artifact if-no-files-found = %q, want error", got)
	}
}

// TestProbeScriptsDoNotDumpEnvironment checks that probe scripts and the
// workflow never print whole environments or enable shell tracing.
func TestProbeScriptsDoNotDumpEnvironment(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(probeScriptsDir, "*.sh"))
	if err != nil {
		t.Fatalf("globbing probe scripts: %v", err)
	}
	paths = append(paths, realSessionWorkflowPath)

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		// lib.sh reads /proc/<pid>/environ through rs_proc_vars; that one
		// helper is the reason the function exists, so lib.sh is exempt from
		// the process-environment patterns.
		allowProcEnvReads := filepath.Base(path) == "lib.sh"
		for _, finding := range environmentDumpFindings(string(data), allowProcEnvReads) {
			t.Errorf("%s dumps the environment: %q", path, finding)
		}
	}

	// The check must report a script that turns on shell tracing.
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.sh")
	if err := os.WriteFile(bad, []byte("#!/usr/bin/env bash\nset -x\n"), 0o600); err != nil {
		t.Fatalf("writing the negative control: %v", err)
	}
	data, err := os.ReadFile(bad)
	if err != nil {
		t.Fatalf("reading the negative control: %v", err)
	}
	if findings := environmentDumpFindings(string(data), false); len(findings) == 0 {
		t.Fatalf("the check does not report a script containing set -x")
	}

	// Each forbidden form is caught, while reading named variables and the
	// workflow's env blocks are not.
	for _, line := range []string{
		"set -x",
		"printenv",
		"declare -p",
		"declare -x",
		"export -p",
		"set",
		"set;",
		"export",
		"compgen -e",
		"set -o xtrace",
		"bash -x probe.sh",
		"/usr/bin/env",
		`"$R/run" /usr/bin/env >f`,
		"/proc/1/environ",
		`cat /proc/1/environ`,
		`env >"$OUT/e.txt"`,
		"env>f",
		"env",
		"cmd; env",
		"then env",
		`p="${X#y}"; env`,
	} {
		if findings := environmentDumpFindings(line, false); len(findings) == 0 {
			t.Errorf("the check does not report %q", line)
		}
	}
	// The run-wrapper form is the most likely way the token reaches the
	// evidence: env is not at the start of a command segment, so a check that
	// only matches a segment-initial env would miss it.
	t.Run("run wrapper env redirect", func(t *testing.T) {
		const line = `"$R/run" env >"$OUT/x"`
		if findings := environmentDumpFindings(line, false); len(findings) == 0 {
			t.Errorf("the check does not report %q", line)
		}
	})

	for _, line := range []string{
		"      env:",
		"env FOO=1 cmd",
		`p="${GITHUB_REF_NAME#probe/}"`,
		"cmd # set -x",
		"set -euo pipefail",
		"export FOO=1",
		"declare -r X=1",
		"compgen -c",
		"bash probe.sh",
		"#!/usr/bin/env bash",
		"environment: ok",
	} {
		if findings := environmentDumpFindings(line, false); len(findings) != 0 {
			t.Errorf("the check reports a harmless line %q as %v", line, findings)
		}
	}

	// lib.sh may read /proc/<pid>/environ, but only through the helper: the
	// same read in any other script is still reported, and /usr/bin/env is
	// reported even in lib.sh because the exemption covers environ alone.
	if findings := environmentDumpFindings("cat /proc/1/environ", true); len(findings) != 0 {
		t.Errorf("the lib.sh exemption still reports /proc/1/environ as %v", findings)
	}
	if findings := environmentDumpFindings("/usr/bin/env", true); len(findings) == 0 {
		t.Errorf("the lib.sh exemption still hides /usr/bin/env")
	}
}

// TestProbeScriptsUseTheLibraryCleanupChain checks that probe scripts register
// cleanup with rs_on_exit instead of installing their own trap. A script that
// installs its own trap replaces the library's, so a failing step would no
// longer capture the windows or run the other registered cleanups.
func TestProbeScriptsUseTheLibraryCleanupChain(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join(probeScriptsDir, "*.sh"))
	if err != nil {
		t.Fatalf("globbing probe scripts: %v", err)
	}
	if len(paths) == 0 {
		t.Fatalf("no probe scripts found in %s", probeScriptsDir)
	}
	for _, path := range paths {
		if filepath.Base(path) == "lib.sh" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if strings.Contains(string(data), "trap ") {
			t.Errorf("%s installs its own trap; register cleanup with rs_on_exit instead", path)
		}
	}
}

// TestProbeNamesMatchScripts checks that every probe script that exists is a
// known probe, and that the workflow's case list is exactly probeNames.
//
// The reverse direction is asserted too: every name in probeNames must have a
// script under probes/, with lib.sh the only exception.
func TestProbeNamesMatchScripts(t *testing.T) {
	known := make(map[string]bool, len(probeNames))
	for _, name := range probeNames {
		if known[name] {
			t.Fatalf("probeNames lists %q twice", name)
		}
		known[name] = true
	}

	entries, err := os.ReadDir(probeScriptsDir)
	if err != nil {
		t.Fatalf("reading %s: %v", probeScriptsDir, err)
	}
	scripts := make(map[string]bool, len(probeNames))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sh") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".sh")
		if name == "lib" {
			continue
		}
		scripts[name] = true
		if !known[name] {
			t.Errorf("probe script %s is not in probeNames", entry.Name())
		}
	}
	for _, name := range probeNames {
		if !scripts[name] {
			t.Errorf("probeNames lists %q but probes/%s.sh does not exist", name, name)
		}
	}

	doc := loadRealSessionWorkflow(t)
	if code, ok := unknownProbeExitCode(t, doc); !ok || code == 0 {
		t.Errorf("the case branch for an unknown probe must exit non-zero; exit code = %d (found = %v)", code, ok)
	}

	caseNames := workflowCaseNames(t, doc)
	seen := make(map[string]bool, len(caseNames))
	for _, name := range caseNames {
		if !known[name] {
			t.Errorf("the workflow case lists %q, which is not in probeNames", name)
		}
		if seen[name] {
			t.Errorf("the workflow case lists %q twice", name)
		}
		seen[name] = true
	}
	for _, name := range probeNames {
		if !seen[name] {
			t.Errorf("the workflow case does not list %q", name)
		}
	}
}

// TestLibResolvesComposeFromScriptLocation checks that lib.sh finds
// test/realsession/compose.yaml from its own location rather than the caller's
// working directory. The workflow runs a probe from the repository root, but a
// developer may start one anywhere; a relative -f path then fails with "no
// configuration file provided".
func TestLibResolvesComposeFromScriptLocation(t *testing.T) {
	lib, err := filepath.Abs(filepath.Join(probeScriptsDir, "lib.sh"))
	if err != nil {
		t.Fatalf("resolving the lib.sh path: %v", err)
	}
	root := t.TempDir()
	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		// Drop any ambient value so a pre-fix lib.sh cannot pass by accident.
		if strings.HasPrefix(kv, "RS_COMPOSE=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "R="+root, "PROBE=selftest")

	cmd := exec.Command("bash", "-c", `source "$1"; printf '%s' "$RS_COMPOSE"`, "bash", lib)
	cmd.Dir = root
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sourcing lib.sh from %s: %v\n%s", root, err, out)
	}
	got, err := filepath.EvalSymlinks(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("resolving RS_COMPOSE %q: %v", strings.TrimSpace(string(out)), err)
	}
	want, err := filepath.Abs(filepath.Join("..", "compose.yaml"))
	if err != nil {
		t.Fatalf("resolving the expected compose path: %v", err)
	}
	want, err = filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatalf("resolving the expected compose path %q: %v", want, err)
	}
	if got != want {
		t.Errorf("RS_COMPOSE = %q, want %q", got, want)
	}
}

// TestRsProcVarsReportsAbsence checks that rs_proc_vars distinguishes a
// missing variable from one set to the empty string. A probe treats the two
// differently, so the helper must not print both as NAME=.
func TestRsProcVarsReportsAbsence(t *testing.T) {
	lib, err := filepath.Abs(filepath.Join(probeScriptsDir, "lib.sh"))
	if err != nil {
		t.Fatalf("resolving the lib.sh path: %v", err)
	}
	root := t.TempDir()
	cmd := exec.Command("bash", "-c", `source "$1"; rs_proc_vars "$$" RS_PRESENT RS_EMPTY RS_ABSENT`, "bash", lib)
	cmd.Dir = root
	// A fixed environment makes the absent variable reliably absent; bash
	// records it in /proc/<pid>/environ exactly as given.
	cmd.Env = []string{
		"R=" + root,
		"PROBE=selftest",
		"PATH=" + os.Getenv("PATH"),
		"RS_PRESENT=value",
		"RS_EMPTY=",
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sourcing lib.sh in a controlled environment: %v\n%s", err, out)
	}
	want := "RS_PRESENT=value\nRS_EMPTY=\nRS_ABSENT absent\n"
	if string(out) != want {
		t.Errorf("rs_proc_vars output = %q, want %q", out, want)
	}
}

// TestRsWaitReplyReadsAssistantTextOnly checks the shared reply predicate:
// the prompt echo in a user record must not count as an answer, only text
// blocks count, and a transcript without assistant text is reported as its
// own result so a probe can say it fell back to the pane.
func TestRsWaitReplyReadsAssistantTextOnly(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("rs_wait_reply parses transcripts with jq")
	}
	lib, err := filepath.Abs(filepath.Join(probeScriptsDir, "lib.sh"))
	if err != nil {
		t.Fatalf("resolving the lib.sh path: %v", err)
	}

	writeTranscript := func(t *testing.T, lines ...string) string {
		t.Helper()
		root := t.TempDir()
		dir := filepath.Join(root, "cfg", "projects", "p")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("creating the transcript directory: %v", err)
		}
		data := strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(dir, "t.jsonl"), []byte(data), 0o600); err != nil {
			t.Fatalf("writing the transcript: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(root, "out", "rsreply"), 0o755); err != nil {
			t.Fatalf("creating the output directory: %v", err)
		}
		return root
	}

	cases := []struct {
		name        string
		lines       []string
		marker      string
		wantStatus  int
		wantRecords int
	}{
		{
			name: "assistant text counts and the prompt echo does not",
			lines: []string{
				`{"type":"user","message":{"role":"user","content":"reply with rs-test-ok"}}`,
				`{"type":"assistant","message":{"content":[{"type":"text","text":"rs-test-ok"}]}}`,
				`{"type":"assistant","message":{"content":"rs-test-ok"}}`,
				`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"x","input":{"text":"rs-test-ok"}}]}}`,
				`null`,
				`not json`,
			},
			marker:      "rs-test-ok",
			wantStatus:  0,
			wantRecords: 2,
		},
		{
			name: "assistant text without the marker is a miss",
			lines: []string{
				`{"type":"assistant","message":{"content":[{"type":"text","text":"something else"}]}}`,
			},
			marker:      "rs-test-ok",
			wantStatus:  1,
			wantRecords: 0,
		},
		{
			name: "no assistant text is a distinct result",
			lines: []string{
				`{"type":"user","message":{"role":"user","content":"rs-test-ok"}}`,
			},
			marker:      "rs-test-ok",
			wantStatus:  2,
			wantRecords: 0,
		},
	}

	const script = `source "$1"; status=0; rs_wait_reply "$2" reply.jsonl 1 || status=$?; printf 'status=%d\n' "$status"; printf 'records=%s\n' "$(wc -l <"$OUT/reply.jsonl" | tr -d ' ')"`
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeTranscript(t, tc.lines...)
			cmd := exec.Command("bash", "-c", script, "bash", lib, tc.marker)
			cmd.Dir = root
			cmd.Env = []string{
				"R=" + root,
				"PROBE=rsreply",
				"PATH=" + os.Getenv("PATH"),
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("running rs_wait_reply: %v\n%s", err, out)
			}
			want := "status=" + strconv.Itoa(tc.wantStatus) + "\nrecords=" + strconv.Itoa(tc.wantRecords) + "\n"
			if string(out) != want {
				t.Errorf("rs_wait_reply output = %q, want %q", out, want)
			}
		})
	}
}

// TestRatelimitWriteArrivalsCallArity checks that every rl_write_arrivals call
// passes the positional parameters its definition reads. The probe failed on
// the runner with "$3: unbound variable": the function had a label parameter
// that the call sites never supplied, and the mismatch only surfaced after the
// first burst had run.
func TestRatelimitWriteArrivalsCallArity(t *testing.T) {
	path := filepath.Join(probeScriptsDir, "ratelimit.sh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	text := string(data)

	defRE := regexp.MustCompile(`(?m)^rl_write_arrivals\(\)\s*\{\s*\n\s*local\s+([^\n]*)$`)
	match := defRE.FindStringSubmatch(text)
	if match == nil {
		t.Fatalf("%s has no rl_write_arrivals function with a local declaration", path)
	}
	posRE := regexp.MustCompile(`\$([0-9]+)`)
	declared := 0
	for _, p := range posRE.FindAllStringSubmatch(match[1], -1) {
		n, err := strconv.Atoi(p[1])
		if err != nil {
			t.Fatalf("parsing positional parameter %q: %v", p[0], err)
		}
		if n > declared {
			declared = n
		}
	}
	if declared == 0 {
		t.Fatalf("rl_write_arrivals declares no positional parameters")
	}

	callRE := regexp.MustCompile(`(?m)^\s*rl_write_arrivals\s+(.+)$`)
	calls := callRE.FindAllStringSubmatch(text, -1)
	if len(calls) == 0 {
		t.Fatalf("%s never calls rl_write_arrivals", path)
	}
	wordRE := regexp.MustCompile(`\S+`)
	for _, call := range calls {
		args := wordRE.FindAllString(call[1], -1)
		if len(args) != declared {
			t.Errorf("rl_write_arrivals %q passes %d arguments, but the function declares %d", call[1], len(args), declared)
		}
	}
}

// TestRatelimitQueueCounts checks the queue-depth helper the ratelimit probe
// uses to decide that the receiver has finished processing delivered messages.
// Claude Code records enqueue, dequeue and remove operations in the session
// transcript; a positive depth means messages are still waiting, so an idle
// session is not yet quiescent.
func TestRatelimitQueueCounts(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("the transcript helper parses records with jq")
	}
	script, err := filepath.Abs(filepath.Join(probeScriptsDir, "ratelimit.sh"))
	if err != nil {
		t.Fatalf("resolving ratelimit.sh: %v", err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "cfg", "projects", "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the transcript directory: %v", err)
	}
	lines := []string{
		`{"type":"queue-operation","operation":"enqueue"}`,
		`{"type":"queue-operation","operation":"enqueue"}`,
		`{"type":"queue-operation","operation":"enqueue"}`,
		`{"type":"queue-operation","operation":"dequeue"}`,
		`{"type":"queue-operation","operation":"remove"}`,
		`{"type":"user","message":{"role":"user","content":"not a queue record"}}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "t.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("writing the transcript: %v", err)
	}

	// $0 is the probe script, so its source of lib.sh resolves next to it.
	cmd := exec.Command("bash", "-c", `source "$1"; rl_queue_counts`, script, script)
	cmd.Dir = root
	cmd.Env = []string{
		"R=" + root,
		"PROBE=ratelimit",
		"RS_SOURCE_ONLY=1",
		"PATH=" + os.Getenv("PATH"),
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("sourcing ratelimit.sh: %v\n%s", err, out)
	}
	if got, want := string(out), "1 3 1 1\n"; got != want {
		t.Errorf("rl_queue_counts = %q, want %q", got, want)
	}
}

// environmentDumpFindings returns the non-comment lines that dump an
// environment or enable shell tracing. allowProcEnvReads exempts only the
// /proc/<pid>/environ read in lib.sh, whose rs_proc_vars helper needs it; any
// other environment read, including /usr/bin/env, is reported everywhere.
func environmentDumpFindings(text string, allowProcEnvReads bool) []string {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`\bset\s+-[a-zA-Z]*x\b`),
		regexp.MustCompile(`\bprintenv\b`),
		regexp.MustCompile(`declare\s+-p`),
		regexp.MustCompile(`declare\s+-[a-zA-Z]*x\b`),
		regexp.MustCompile(`export\s+-p`),
		regexp.MustCompile(`\bcompgen\s+-[a-zA-Z]*e\b`),
		regexp.MustCompile(`\bset\s+-o\s+xtrace\b`),
		regexp.MustCompile(`\bbash\s+-[a-zA-Z]*x\b`),
		regexp.MustCompile(`(^|[;&|(]\s*|\bthen\s+|\bdo\s+)set\s*($|[;&|)])`),
		regexp.MustCompile(`(^|[;&|(]\s*|\bthen\s+|\bdo\s+)export\s*($|[;&|)])`),
		// A bare env anywhere on the line is a dump when it is followed by a
		// redirection or the end of the line. The form '"$R/run" env >file'
		// is the most likely way a token reaches the evidence: env is not at
		// the start of a command segment, so the segment-relative patterns
		// above miss it.
		regexp.MustCompile(`\benv\s*(>|$)`),
		regexp.MustCompile(`/usr/bin/env\b`),
	}
	if !allowProcEnvReads {
		patterns = append(patterns, regexp.MustCompile(`\benviron\b`))
	}
	var findings []string
	for _, line := range strings.Split(text, "\n") {
		line = stripShellComment(line)
		for _, pattern := range patterns {
			if pattern.MatchString(line) {
				findings = append(findings, strings.TrimSpace(line))
				break
			}
		}
	}
	return findings
}

// stripShellComment removes a comment, which in shell and YAML starts at a
// '#' that is at the beginning of the line or follows whitespace. A '#' inside
// a word such as ${GITHUB_REF_NAME#probe/} is not a comment.
func stripShellComment(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] != '#' {
			continue
		}
		if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
			return line[:i]
		}
	}
	return line
}

func loadRealSessionWorkflow(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(realSessionWorkflowPath)
	if err != nil {
		t.Fatalf("reading %s: %v", realSessionWorkflowPath, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parsing %s: %v", realSessionWorkflowPath, err)
	}
	return doc
}

func realSessionProbeJob(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	jobs := asMap(t, doc["jobs"], "jobs")
	return asMap(t, jobs["probe"], "jobs.probe")
}

func findWorkflowStepByName(t *testing.T, steps []any, name string) map[string]any {
	t.Helper()
	for _, raw := range steps {
		step := asMap(t, raw, "workflow step")
		if got, _ := step["name"].(string); got == name {
			return step
		}
	}
	t.Fatalf("the workflow has no step named %q", name)
	return nil
}

// findWorkflowStepIndex returns the position of the step with this name.
func findWorkflowStepIndex(t *testing.T, steps []any, name string) int {
	t.Helper()
	for i, raw := range steps {
		step := asMap(t, raw, "workflow step")
		if got, _ := step["name"].(string); got == name {
			return i
		}
	}
	t.Fatalf("the workflow has no step named %q", name)
	return -1
}

// findWorkflowStepIndexByUses returns the position of the step using an action
// whose name starts with prefix.
func findWorkflowStepIndexByUses(t *testing.T, steps []any, prefix string) int {
	t.Helper()
	for i, raw := range steps {
		step := asMap(t, raw, "workflow step")
		if got, _ := step["uses"].(string); strings.HasPrefix(got, prefix) {
			return i
		}
	}
	t.Fatalf("the workflow has no step using %s", prefix)
	return -1
}

// unknownProbeExitCode returns the exit code of the case branch that handles
// an unknown probe. The second result is false when no such branch exists.
func unknownProbeExitCode(t *testing.T, doc map[string]any) (int, bool) {
	t.Helper()
	probe := realSessionProbeJob(t, doc)
	steps := asSlice(t, probe["steps"], "jobs.probe.steps")
	selectStep := findWorkflowStepByName(t, steps, "Select the probe")
	run := asString(t, selectStep["run"], "Select the probe run")

	var body []string
	inBranch := false
	for _, line := range strings.Split(run, "\n") {
		trimmed := strings.TrimSpace(line)
		if !inBranch {
			if !strings.HasPrefix(trimmed, "*)") {
				continue
			}
			inBranch = true
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "*)"))
		}
		if end := strings.Index(trimmed, ";;"); end >= 0 {
			body = append(body, trimmed[:end])
			return exitCodeIn(strings.Join(body, "\n"))
		}
		if trimmed == "esac" {
			return exitCodeIn(strings.Join(body, "\n"))
		}
		body = append(body, trimmed)
	}
	return 0, false
}

var exitCodePattern = regexp.MustCompile(`\bexit\s+([0-9]+)\b`)

func exitCodeIn(body string) (int, bool) {
	match := exitCodePattern.FindStringSubmatch(body)
	if match == nil {
		return 0, true
	}
	code, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, false
	}
	return code, true
}

func workflowCaseNames(t *testing.T, doc map[string]any) []string {
	t.Helper()
	probe := realSessionProbeJob(t, doc)
	steps := asSlice(t, probe["steps"], "jobs.probe.steps")
	selectStep := findWorkflowStepByName(t, steps, "Select the probe")
	run := asString(t, selectStep["run"], "Select the probe run")

	var names []string
	inCase := false
	for _, line := range strings.Split(run, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "case "):
			inCase = true
		case inCase && trimmed == "esac":
			return names
		case inCase:
			if end := strings.IndexByte(trimmed, ')'); end >= 0 {
				pattern := strings.TrimSpace(trimmed[:end])
				if pattern != "" && pattern != "*" {
					names = append(names, pattern)
				}
			}
		}
	}
	t.Fatalf("the Select the probe step has no closing esac")
	return nil
}

func asMap(t *testing.T, value any, what string) map[string]any {
	t.Helper()
	typed, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want a mapping", what, value)
	}
	return typed
}

func asSlice(t *testing.T, value any, what string) []any {
	t.Helper()
	typed, ok := value.([]any)
	if !ok {
		t.Fatalf("%s is %T, want a sequence", what, value)
	}
	return typed
}

func asString(t *testing.T, value any, what string) string {
	t.Helper()
	typed, ok := value.(string)
	if !ok {
		t.Fatalf("%s is %T, want a string", what, value)
	}
	return typed
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
