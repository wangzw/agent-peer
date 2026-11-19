package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// probeNames lists the probes the real-session workflow accepts. It is the
// source the script checks compare against.
var probeNames = []string{
	"selftest",
	"preflight",
	"binding",
	"registry",
	"ref-clear",
	"inbound",
	"signature",
	"reply",
	"mcp-toggle",
	"subagent",
	"headless",
	"broker-down",
	"permissions",
	"ratelimit",
}

// pinnedClaudeVersion is the Claude Code version every probe runs against.
const pinnedClaudeVersion = "2.1.311"

func init() {
	register("env init", cmdEnvInit)
	register("env check", cmdEnvCheck)
	register("env usage", cmdEnvUsage)
	register("env clean", cmdEnvClean)
}

func cmdEnvInit(args []string) error {
	return runEnvInit(args, os.Stdout)
}

func cmdEnvCheck(args []string) error {
	return runEnvCheck(args, os.Stdout)
}

func cmdEnvUsage(args []string) error {
	return runEnvUsage(args, os.Stdout)
}

func cmdEnvClean(args []string) error {
	return runEnvClean(args, os.Stdout)
}

// envAccountHome mirrors config.accountHomeDir: the account's real home, with
// os.UserHomeDir as the fallback.
func envAccountHome() (string, error) {
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir, nil
	}
	return os.UserHomeDir()
}

// envDefaultAgentPeerHome is the Broker data directory the probes use when
// --agent-peer-home is not given.
func envDefaultAgentPeerHome() (string, error) {
	home, err := envAccountHome()
	if err != nil {
		return "", fmt.Errorf("cannot determine the home directory: %w", err)
	}
	if home == "" {
		return "", errors.New("the account has no home directory")
	}
	return filepath.Join(home, ".agent-peer"), nil
}

// envCurrentUser returns the account name the run wrapper exports.
func envCurrentUser() string {
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if name := os.Getenv("USER"); name != "" {
		return name
	}
	return "root"
}

