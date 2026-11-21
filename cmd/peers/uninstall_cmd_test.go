package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wangzw/peers/internal/broker/service"
)

// uninstallSeams holds the replacements for one test. A nil field keeps the
// production value.
type uninstallSeams struct {
	options         func(bool) (service.Options, error)
	installed       func(service.Options) (bool, error)
	uninstallSvc    func(service.Options) (service.Result, error)
	serviceBinary   func(service.Options) (string, bool, error)
	removeRuntime   func(service.Options) ([]string, error)
	purgeData       func(service.Options) ([]string, error)
	otherAccounts   func(service.Options) ([]string, error)
	removeMCP       func(string, string) (string, error)
	mcpLookup       func(string) mcpRegistration
	executable      func() (string, error)
	evalSymlinks    func(string) (string, error)
	geteuid         func() int
	getenv          func(string) string
	lookPath        func(string) (string, error)
	ask             func(string) (bool, bool)
	sudo            func(context.Context, string, []string, io.Reader, io.Writer, io.Writer) (int, error)
	fileOwner       func(string) (uint32, error)
	removeFile      func(string) error
	systemInstalled func(service.Options) bool
}

func withUninstallSeams(t *testing.T, s uninstallSeams) {
	t.Helper()
	previous := uninstallSeams{
		options:         uninstallOptionsFn,
		installed:       uninstallInstalledFn,
		uninstallSvc:    uninstallServiceFn,
		serviceBinary:   uninstallServiceBinaryFn,
		removeRuntime:   uninstallRemoveRuntimeDirFn,
		purgeData:       uninstallPurgeDataFn,
		otherAccounts:   uninstallOtherAccountsFn,
		removeMCP:       uninstallMCPFn,
		mcpLookup:       uninstallMCPLookupFn,
		executable:      uninstallExecutableFn,
		evalSymlinks:    uninstallEvalSymlinksFn,
		geteuid:         uninstallGeteuidFn,
		getenv:          uninstallGetenvFn,
		lookPath:        uninstallLookPathFn,
		ask:             uninstallAskFn,
		sudo:            uninstallSudoFn,
		fileOwner:       uninstallFileOwnerFn,
		removeFile:      uninstallRemoveFileFn,
		systemInstalled: uninstallSystemInstalledCheck,
	}
	t.Cleanup(func() {
		uninstallOptionsFn = previous.options
		uninstallInstalledFn = previous.installed
		uninstallServiceFn = previous.uninstallSvc
		uninstallServiceBinaryFn = previous.serviceBinary
		uninstallRemoveRuntimeDirFn = previous.removeRuntime
		uninstallPurgeDataFn = previous.purgeData
		uninstallOtherAccountsFn = previous.otherAccounts
		uninstallMCPFn = previous.removeMCP
		uninstallMCPLookupFn = previous.mcpLookup
		uninstallExecutableFn = previous.executable
		uninstallEvalSymlinksFn = previous.evalSymlinks
		uninstallGeteuidFn = previous.geteuid
		uninstallGetenvFn = previous.getenv
		uninstallLookPathFn = previous.lookPath
		uninstallAskFn = previous.ask
		uninstallSudoFn = previous.sudo
		uninstallFileOwnerFn = previous.fileOwner
		uninstallRemoveFileFn = previous.removeFile
		uninstallSystemInstalledCheck = previous.systemInstalled
	})

	if s.options != nil {
		uninstallOptionsFn = s.options
	}
	if s.installed != nil {
		uninstallInstalledFn = s.installed
	}
	if s.uninstallSvc != nil {
		uninstallServiceFn = s.uninstallSvc
	}
	if s.serviceBinary != nil {
		uninstallServiceBinaryFn = s.serviceBinary
	}
	if s.removeRuntime != nil {
		uninstallRemoveRuntimeDirFn = s.removeRuntime
	}
	if s.purgeData != nil {
		uninstallPurgeDataFn = s.purgeData
	}
	if s.otherAccounts != nil {
		uninstallOtherAccountsFn = s.otherAccounts
	}
	if s.removeMCP != nil {
		uninstallMCPFn = s.removeMCP
	}
	if s.mcpLookup != nil {
		uninstallMCPLookupFn = s.mcpLookup
	}
	if s.executable != nil {
		uninstallExecutableFn = s.executable
	}
	if s.evalSymlinks != nil {
		uninstallEvalSymlinksFn = s.evalSymlinks
	}
	if s.geteuid != nil {
		uninstallGeteuidFn = s.geteuid
	}
	if s.getenv != nil {
		uninstallGetenvFn = s.getenv
	}
	if s.lookPath != nil {
		uninstallLookPathFn = s.lookPath
	}
	if s.ask != nil {
		uninstallAskFn = s.ask
	}
	if s.sudo != nil {
		uninstallSudoFn = s.sudo
	}
	if s.fileOwner != nil {
		uninstallFileOwnerFn = s.fileOwner
	}
	if s.removeFile != nil {
		uninstallRemoveFileFn = s.removeFile
	}
	if s.systemInstalled != nil {
		uninstallSystemInstalledCheck = s.systemInstalled
	}
}

// uninstallRecorder records the steps a test observed.
type uninstallRecorder struct {
	steps     []string
	mcpFor    string
	mcpBinary string
	sudoExe   string
	sudoArgs  []string
}

// askQueue answers prompts in order; an exhausted queue reports no terminal.
type askQueue struct {
	answers []bool
	oks     []bool
	prompts []string
	next    int
}