// runEnvInit implements "rsprobe env init --root R --claude-bin B
// --agent-peer-bin A [--agent-peer-home H]".
func runEnvInit(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("env init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", "", "probe root directory")
	claudeBin := fs.String("claude-bin", "", "Claude Code executable to install into the probe")
	agentPeerBin := fs.String("agent-peer-bin", "", "agent-peer executable to install into the probe")
	agentPeerHome := fs.String("agent-peer-home", "", "Broker data directory (default: the account's ~/.agent-peer)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *root == "" {
		return errors.New("--root is required")
	}
	if *claudeBin == "" {
		return errors.New("--claude-bin is required")
	}
	if *agentPeerBin == "" {
		return errors.New("--agent-peer-bin is required")
	}
	rootPath, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	homePath := *agentPeerHome
	if homePath == "" {
		homePath, err = envDefaultAgentPeerHome()
		if err != nil {
			return err
		}
	}
	homePath, err = filepath.Abs(homePath)
	if err != nil {
		return err
	}

	// Refuse before touching anything when the Broker data directory already
	// exists: a stray local run must not overwrite the developer's config.
	if _, err := os.Stat(homePath); err == nil {
		return fmt.Errorf("refusing to touch existing %s", homePath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	type envDir struct {
		path string
		mode os.FileMode
	}
	dirs := []envDir{
		{filepath.Join(rootPath, "bin"), 0o755},
		{filepath.Join(rootPath, "cfg"), 0o755},
		{filepath.Join(rootPath, "cfg", "sessions"), 0o700},
		{filepath.Join(rootPath, "home"), 0o755},
		{filepath.Join(rootPath, "debug"), 0o755},
		{filepath.Join(rootPath, "ctl"), 0o755},
	}
	for _, name := range probeNames {
		dirs = append(dirs,
			envDir{filepath.Join(rootPath, "work", name), 0o755},
			envDir{filepath.Join(rootPath, "out", name), 0o755},
		)
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir.path, dir.mode); err != nil {
			return err
		}
		if err := os.Chmod(dir.path, dir.mode); err != nil {
			return err
		}
	}

	claudePath := filepath.Join(rootPath, "bin", "claude")
	if err := envCopyExecutable(*claudeBin, claudePath); err != nil {
		return err
	}
	if err := envCopyExecutable(*agentPeerBin, filepath.Join(rootPath, "bin", "agent-peer")); err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find the running executable: %w", err)
	}
	if err := envCopyExecutable(self, filepath.Join(rootPath, "bin", "rsprobe")); err != nil {
		return err
	}

	versionOut, err := exec.Command(claudePath, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("run %s --version: %w (%s)", claudePath, err, strings.TrimSpace(string(versionOut)))
	}
	version := strings.TrimSpace(string(versionOut))
	if !strings.HasPrefix(version, pinnedClaudeVersion) {
		return fmt.Errorf("claude --version = %q, want a version starting with %s", version, pinnedClaudeVersion)
	}

	if err := os.WriteFile(filepath.Join(rootPath, "VERSION"), []byte(version+"\n"), 0o644); err != nil {
		return err
	}
	if err := envWriteSettings(filepath.Join(rootPath, "cfg", "settings.json")); err != nil {
		return err
	}
	if err := envWriteClaudeConfig(rootPath); err != nil {
		return err
	}

	if err := os.MkdirAll(homePath, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(homePath, 0o700); err != nil {
		return err
	}
	configPath := filepath.Join(homePath, "config.yaml")
	if err := os.WriteFile(configPath, []byte("broker:\n  listen: 127.0.0.1:27443\n  alias: hostB\n"), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(configPath, 0o600); err != nil {
		return err
	}

	for _, name := range probeNames {
		projectConfig := filepath.Join(rootPath, "work", name, ".agent-peer.yaml")
		if err := os.WriteFile(projectConfig, []byte("topics: [rs-probe]\n"), 0o644); err != nil {
			return err
		}
	}

	runPath := filepath.Join(rootPath, "run")
	if err := os.WriteFile(runPath, []byte(envRunScript(rootPath, envCurrentUser())), 0o755); err != nil {
		return err
	}
	if err := os.Chmod(runPath, 0o755); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stdout, "initialized %s\n", rootPath)
	return nil
}

// envCopyExecutable copies src to dst with an executable mode.
func envCopyExecutable(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.WriteFile(dst, data, 0o755); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return os.Chmod(dst, 0o755)
}

type envSettings struct {
	SkipDangerousModePermissionPrompt bool              `json:"skipDangerousModePermissionPrompt"`
	Env                               map[string]string `json:"env"`
}

func envWriteSettings(path string) error {
	return envWriteJSON(path, envSettings{
		SkipDangerousModePermissionPrompt: true,
		Env:                               map[string]string{"DISABLE_AUTOUPDATER": "1"},
	})
}

type envClaudeProject struct {
	HasTrustDialogAccepted bool `json:"hasTrustDialogAccepted"`
}

type envClaudeConfig struct {
	HasCompletedOnboarding bool                        `json:"hasCompletedOnboarding"`
	Projects               map[string]envClaudeProject `json:"projects"`
}

func envWriteClaudeConfig(root string) error {
	projects := make(map[string]envClaudeProject, len(probeNames))
	for _, name := range probeNames {
		projects[filepath.Join(root, "work", name)] = envClaudeProject{HasTrustDialogAccepted: true}
	}
	return envWriteJSON(filepath.Join(root, "cfg", ".claude.json"), envClaudeConfig{
		HasCompletedOnboarding: true,
		Projects:               projects,
	})
}

func envWriteJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// envRunScript renders the wrapper that starts every probe process with a
// clean, reproducible environment. The OAuth token is passed by reference from
// the caller's environment and never written into the file.
func envRunScript(root, name string) string {
	return fmt.Sprintf(`#!/bin/sh
exec env -i HOME=%s/home USER=%s LOGNAME=%s PATH=%s/bin:/usr/local/bin:/usr/bin:/bin TERM=xterm-256color LANG=C.UTF-8 CLAUDE_CONFIG_DIR=%s/cfg DISABLE_AUTOUPDATER=1 CLAUDE_CODE_OAUTH_TOKEN="$CLAUDE_CODE_OAUTH_TOKEN" "$@"
`, root, name, name, root, root)
}

// runEnvCheck implements "rsprobe env check --root R". It prints one line per
// item and returns an error when any item failed.
func runEnvCheck(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("env check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", "", "probe root directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *root == "" {
		return errors.New("--root is required")
	}
	rootPath, err := filepath.Abs(*root)
	if err != nil {
		return err
	}

	var failures []string
	fail := func(reason string) {
		failures = append(failures, reason)
	}

	// The token is only ever judged for presence; its value is never printed.
	if os.Getenv("CLAUDE_CODE_OAUTH_TOKEN") != "" {
		_, _ = fmt.Fprintln(stdout, "CLAUDE_CODE_OAUTH_TOKEN: set")
	} else {
		reason := "CLAUDE_CODE_OAUTH_TOKEN is not set"
		_, _ = fmt.Fprintf(stdout, "CLAUDE_CODE_OAUTH_TOKEN: %s\n", reason)
		fail(reason)
	}

	if names, err := envWrappedEnvNames(rootPath); err != nil {
		reason := fmt.Sprintf("cannot run the wrapper: %v", err)
		_, _ = fmt.Fprintf(stdout, "environment: %s\n", reason)
		fail(reason)
	} else {
		var leaked []string
		for _, name := range []string{
			"CLAUDE_CODE_MESSAGING_SOCKET",
			"CLAUDE_CODE_CHILD_SESSION",
			"CLAUDECODE",
			"CLAUDE_PID",
		} {
			if names[name] {
				leaked = append(leaked, name)
			}
		}
		switch {
		case len(leaked) > 0:
			reason := strings.Join(leaked, ", ") + " leaked into the wrapped environment"
			_, _ = fmt.Fprintf(stdout, "environment: %s\n", reason)
			fail(reason)
		case !names["CLAUDE_CODE_OAUTH_TOKEN"]:
			reason := "CLAUDE_CODE_OAUTH_TOKEN is missing from the wrapped environment"
			_, _ = fmt.Fprintf(stdout, "environment: %s\n", reason)
			fail(reason)
		default:
			_, _ = fmt.Fprintln(stdout, "environment: ok")
		}
	}

	gotVersion, gotErr := exec.Command(filepath.Join(rootPath, "bin", "claude"), "--version").Output()
	wantVersion, wantErr := os.ReadFile(filepath.Join(rootPath, "VERSION"))
	switch {
	case gotErr != nil:
		reason := fmt.Sprintf("cannot run claude --version: %v", gotErr)
		_, _ = fmt.Fprintf(stdout, "version: %s\n", reason)
		fail(reason)
	case wantErr != nil:
		reason := fmt.Sprintf("cannot read VERSION: %v", wantErr)
		_, _ = fmt.Fprintf(stdout, "version: %s\n", reason)
		fail(reason)
	default:
		got := strings.TrimSpace(string(gotVersion))
		want := strings.TrimSpace(string(wantVersion))
		if got != want {
			reason := fmt.Sprintf("claude --version is %q, R/VERSION is %q", got, want)
			_, _ = fmt.Fprintf(stdout, "version: %s\n", reason)
			fail(reason)
		} else {
			_, _ = fmt.Fprintln(stdout, "version: ok")
		}
	}

	if ok, detail := envBrokerLockFree(rootPath); ok {
		if detail == "" {
			_, _ = fmt.Fprintln(stdout, "broker lock: ok")
		} else {
			_, _ = fmt.Fprintf(stdout, "broker lock: ok (%s)\n", detail)
		}
	} else {
		_, _ = fmt.Fprintf(stdout, "broker lock: %s\n", detail)
		fail(detail)
	}

	limit := strings.TrimSpace(string(mustReadFile("/proc/sys/fs/inotify/max_user_instances")))
	if limit == "" {
		limit = "unknown"
	}
	_, _ = fmt.Fprintf(stdout, "inotify: max_user_instances=%s in_use=%d\n", limit, envCountInotify())

	if len(failures) > 0 {
		return fmt.Errorf("environment check failed: %s", strings.Join(failures, "; "))
	}
	return nil
}

func mustReadFile(path string) []byte {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return data
}

// envWrappedEnvNames runs R/run /usr/bin/env and returns the set of variable
// names. The output is parsed in memory and never printed.
func envWrappedEnvNames(root string) (map[string]bool, error) {
	out, err := exec.Command(filepath.Join(root, "run"), "/usr/bin/env").Output()
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool)
	for _, line := range strings.Split(string(out), "\n") {
		if i := strings.IndexByte(line, '='); i > 0 {
			names[line[:i]] = true
		}
	}
	return names, nil
}