func (q *askQueue) ask(prompt string) (bool, bool) {
	q.prompts = append(q.prompts, prompt)
	if q.next >= len(q.answers) {
		return false, false
	}
	yes, ok := q.answers[q.next], q.oks[q.next]
	q.next++
	return yes, ok
}

// uninstallOptions returns test options for either level.
func uninstallOptions(root string, system bool, euid int) service.Options {
	return service.Options{
		System: system,
		GOOS:   "linux",
		EUID:   euid,
		UID:    euid,
		Home:   "/home/alice",
		Exe:    "/usr/local/bin/peers",
		Root:   root,
	}
}

func TestUninstallUsageErrors(t *testing.T) {
	cases := [][]string{
		{"uninstall", "--keep-data"},
		{"uninstall", "extra"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			a, _, stderr, _ := newDispatchApp("dev")
			if got := a.run(context.Background(), args); got != exitUsage {
				t.Fatalf("run(%q) = %d, want %d", args, got, exitUsage)
			}
			if !strings.Contains(stderr.String(), uninstallUsage) {
				t.Errorf("stderr = %q, want the usage", stderr.String())
			}
		})
	}
}

func TestUninstallHelp(t *testing.T) {
	a, stdout, _, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall", "-h"}); got != exitOK {
		t.Fatalf("run(uninstall -h) = %d, want %d", got, exitOK)
	}
	if stdout.String() != uninstallUsage {
		t.Errorf("stdout = %q, want %q", stdout.String(), uninstallUsage)
	}
}

// Recorded root uninstall: the system-level steps run and the data question is
// asked once.
func TestUninstallRootSystemLevel(t *testing.T) {
	root := t.TempDir()
	binary := "/usr/local/libexec/peers"
	binPath := filepath.Join(root, binary)
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	rec := &uninstallRecorder{}
	ask := &askQueue{answers: []bool{true}, oks: []bool{true}}
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(root, system, 0), nil
		},
		installed: func(opts service.Options) (bool, error) { return opts.System, nil },
		serviceBinary: func(service.Options) (string, bool, error) {
			return binary, true, nil
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			rec.steps = append(rec.steps, "service")
			return service.Result{UnitPath: "/etc/systemd/system/peers.service", Removed: true}, nil
		},
		removeMCP: func(account, binary string) (string, error) {
			rec.steps = append(rec.steps, "mcp")
			rec.mcpFor, rec.mcpBinary = account, binary
			return "removed the peer MCP registration for bob", nil
		},
		mcpLookup: func(string) mcpRegistration { return mcpRegistration{} },
		removeRuntime: func(service.Options) ([]string, error) {
			rec.steps = append(rec.steps, "runtime")
			return []string{"/run/peers"}, nil
		},
		purgeData: func(service.Options) ([]string, error) {
			rec.steps = append(rec.steps, "purge")
			return []string{"/etc/peers", "group peers"}, nil
		},
		otherAccounts: func(service.Options) ([]string, error) {
			rec.steps = append(rec.steps, "others")
			return []string{"other"}, nil
		},
		getenv: func(key string) string {
			if key == "SUDO_USER" {
				return "bob"
			}
			return ""
		},
		ask:          ask.ask,
		fileOwner:    func(string) (uint32, error) { return 0, nil },
		removeFile:   func(path string) error { rec.steps = append(rec.steps, "binary"); return os.Remove(path) },
		executable:   func() (string, error) { return binPath, nil },
		evalSymlinks: func(path string) (string, error) { return path, nil },
		geteuid:      func() int { return 0 },
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall"}); got != exitOK {
		t.Fatalf("run(uninstall) = %d, want %d\nstdout: %s\nstderr: %s", got, exitOK, stdout, stderr)
	}
	if want := []string{"service", "mcp", "runtime", "purge", "others", "binary"}; !slices.Equal(rec.steps, want) {
		t.Errorf("steps = %q, want %q", rec.steps, want)
	}
	if rec.mcpFor != "bob" || rec.mcpBinary != binary {
		t.Errorf("MCP registration %q of %q, want bob of %s", rec.mcpFor, rec.mcpBinary, binary)
	}
	if len(ask.prompts) != 1 || ask.prompts[0] != uninstallDataPrompt {
		t.Errorf("prompts = %q, want one data question", ask.prompts)
	}
	for _, want := range []string{
		"stopped and removed the service /etc/systemd/system/peers.service",
		"other accounts run a user-level Broker: other",
		"removed the binary " + binary,
		"each account that registered the peer MCP server",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout does not contain %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(binPath); !os.IsNotExist(err) {
		t.Errorf("binary still exists: %v", err)
	}
}

func TestUninstallRootPurgeSkipsDataQuestion(t *testing.T) {
	root := t.TempDir()
	ask := &askQueue{answers: []bool{true}, oks: []bool{true}}
	purged := false
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(root, system, 0), nil
		},
		installed: func(opts service.Options) (bool, error) { return opts.System, nil },
		serviceBinary: func(service.Options) (string, bool, error) {
			return "/usr/local/bin/peers", true, nil
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			return service.Result{UnitPath: "/etc/systemd/system/peers.service", Removed: true}, nil
		},
		removeMCP: func(string, string) (string, error) { return "removed", nil },
		mcpLookup: func(string) mcpRegistration { return mcpRegistration{} },
		removeRuntime: func(service.Options) ([]string, error) {
			return []string{"/run/peers"}, nil
		},
		purgeData: func(service.Options) ([]string, error) {
			purged = true
			return []string{"/etc/peers"}, nil
		},
		otherAccounts: func(service.Options) ([]string, error) { return nil, nil },
		getenv:        func(string) string { return "" },
		ask:           ask.ask,
		fileOwner:     func(string) (uint32, error) { return 0, nil },
		removeFile:    func(string) error { return nil },
		executable:    func() (string, error) { return "/usr/local/bin/peers", nil },
		evalSymlinks:  func(path string) (string, error) { return path, nil },
		geteuid:       func() int { return 0 },
	})

	a, _, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall", "--purge"}); got != exitOK {
		t.Fatalf("run(uninstall --purge) = %d, want %d (stderr %s)", got, exitOK, stderr)
	}
	if len(ask.prompts) != 0 {
		t.Errorf("prompts = %q, want none with --purge", ask.prompts)
	}
	if !purged {
		t.Error("PurgeData was not called with --purge")
	}
}