// envBrokerLockFree tests the account's Broker lock. It returns false with a
// reason when a process other than this probe's own agent-peer holds it.
func envBrokerLockFree(root string) (bool, string) {
	lockPath := filepath.Join(fmt.Sprintf("/tmp/agent-peer-%d", os.Getuid()), "broker.lock")
	f, err := os.OpenFile(lockPath, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return true, ""
	}
	if err != nil {
		return false, fmt.Sprintf("cannot open %s: %v", lockPath, err)
	}
	defer func() { _ = f.Close() }()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return false, fmt.Sprintf("cannot test %s: %v", lockPath, err)
		}
		pids := envLockHolders(lockPath, os.Getpid())
		if len(pids) == 0 {
			return false, "broker.lock is held by an unknown process"
		}
		own := filepath.Join(root, "bin", "agent-peer")
		for _, pid := range pids {
			exe, _ := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
			if exe != own {
				return false, fmt.Sprintf("broker.lock is held by pid %d (%s)", pid, exe)
			}
		}
		return true, fmt.Sprintf("held by pid %d from this environment", pids[0])
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return true, ""
}

// envLockHolders finds the processes that have lockPath open, excluding self.
// The caller opens the lock file to test the lock, so its own descriptor would
// otherwise make the checker look like a holder.
func envLockHolders(lockPath string, self int) []int {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var pids []int
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		fdDir := fmt.Sprintf("/proc/%d/fd", pid)
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err != nil {
				continue
			}
			if target == lockPath || target == lockPath+" (deleted)" {
				pids = append(pids, pid)
				break
			}
		}
	}
	return pids
}

// envCountInotify counts the inotify instances held by this account's
// processes, for the record only.
func envCountInotify() int {
	uid := os.Getuid()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
		if err != nil || !envStatusOwnedBy(status, uid) {
			continue
		}
		fdinfo, err := filepath.Glob(fmt.Sprintf("/proc/%d/fdinfo/*", pid))
		if err != nil {
			continue
		}
		for _, name := range fdinfo {
			data, err := os.ReadFile(name)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "inotify ") {
					count++
					break
				}
			}
		}
	}
	return count
}

func envStatusOwnedBy(status []byte, uid int) bool {
	for _, line := range strings.Split(string(status), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return false
		}
		real, err := strconv.Atoi(fields[1])
		return err == nil && real == uid
	}
	return false
}

type envUsageKey struct {
	file  string
	model string
}

type envUsageRow struct {
	file  string
	model string
	count int
}

// runEnvUsage implements "rsprobe env usage --root R [--since RFC3339]".
func runEnvUsage(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("env usage", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", "", "probe root directory")
	sinceText := fs.String("since", "", "count only records at or after this RFC3339 time")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *root == "" {
		return errors.New("--root is required")
	}
	rootPath, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	var since time.Time
	if *sinceText != "" {
		since, err = time.Parse(time.RFC3339, *sinceText)
		if err != nil {
			return fmt.Errorf("--since: %w", err)
		}
	}

	rows, total, err := envCollectUsage(rootPath, since)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if _, err := fmt.Fprintf(stdout, "model=%s file=%s calls=%d\n", row.model, row.file, row.count); err != nil {
			return err
		}
	}
	_, err = fmt.Fprintf(stdout, "total %d\n", total)
	return err
}