func TestUninstallRootNotInstalled(t *testing.T) {
	rec := &uninstallRecorder{}
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(t.TempDir(), system, 0), nil
		},
		installed:    func(service.Options) (bool, error) { return false, nil },
		executable:   func() (string, error) { return "/usr/local/bin/peers", nil },
		evalSymlinks: func(path string) (string, error) { return path, nil },
		mcpLookup:    func(string) mcpRegistration { return mcpRegistration{} },
		uninstallSvc: func(service.Options) (service.Result, error) {
			rec.steps = append(rec.steps, "service")
			return service.Result{}, nil
		},
		geteuid: func() int { return 0 },
	})

	a, stdout, _, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall"}); got != exitOK {
		t.Fatalf("run(uninstall) = %d, want %d", got, exitOK)
	}
	if !strings.Contains(stdout.String(), "not installed") {
		t.Errorf("stdout = %q, want it to report no installation", stdout)
	}
	if len(rec.steps) != 0 {
		t.Errorf("steps = %q, want none", rec.steps)
	}
}

func TestUninstallRootNoServiceInstallation(t *testing.T) {
	root := t.TempDir()
	resolved := "/usr/local/bin/peers"
	if err := os.MkdirAll(filepath.Join(root, filepath.Dir(resolved)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, resolved), []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := &uninstallRecorder{}
	ask := &askQueue{answers: []bool{false}, oks: []bool{true}}
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(root, system, 0), nil
		},
		installed: func(service.Options) (bool, error) { return false, nil },
		mcpLookup: func(account string) mcpRegistration {
			if account == "bob" {
				return mcpRegistration{command: resolved}
			}
			return mcpRegistration{}
		},
		serviceBinary: func(service.Options) (string, bool, error) {
			return "", false, nil
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			rec.steps = append(rec.steps, "service")
			return service.Result{}, nil
		},
		removeMCP: func(account, binary string) (string, error) {
			rec.steps = append(rec.steps, "mcp")
			rec.mcpFor, rec.mcpBinary = account, binary
			return "removed", nil
		},
		removeRuntime: func(service.Options) ([]string, error) { return nil, nil },
		purgeData:     func(service.Options) ([]string, error) { return nil, nil },
		otherAccounts: func(service.Options) ([]string, error) { return nil, nil },
		getenv: func(key string) string {
			if key == "SUDO_USER" {
				return "bob"
			}
			return ""
		},
		ask:          ask.ask,
		executable:   func() (string, error) { return filepath.Join(root, resolved), nil },
		evalSymlinks: func(string) (string, error) { return resolved, nil },
		geteuid:      func() int { return 0 },
		fileOwner:    func(string) (uint32, error) { return 0, nil },
		removeFile:   func(path string) error { rec.steps = append(rec.steps, "binary"); return os.Remove(path) },
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall"}); got != exitOK {
		t.Fatalf("run(uninstall) = %d, want %d\nstdout: %s\nstderr: %s", got, exitOK, stdout, stderr)
	}
	if rec.mcpBinary != resolved || rec.mcpFor != "bob" {
		t.Errorf("MCP registration %q of %q, want bob of %s", rec.mcpFor, rec.mcpBinary, resolved)
	}
	if !slices.Contains(rec.steps, "binary") {
		t.Errorf("steps = %q, want the no-service binary removed", rec.steps)
	}
	if _, err := os.Stat(filepath.Join(root, resolved)); !os.IsNotExist(err) {
		t.Errorf("binary still exists: %v", err)
	}
}

func TestUninstallAccountNoServiceAtDefaultPrefix(t *testing.T) {
	root := t.TempDir()
	exe := "/home/alice/.local/bin/peers"
	full := filepath.Join(root, exe)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := &uninstallRecorder{}
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(root, system, 1000), nil
		},
		installed: func(service.Options) (bool, error) { return false, nil },
		mcpLookup: func(account string) mcpRegistration {
			if account == "" {
				return mcpRegistration{command: exe}
			}
			return mcpRegistration{}
		},
		serviceBinary: func(service.Options) (string, bool, error) {
			return "", false, nil
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			rec.steps = append(rec.steps, "service")
			return service.Result{}, nil
		},
		removeMCP: func(account, binary string) (string, error) {
			rec.steps = append(rec.steps, "mcp")
			rec.mcpFor, rec.mcpBinary = account, binary
			return "removed", nil
		},
		removeRuntime: func(service.Options) ([]string, error) { return nil, nil },
		purgeData:     func(service.Options) ([]string, error) { return nil, nil },
		otherAccounts: func(service.Options) ([]string, error) { return nil, nil },
		ask:           func(string) (bool, bool) { return false, true },
		fileOwner:     func(string) (uint32, error) { return 1000, nil },
		removeFile:    func(path string) error { rec.steps = append(rec.steps, "binary"); return os.Remove(path) },
		executable:    func() (string, error) { return full, nil },
		evalSymlinks:  func(path string) (string, error) { return path, nil },
		geteuid:       func() int { return 1000 },
		systemInstalled: func(service.Options) bool {
			return false
		},
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall"}); got != exitOK {
		t.Fatalf("run(uninstall) = %d, want %d\nstdout: %s\nstderr: %s", got, exitOK, stdout, stderr)
	}
	if rec.mcpFor != "" || rec.mcpBinary != exe {
		t.Errorf("MCP registration %q of %q, want the running account of %s", rec.mcpFor, rec.mcpBinary, exe)
	}
	if !slices.Contains(rec.steps, "binary") {
		t.Errorf("steps = %q, want the no-service binary removed", rec.steps)
	}
	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Errorf("binary still exists: %v", err)
	}
}

func TestUninstallNoServiceOutsideTheDefaultPrefix(t *testing.T) {
	root := t.TempDir()
	exe := "/home/alice/downloads/peers"
	rec := &uninstallRecorder{}
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(root, system, 1000), nil
		},
		installed: func(service.Options) (bool, error) { return false, nil },
		mcpLookup: func(string) mcpRegistration {
			return mcpRegistration{command: exe}
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			rec.steps = append(rec.steps, "service")
			return service.Result{}, nil
		},
		removeMCP: func(string, string) (string, error) {
			rec.steps = append(rec.steps, "mcp")
			return "removed", nil
		},
		removeRuntime: func(service.Options) ([]string, error) {
			rec.steps = append(rec.steps, "runtime")
			return nil, nil
		},
		executable: func() (string, error) { return filepath.Join(root, exe), nil },
		evalSymlinks: func(path string) (string, error) {
			return path, nil
		},
		geteuid: func() int { return 1000 },
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall"}); got != exitOK {
		t.Fatalf("run(uninstall) = %d, want %d\nstdout: %s\nstderr: %s", got, exitOK, stdout, stderr)
	}
	if !strings.Contains(stdout.String(), "not installed") {
		t.Errorf("stdout = %q, want it to report no installation", stdout)
	}
	if len(rec.steps) != 0 {
		t.Errorf("steps = %q, want a downloaded copy outside the prefix left alone", rec.steps)
	}
}

func TestUninstallKeepsADownloadedCopyWhenNoUnitFileExists(t *testing.T) {
	root := t.TempDir()
	exe := "/home/alice/downloads/peers"
	full := filepath.Join(root, exe)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := &uninstallRecorder{}
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(root, system, 0), nil
		},
		installed: func(opts service.Options) (bool, error) { return opts.System, nil },
		serviceBinary: func(service.Options) (string, bool, error) {
			return "", false, nil
		},
		mcpLookup: func(string) mcpRegistration {
			return mcpRegistration{command: exe}
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			rec.steps = append(rec.steps, "service")
			return service.Result{UnitPath: "/etc/systemd/system/peers.service", Removed: true}, nil
		},
		removeMCP: func(string, string) (string, error) {
			rec.steps = append(rec.steps, "mcp")
			return "removed", nil
		},
		removeRuntime: func(service.Options) ([]string, error) {
			rec.steps = append(rec.steps, "runtime")
			return nil, nil
		},
		purgeData:     func(service.Options) ([]string, error) { return nil, nil },
		otherAccounts: func(service.Options) ([]string, error) { return nil, nil },
		getenv: func(key string) string {
			if key == "SUDO_USER" {
				return "bob"
			}
			return ""
		},
		ask:          func(string) (bool, bool) { return false, true },
		fileOwner:    func(string) (uint32, error) { return 0, nil },
		removeFile:   func(string) error { t.Error("removed a downloaded copy"); return nil },
		executable:   func() (string, error) { return full, nil },
		evalSymlinks: func(path string) (string, error) { return path, nil },
		geteuid:      func() int { return 0 },
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall"}); got != exitOK {
		t.Fatalf("run(uninstall) = %d, want %d\nstdout: %s\nstderr: %s", got, exitOK, stdout, stderr)
	}
	if want := []string{"service", "runtime"}; !slices.Equal(rec.steps, want) {
		t.Errorf("steps = %q, want %q", rec.steps, want)
	}
	if !strings.Contains(stdout.String(), "kept the running executable "+full) {
		t.Errorf("stdout = %q, want it to say the executable was kept", stdout)
	}
	if _, err := os.Stat(full); err != nil {
		t.Errorf("downloaded copy was removed: %v", err)
	}
}