// envCollectUsage counts the distinct assistant message ids in the session
// records, grouped by file and model.
func envCollectUsage(root string, since time.Time) ([]envUsageRow, int, error) {
	projects := filepath.Join(root, "cfg", "projects")
	per := make(map[envUsageKey]map[string]struct{})
	global := make(map[string]struct{})

	type record struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		Message   struct {
			ID    string `json:"id"`
			Model string `json:"model"`
		} `json:"message"`
	}

	err := filepath.WalkDir(projects, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var rec record
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				continue
			}
			if rec.Type != "assistant" || rec.Message.ID == "" {
				continue
			}
			if !since.IsZero() && rec.Timestamp != "" {
				if ts, err := time.Parse(time.RFC3339, rec.Timestamp); err == nil && ts.Before(since) {
					continue
				}
			}
			key := envUsageKey{file: path, model: rec.Message.Model}
			if per[key] == nil {
				per[key] = make(map[string]struct{})
			}
			per[key][rec.Message.ID] = struct{}{}
			global[rec.Message.ID] = struct{}{}
		}
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, 0, err
	}

	rows := make([]envUsageRow, 0, len(per))
	for key, ids := range per {
		rows = append(rows, envUsageRow{file: key.file, model: key.model, count: len(ids)})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].file != rows[j].file {
			return rows[i].file < rows[j].file
		}
		return rows[i].model < rows[j].model
	})
	return rows, len(global), nil
}

// runEnvClean implements "rsprobe env clean --root R [--agent-peer-home H]".
func runEnvClean(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("env clean", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", "", "probe root directory")
	agentPeerHome := fs.String("agent-peer-home", "", "Broker data directory (default: the account's ~/.agent-peer)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *root == "" {
		return errors.New("--root is required")
	}
	rootPath, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	homePath := *agentPeerHome
	if homePath == "" {
		homePath, err = envDefaultAgentPeerHome()
		if err != nil {
			return err
		}
	}
	homePath, err = filepath.Abs(homePath)
	if err != nil {
		return err
	}

	if _, err := exec.LookPath("tmux"); err != nil {
		_, _ = fmt.Fprintln(stdout, "tmux: not found, skipping")
	} else {
		_ = exec.Command("tmux", "-L", "agent-peer-rs", "kill-server").Run()
	}

	envKillProbeProcesses(rootPath, stdout)

	compose := filepath.Join("test", "realsession", "compose.yaml")
	if _, err := os.Stat(compose); err != nil {
		_, _ = fmt.Fprintf(stdout, "docker compose: %s not found, skipping\n", compose)
	} else if _, err := exec.LookPath("docker"); err != nil {
		_, _ = fmt.Fprintln(stdout, "docker: not found, skipping")
	} else {
		cmd := exec.Command("docker", "compose", "-f", compose, "down", "-v", "--timeout", "1")
		// The compose file requires RS_ROOT for its volume mounts, so the
		// environment root is passed along with the rest of the environment.
		cmd.Env = envWithOverride(os.Environ(), "RS_ROOT", rootPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			_, _ = fmt.Fprintf(stdout, "docker compose: %v: %s\n", err, strings.TrimSpace(string(out)))
		}
	}

	if err := os.RemoveAll(homePath); err != nil {
		return fmt.Errorf("remove %s: %w", homePath, err)
	}
	entries, err := os.ReadDir(rootPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "out" {
			continue
		}
		if err := os.RemoveAll(filepath.Join(rootPath, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// envWithOverride returns env with key set to value, replacing any earlier
// entry for the same key.
func envWithOverride(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, prefix) {
			out = append(out, kv)
		}
	}
	return append(out, prefix+value)
}

// envKillProbeProcesses stops every process started from the probe: binaries
// under R/bin and processes whose HOME is R/home.
func envKillProbeProcesses(root string, stdout io.Writer) {
	binPrefix := filepath.Join(root, "bin") + string(os.PathSeparator)
	homeValue := "HOME=" + filepath.Join(root, "home")
	self := os.Getpid()

	var pids []int
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		match := false
		if exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid)); err == nil && strings.HasPrefix(exe, binPrefix) {
			match = true
		}
		if !match {
			if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid)); err == nil {
				for _, kv := range strings.Split(string(data), "\x00") {
					if kv == homeValue {
						match = true
						break
					}
				}
			}
		}
		if match {
			pids = append(pids, pid)
		}
	}
	if len(pids) == 0 {
		return
	}
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !envAnyAlive(pids) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for _, pid := range pids {
		if syscall.Kill(pid, 0) == nil {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
	_, _ = fmt.Fprintf(stdout, "stopped %d probe process(es)\n", len(pids))
}

func envAnyAlive(pids []int) bool {
	for _, pid := range pids {
		if syscall.Kill(pid, 0) == nil {
			return true
		}
	}
	return false
}