func TestUninstallAccountUserLevelKeepsData(t *testing.T) {
	root := t.TempDir()
	binary := "/home/alice/bin/peers"
	binPath := filepath.Join(root, binary)
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	rec := &uninstallRecorder{}
	ask := &askQueue{answers: []bool{false}, oks: []bool{true}}
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(root, system, 1000), nil
		},
		installed: func(opts service.Options) (bool, error) { return !opts.System, nil },
		serviceBinary: func(service.Options) (string, bool, error) {
			return binary, true, nil
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			rec.steps = append(rec.steps, "service")
			return service.Result{UnitPath: "/home/alice/.config/systemd/user/peers.service", Removed: true}, nil
		},
		removeMCP: func(account, binary string) (string, error) {
			rec.steps = append(rec.steps, "mcp")
			if account != "" {
				t.Errorf("account = %q, want the running account", account)
			}
			return "removed the peer MCP registration", nil
		},
		mcpLookup: func(string) mcpRegistration { return mcpRegistration{} },
		removeRuntime: func(service.Options) ([]string, error) {
			rec.steps = append(rec.steps, "runtime")
			return []string{"/tmp/peers-1000"}, nil
		},
		purgeData: func(service.Options) ([]string, error) {
			rec.steps = append(rec.steps, "purge")
			return nil, nil
		},
		otherAccounts: func(service.Options) ([]string, error) {
			rec.steps = append(rec.steps, "others")
			return nil, nil
		},
		ask:             ask.ask,
		fileOwner:       func(string) (uint32, error) { return 1000, nil },
		removeFile:      func(path string) error { rec.steps = append(rec.steps, "binary"); return os.Remove(path) },
		executable:      func() (string, error) { return filepath.Join(root, binary), nil },
		evalSymlinks:    func(path string) (string, error) { return path, nil },
		geteuid:         func() int { return 1000 },
		systemInstalled: func(service.Options) bool { return false },
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall"}); got != exitOK {
		t.Fatalf("run(uninstall) = %d, want %d\nstdout: %s\nstderr: %s", got, exitOK, stdout, stderr)
	}
	if want := []string{"service", "mcp", "runtime", "binary"}; !slices.Equal(rec.steps, want) {
		t.Errorf("steps = %q, want %q", rec.steps, want)
	}
	if !strings.Contains(stdout.String(), "kept configuration, data and logs: /home/alice/.peers") {
		t.Errorf("stdout = %q, want the kept paths", stdout)
	}
	if len(ask.prompts) != 1 || ask.prompts[0] != uninstallDataPrompt {
		t.Errorf("prompts = %q, want one data question", ask.prompts)
	}
}

func TestUninstallAccountHintsAtSystemLevel(t *testing.T) {
	root := t.TempDir()
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(root, system, 1000), nil
		},
		installed: func(opts service.Options) (bool, error) { return true, nil },
		serviceBinary: func(service.Options) (string, bool, error) {
			return "/home/alice/bin/peers", true, nil
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			return service.Result{UnitPath: "/home/alice/.config/systemd/user/peers.service", Removed: true}, nil
		},
		removeMCP:       func(string, string) (string, error) { return "removed", nil },
		mcpLookup:       func(string) mcpRegistration { return mcpRegistration{} },
		removeRuntime:   func(service.Options) ([]string, error) { return nil, nil },
		purgeData:       func(service.Options) ([]string, error) { return nil, nil },
		otherAccounts:   func(service.Options) ([]string, error) { return nil, nil },
		ask:             func(string) (bool, bool) { return false, true },
		fileOwner:       func(string) (uint32, error) { return 1000, nil },
		removeFile:      func(string) error { return nil },
		executable:      func() (string, error) { return "/usr/local/bin/peers", nil },
		evalSymlinks:    func(path string) (string, error) { return path, nil },
		geteuid:         func() int { return 1000 },
		systemInstalled: func(service.Options) bool { return true },
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall"}); got != exitOK {
		t.Fatalf("run(uninstall) = %d, want %d (stderr %s)", got, exitOK, stderr)
	}
	if !strings.Contains(stdout.String(), "sudo peers uninstall") {
		t.Errorf("stdout = %q, want the sudo hint", stdout)
	}
}

func TestUninstallAccountOnlySystemRefused(t *testing.T) {
	cases := []struct {
		name  string
		ask   func(string) (bool, bool)
		purge bool
	}{
		{"answer no", func(string) (bool, bool) { return false, true }, false},
		{"no terminal", func(string) (bool, bool) { return false, false }, false},
		{"no terminal with purge", func(string) (bool, bool) { return false, false }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &uninstallRecorder{}
			withUninstallSeams(t, uninstallSeams{
				options: func(system bool) (service.Options, error) {
					return uninstallOptions(t.TempDir(), system, 1000), nil
				},
				installed: func(opts service.Options) (bool, error) { return opts.System, nil },
				mcpLookup: func(string) mcpRegistration { return mcpRegistration{} },
				uninstallSvc: func(service.Options) (service.Result, error) {
					rec.steps = append(rec.steps, "service")
					return service.Result{}, nil
				},
				ask:     tc.ask,
				geteuid: func() int { return 1000 },
			})

			args := []string{"uninstall"}
			if tc.purge {
				args = append(args, "--purge")
			}
			a, stdout, stderr, _ := newDispatchApp("dev")
			if got := a.run(context.Background(), args); got != exitOK {
				t.Fatalf("run(%q) = %d, want %d (stderr %s)", args, got, exitOK, stderr)
			}
			if len(rec.steps) != 0 {
				t.Errorf("steps = %q, want nothing changed", rec.steps)
			}
			want := "Run: sudo peers uninstall"
			if tc.purge {
				want += " --purge"
			}
			if !strings.Contains(stdout.String(), want) {
				t.Errorf("stdout = %q, want %q", stdout, want)
			}
		})
	}
}

func TestUninstallAccountOnlySystemRunsSudo(t *testing.T) {
	cases := []struct {
		name     string
		purge    bool
		wantArgs []string
		code     int
	}{
		{"without purge", false, []string{"uninstall"}, 0},
		{"with purge", true, []string{"uninstall", "--purge"}, 0},
		{"child failure", false, []string{"uninstall"}, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &uninstallRecorder{}
			withUninstallSeams(t, uninstallSeams{
				options: func(system bool) (service.Options, error) {
					return uninstallOptions(t.TempDir(), system, 1000), nil
				},
				installed: func(opts service.Options) (bool, error) { return opts.System, nil },
				mcpLookup: func(string) mcpRegistration { return mcpRegistration{} },
				ask:       func(string) (bool, bool) { return true, true },
				geteuid:   func() int { return 1000 },
				lookPath:  func(string) (string, error) { return "/usr/bin/sudo", nil },
				executable: func() (string, error) {
					return "/usr/local/bin/peers", nil
				},
				sudo: func(_ context.Context, exe string, args []string, _ io.Reader, _ io.Writer, _ io.Writer) (int, error) {
					rec.sudoExe, rec.sudoArgs = exe, args
					return tc.code, nil
				},
			})

			args := []string{"uninstall"}
			if tc.purge {
				args = append(args, "--purge")
			}
			a, _, stderr, _ := newDispatchApp("dev")
			if got := a.run(context.Background(), args); got != tc.code {
				t.Fatalf("run(%q) = %d, want the child's %d (stderr %s)", args, got, tc.code, stderr)
			}
			if rec.sudoExe != "/usr/local/bin/peers" {
				t.Errorf("sudo exe = %q", rec.sudoExe)
			}
			if !slices.Equal(rec.sudoArgs, tc.wantArgs) {
				t.Errorf("sudo args = %q, want %q", rec.sudoArgs, tc.wantArgs)
			}
		})
	}
}

func TestUninstallAccountOnlySystemWithoutSudo(t *testing.T) {
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(t.TempDir(), system, 1000), nil
		},
		installed: func(opts service.Options) (bool, error) { return opts.System, nil },
		mcpLookup: func(string) mcpRegistration { return mcpRegistration{} },
		ask:       func(string) (bool, bool) { return true, true },
		geteuid:   func() int { return 1000 },
		lookPath:  func(string) (string, error) { return "", os.ErrNotExist },
		executable: func() (string, error) {
			return "/usr/local/bin/peers", nil
		},
	})

	a, _, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall"}); got != exitFailure {
		t.Fatalf("run(uninstall) = %d, want %d", got, exitFailure)
	}
	if !strings.Contains(stderr.String(), "run sudo") {
		t.Errorf("stderr = %q, want the sudo step named", stderr)
	}
}

func TestUninstallStopsAfterAFailedStep(t *testing.T) {
	rec := &uninstallRecorder{}
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(t.TempDir(), system, 1000), nil
		},
		installed: func(opts service.Options) (bool, error) { return !opts.System, nil },
		serviceBinary: func(service.Options) (string, bool, error) {
			return "/home/alice/bin/peers", true, nil
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			return service.Result{}, os.ErrPermission
		},
		removeRuntime: func(service.Options) ([]string, error) {
			rec.steps = append(rec.steps, "runtime")
			return nil, nil
		},
		purgeData: func(service.Options) ([]string, error) {
			rec.steps = append(rec.steps, "purge")
			return nil, nil
		},
		mcpLookup: func(string) mcpRegistration { return mcpRegistration{} },
		ask:       func(string) (bool, bool) { return true, true },
		geteuid:   func() int { return 1000 },
	})

	a, _, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall"}); got != exitFailure {
		t.Fatalf("run(uninstall) = %d, want %d", got, exitFailure)
	}
	if !strings.Contains(stderr.String(), "stop and remove the service") {
		t.Errorf("stderr = %q, want the failed step named", stderr)
	}
	if len(rec.steps) != 0 {
		t.Errorf("later steps ran after the failure: %q", rec.steps)
	}
}

func TestUninstallKeepsABinaryOfAnotherAccount(t *testing.T) {
	root := t.TempDir()
	binary := "/usr/local/bin/peers"
	full := filepath.Join(root, binary)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(root, system, 1000), nil
		},
		installed: func(opts service.Options) (bool, error) { return !opts.System, nil },
		serviceBinary: func(service.Options) (string, bool, error) {
			return binary, true, nil
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			return service.Result{UnitPath: "/home/alice/.config/systemd/user/peers.service", Removed: true}, nil
		},
		removeMCP:     func(string, string) (string, error) { return "removed", nil },
		mcpLookup:     func(string) mcpRegistration { return mcpRegistration{} },
		removeRuntime: func(service.Options) ([]string, error) { return nil, nil },
		purgeData:     func(service.Options) ([]string, error) { return nil, nil },
		otherAccounts: func(service.Options) ([]string, error) { return nil, nil },
		ask:           func(string) (bool, bool) { return true, true },
		fileOwner:     func(string) (uint32, error) { return 0, nil },
		removeFile: func(string) error {
			t.Error("removed a binary that belongs to another account")
			return nil
		},
		executable:   func() (string, error) { return full, nil },
		evalSymlinks: func(path string) (string, error) { return path, nil },
		geteuid:      func() int { return 1000 },
		systemInstalled: func(service.Options) bool {
			return false
		},
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall"}); got != exitOK {
		t.Fatalf("run(uninstall) = %d, want %d (stderr %s)", got, exitOK, stderr)
	}
	if !strings.Contains(stdout.String(), "belongs to another account") {
		t.Errorf("stdout = %q, want the reason the binary was kept", stdout)
	}
}

func TestUninstallReportsPathsRemovedBeforeAPurgeFailure(t *testing.T) {
	root := t.TempDir()
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(root, system, 1000), nil
		},
		installed: func(opts service.Options) (bool, error) { return !opts.System, nil },
		serviceBinary: func(service.Options) (string, bool, error) {
			return "/home/alice/bin/peers", true, nil
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			return service.Result{UnitPath: "/home/alice/.config/systemd/user/peers.service", Removed: true}, nil
		},
		removeMCP:     func(string, string) (string, error) { return "removed", nil },
		mcpLookup:     func(string) mcpRegistration { return mcpRegistration{} },
		removeRuntime: func(service.Options) ([]string, error) { return nil, nil },
		purgeData: func(service.Options) ([]string, error) {
			return []string{
				"/home/alice/.peers",
				"kept group ops: the installation did not record creating it; remove it manually with: groupdel ops",
			}, errors.New("disk on fire")
		},
		otherAccounts:   func(service.Options) ([]string, error) { return nil, nil },
		ask:             func(string) (bool, bool) { return true, true },
		fileOwner:       func(string) (uint32, error) { return 1000, nil },
		removeFile:      func(string) error { return nil },
		executable:      func() (string, error) { return "/usr/local/bin/peers", nil },
		evalSymlinks:    func(path string) (string, error) { return path, nil },
		geteuid:         func() int { return 1000 },
		systemInstalled: func(service.Options) bool { return false },
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall", "--purge"}); got != exitFailure {
		t.Fatalf("run(uninstall --purge) = %d, want %d", got, exitFailure)
	}
	if !strings.Contains(stdout.String(), "removed /home/alice/.peers") {
		t.Errorf("stdout does not list the removed path before the failure:\n%s", stdout)
	}
	if !strings.Contains(stdout.String(), "kept group ops:") {
		t.Errorf("stdout does not print the kept group as it is:\n%s", stdout)
	}
	if strings.Contains(stdout.String(), "removed kept group") {
		t.Errorf("stdout prefixes a kept-group message with removed:\n%s", stdout)
	}
	if !strings.Contains(stderr.String(), "remove the data") {
		t.Errorf("stderr = %q, want the failed step named", stderr)
	}
}

func TestUninstallRejectsRelativeHomeBeforeAnyStep(t *testing.T) {
	rec := &uninstallRecorder{}
	asked := false
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			opts := uninstallOptions(t.TempDir(), system, 1000)
			opts.Home = "alice"
			return opts, nil
		},
		installed: func(service.Options) (bool, error) { return true, nil },
		serviceBinary: func(service.Options) (string, bool, error) {
			return "/home/alice/.local/bin/peers", true, nil
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			rec.steps = append(rec.steps, "service")
			return service.Result{UnitPath: "/home/alice/.config/systemd/user/peers.service", Removed: true}, nil
		},
		removeMCP: func(string, string) (string, error) {
			rec.steps = append(rec.steps, "mcp")
			return "removed", nil
		},
		mcpLookup: func(string) mcpRegistration { return mcpRegistration{} },
		removeRuntime: func(service.Options) ([]string, error) {
			rec.steps = append(rec.steps, "runtime")
			return nil, nil
		},
		purgeData: func(service.Options) ([]string, error) {
			rec.steps = append(rec.steps, "purge")
			return nil, nil
		},
		otherAccounts: func(service.Options) ([]string, error) {
			rec.steps = append(rec.steps, "others")
			return nil, nil
		},
		ask:     func(string) (bool, bool) { asked = true; return true, true },
		geteuid: func() int { return 1000 },
		systemInstalled: func(service.Options) bool {
			return false
		},
	})

	a, _, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall", "--purge"}); got != exitFailure {
		t.Errorf("run(uninstall --purge) = %d, want %d", got, exitFailure)
	}
	if !strings.Contains(stderr.String(), "home directory") {
		t.Errorf("stderr = %q, want it to name the home directory", stderr)
	}
	if len(rec.steps) != 0 {
		t.Errorf("steps = %q, want nothing changed before rejecting the home directory", rec.steps)
	}
	if asked {
		t.Error("asked a question before rejecting the home directory")
	}
}

func TestYesAnswer(t *testing.T) {
	for _, answer := range []string{"y", "Y", "yes", "YES", " yes \n"} {
		if !yesAnswer(answer) {
			t.Errorf("yesAnswer(%q) = false, want true", answer)
		}
	}
	for _, answer := range []string{"", "n", "no", "yess", "1"} {
		if yesAnswer(answer) {
			t.Errorf("yesAnswer(%q) = true, want false", answer)
		}
	}
}

// runBinaryDeletion runs a system-level uninstall whose service definition
// names binary and whose executable seam returns exe. It reports whether the
// binary was removed and the command's output.
func runBinaryDeletion(t *testing.T, binary, exe string) (removed bool, output string) {
	t.Helper()
	root := t.TempDir()
	full := filepath.Join(root, binary)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(root, system, 0), nil
		},
		installed: func(opts service.Options) (bool, error) { return opts.System, nil },
		serviceBinary: func(service.Options) (string, bool, error) {
			return binary, true, nil
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			return service.Result{UnitPath: "/etc/systemd/system/peers.service", Removed: true}, nil
		},
		removeMCP:     func(string, string) (string, error) { return "removed", nil },
		mcpLookup:     func(string) mcpRegistration { return mcpRegistration{} },
		removeRuntime: func(service.Options) ([]string, error) { return nil, nil },
		purgeData:     func(service.Options) ([]string, error) { return nil, nil },
		otherAccounts: func(service.Options) ([]string, error) { return nil, nil },
		getenv:        func(string) string { return "" },
		ask:           func(string) (bool, bool) { return false, true },
		fileOwner:     func(string) (uint32, error) { return 0, nil },
		removeFile: func(path string) error {
			removed = true
			return os.Remove(path)
		},
		executable:   func() (string, error) { return filepath.Join(root, exe), nil },
		evalSymlinks: func(path string) (string, error) { return path, nil },
		geteuid:      func() int { return 0 },
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall"}); got != exitOK {
		t.Fatalf("run(uninstall) = %d, want %d\nstdout: %s\nstderr: %s", got, exitOK, stdout, stderr)
	}
	return removed, stdout.String()
}

func TestUninstallKeepsABinaryThatIsNotPeers(t *testing.T) {
	cases := []struct {
		name   string
		binary string
		exe    string
		script string
	}{
		{name: "shell", binary: "/bin/sh", exe: "/usr/local/bin/peers"},
		{name: "env", binary: "/usr/bin/env", exe: "/usr/local/bin/peers"},
		{
			name:   "same name different inode",
			binary: "/opt/check/peers",
			exe:    "/opt/other/peers",
			script: "#!/bin/sh\necho ran > \"$0.ran\"\necho peers fake-version\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.script != "" {
				removed, output, marker := runBinaryDeletionWithScript(t, tc.binary, tc.exe, tc.script)
				if removed {
					t.Errorf("removed %s, which is not this peers binary\n%s", tc.binary, output)
				}
				if _, err := os.Stat(marker); !os.IsNotExist(err) {
					t.Errorf("executed %s; a binary of another inode must not be run: %v", tc.binary, err)
				}
				if !strings.Contains(output, "kept the binary "+tc.binary) {
					t.Errorf("output does not say the binary was kept:\n%s", output)
				}
				if !strings.Contains(output, "remove it manually with: rm "+tc.binary) {
					t.Errorf("output does not give the manual removal command:\n%s", output)
				}
				return
			}
			removed, output := runBinaryDeletion(t, tc.binary, tc.exe)
			if removed {
				t.Errorf("removed %s, which is not this peers binary\n%s", tc.binary, output)
			}
			if !strings.Contains(output, "kept the binary "+tc.binary) {
				t.Errorf("output does not say the binary was kept:\n%s", output)
			}
		})
	}
}

// runBinaryDeletionWithScript is runBinaryDeletion with the service binary
// written as an executable script. It returns the path the script would create
// if it ran.
func runBinaryDeletionWithScript(t *testing.T, binary, exe, script string) (removed bool, output, marker string) {
	t.Helper()
	root := t.TempDir()
	marker = filepath.Join(root, binary+".ran")
	for path, content := range map[string]string{binary: script, exe: "other"} {
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(root, system, 0), nil
		},
		installed: func(opts service.Options) (bool, error) { return opts.System, nil },
		serviceBinary: func(service.Options) (string, bool, error) {
			return binary, true, nil
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			return service.Result{UnitPath: "/etc/systemd/system/peers.service", Removed: true}, nil
		},
		removeMCP:     func(string, string) (string, error) { return "removed", nil },
		mcpLookup:     func(string) mcpRegistration { return mcpRegistration{} },
		removeRuntime: func(service.Options) ([]string, error) { return nil, nil },
		purgeData:     func(service.Options) ([]string, error) { return nil, nil },
		otherAccounts: func(service.Options) ([]string, error) { return nil, nil },
		getenv:        func(string) string { return "" },
		ask:           func(string) (bool, bool) { return false, true },
		fileOwner:     func(string) (uint32, error) { return 0, nil },
		removeFile: func(path string) error {
			removed = true
			return os.Remove(path)
		},
		executable:   func() (string, error) { return filepath.Join(root, exe), nil },
		evalSymlinks: func(path string) (string, error) { return path, nil },
		geteuid:      func() int { return 0 },
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall"}); got != exitOK {
		t.Fatalf("run(uninstall) = %d, want %d\nstdout: %s\nstderr: %s", got, exitOK, stdout, stderr)
	}
	return removed, stdout.String(), marker
}

func TestUninstallRemovesTheServiceBinary(t *testing.T) {
	root := t.TempDir()
	binary := "/usr/local/bin/peers"
	full := filepath.Join(root, binary)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	removed := false
	withUninstallSeams(t, uninstallSeams{
		options: func(system bool) (service.Options, error) {
			return uninstallOptions(root, system, 0), nil
		},
		installed: func(opts service.Options) (bool, error) { return opts.System, nil },
		serviceBinary: func(service.Options) (string, bool, error) {
			return binary, true, nil
		},
		uninstallSvc: func(service.Options) (service.Result, error) {
			return service.Result{UnitPath: "/etc/systemd/system/peers.service", Removed: true}, nil
		},
		removeMCP:     func(string, string) (string, error) { return "removed", nil },
		mcpLookup:     func(string) mcpRegistration { return mcpRegistration{} },
		removeRuntime: func(service.Options) ([]string, error) { return nil, nil },
		purgeData:     func(service.Options) ([]string, error) { return nil, nil },
		otherAccounts: func(service.Options) ([]string, error) { return nil, nil },
		getenv:        func(string) string { return "" },
		ask:           func(string) (bool, bool) { return false, true },
		fileOwner:     func(string) (uint32, error) { return 0, nil },
		removeFile: func(path string) error {
			removed = true
			return os.Remove(path)
		},
		executable:   func() (string, error) { return full, nil },
		evalSymlinks: func(path string) (string, error) { return path, nil },
		geteuid:      func() int { return 0 },
	})

	a, stdout, stderr, _ := newDispatchApp("dev")
	if got := a.run(context.Background(), []string{"uninstall"}); got != exitOK {
		t.Fatalf("run(uninstall) = %d, want %d\nstdout: %s\nstderr: %s", got, exitOK, stdout, stderr)
	}
	if !removed {
		t.Errorf("the service binary was not removed:\n%s", stdout)
	}
	if _, err := os.Stat(full); !os.IsNotExist(err) {
		t.Errorf("binary still exists: %v", err)
	}
}
