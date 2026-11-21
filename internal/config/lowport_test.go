package config

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// systemWatchCore loads a user-mode core under a temporary home, then switches
// it to the system deployment mode so the privileged-port rules apply. The
// paths stay under the temporary home. The permission seam states whether the
// file may be written only by root.
func systemWatchCore(t *testing.T, body string, rootOnly bool) (*core, string) {
	t.Helper()
	c, path := newWatchCore(t, body, false, nil)
	c.mu.Lock()
	c.mode = ModeSystem
	c.configFileRootOnly = func(string) (bool, error) { return rootOnly, nil }
	c.mu.Unlock()
	return c, path
}

// TestSetConfigLowPortRequiresRoot covers the Set refusal for a privileged
// port. Only a root caller may set one, and only while the configuration file
// cannot be written by another account.
func TestSetConfigLowPortRequiresRoot(t *testing.T) {
	const body = "broker:\n  listen: 127.0.0.1:7777\n"

	t.Run("non-root refused", func(t *testing.T) {
		c, path := systemWatchCore(t, body, true)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		_, err = c.setLocked(Actor{UIDKnown: true, UID: 1000, Account: "ana"}, "broker.listen", stringValue(":80"))
		if !errors.Is(err, ErrLowPortDenied) {
			t.Fatalf("setLocked error = %v, want ErrLowPortDenied", err)
		}
		if !strings.Contains(err.Error(), "requires root") {
			t.Errorf("error %q does not explain that root is required", err)
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(after) != string(before) {
			t.Errorf("the configuration file changed after a refused Set:\nbefore:\n%s\nafter:\n%s", before, after)
		}
		if got := c.values["broker.listen"].String; got != "127.0.0.1:7777" {
			t.Errorf("broker.listen = %q after a refused Set, want 127.0.0.1:7777", got)
		}
	})

	t.Run("root succeeds", func(t *testing.T) {
		c, _ := systemWatchCore(t, body, true)

		if _, err := c.setLocked(Actor{UIDKnown: true, UID: 0}, "broker.listen", stringValue(":80")); err != nil {
			t.Fatalf("setLocked as root: %v", err)
		}
		if got := c.values["broker.listen"].String; got != ":80" {
			t.Errorf("broker.listen = %q, want :80", got)
		}
		if got := c.effectiveListen.port; got != 80 {
			t.Errorf("effective listen port = %d, want 80", got)
		}
	})

	t.Run("root refused while the file is group writable", func(t *testing.T) {
		c, path := systemWatchCore(t, body, false)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		_, err = c.setLocked(Actor{UIDKnown: true, UID: 0}, "broker.listen", stringValue(":80"))
		if !errors.Is(err, ErrLowPortDenied) {
			t.Fatalf("setLocked error = %v, want ErrLowPortDenied", err)
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(after) != string(before) {
			t.Errorf("the configuration file changed after a refused Set")
		}
	})

	t.Run("an unknown caller identity is refused", func(t *testing.T) {
		c, path := systemWatchCore(t, body, true)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}

		// UID 0 without UIDKnown is not proof of root: an Actor that never
		// established a caller identity must not pass the root-only rule.
		_, err = c.setLocked(Actor{UID: 0}, "broker.listen", stringValue(":80"))
		if !errors.Is(err, ErrLowPortDenied) {
			t.Fatalf("setLocked error = %v, want ErrLowPortDenied", err)
		}
		after, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(after) != string(before) {
			t.Errorf("the configuration file changed after a refused Set")
		}
	})

	t.Run("non-root may set a port at or above 1024", func(t *testing.T) {
		c, _ := systemWatchCore(t, body, false)

		if _, err := c.setLocked(Actor{UIDKnown: true, UID: 1000}, "broker.listen", stringValue(":8080")); err != nil {
			t.Fatalf("setLocked non-root on :8080: %v", err)
		}
		if got := c.values["broker.listen"].String; got != ":8080" {
			t.Errorf("broker.listen = %q, want :8080", got)
		}
	})
}

// TestReloadLowPortRequiresRootOnlyFile covers the reload refusal: a
// privileged port written into a file that group or other accounts can write
// is not adopted, the old value stays, a warning is logged, and config_rejected
// is audited.
func TestReloadLowPortRequiresRootOnlyFile(t *testing.T) {
	c, path := systemWatchCore(t, "broker:\n  listen: 127.0.0.1:7777\n", false)
	var logs strings.Builder
	c.mu.Lock()
	c.logger = slog.New(slog.NewTextHandler(&logs, nil))
	c.mu.Unlock()

	writeConfigBody(t, path, "broker:\n  listen: 127.0.0.1:80\n")
	c.rescanOnce()

	if got := c.values["broker.listen"].String; got != "127.0.0.1:7777" {
		t.Fatalf("broker.listen = %q after the rejected reload, want 127.0.0.1:7777", got)
	}
	if got := c.effectiveListen.port; got != 7777 {
		t.Fatalf("effective listen port = %d after the rejected reload, want 7777", got)
	}
	if n := countEvent(t, c, EventConfigRejected, "broker.listen"); n != 1 {
		t.Fatalf("config_rejected records = %d, want 1", n)
	}
	if n := countEvent(t, c, EventConfigFileChanged, "broker.listen"); n != 0 {
		t.Fatalf("config_file_changed records = %d, want 0", n)
	}
	if !strings.Contains(logs.String(), "port below 1024") {
		t.Errorf("the warning does not explain the rejected port; logs = %q", logs.String())
	}
}

// TestReloadLowPortAdoptedWhenFileRootOnly covers the allowed reload: with a
// file only root can write, the new privileged port is adopted normally.
func TestReloadLowPortAdoptedWhenFileRootOnly(t *testing.T) {
	c, path := systemWatchCore(t, "broker:\n  listen: 127.0.0.1:7777\n", true)

	writeConfigBody(t, path, "broker:\n  listen: 127.0.0.1:80\n")
	c.rescanOnce()

	if got := c.values["broker.listen"].String; got != "127.0.0.1:80" {
		t.Fatalf("broker.listen = %q after the reload, want 127.0.0.1:80", got)
	}
	if got := c.effectiveListen.port; got != 80 {
		t.Fatalf("effective listen port = %d after the reload, want 80", got)
	}
	if n := countEvent(t, c, EventConfigRejected, "broker.listen"); n != 0 {
		t.Fatalf("config_rejected records = %d, want 0", n)
	}
	if n := countEvent(t, c, EventConfigFileChanged, "broker.listen"); n != 1 {
		t.Fatalf("config_file_changed records = %d, want 1", n)
	}
}

// TestReloadLowPortUserModeUnaffected covers the user Broker: the rule
// belongs to the system deployment, so a user reload adopts the value
// without consulting the file permission rule.
func TestReloadLowPortUserModeUnaffected(t *testing.T) {
	c, path := newWatchCore(t, "broker:\n  listen: 127.0.0.1:7777\n", false, nil)
	c.mu.Lock()
	c.configFileRootOnly = func(string) (bool, error) { return false, nil }
	c.mu.Unlock()

	writeConfigBody(t, path, "broker:\n  listen: 127.0.0.1:80\n")
	c.rescanOnce()

	if got := c.values["broker.listen"].String; got != "127.0.0.1:80" {
		t.Fatalf("broker.listen = %q after a user reload, want 127.0.0.1:80", got)
	}
}

// systemLoadCore builds a core whose system paths live under home. It is used
// to drive Load in ModeSystem without touching /etc, /var or /run.
func systemLoadCore(t *testing.T, home string, rootOnly bool) (*core, string) {
	t.Helper()
	c := newLoadTestCore(t, home, nil)
	base := filepath.Join(home, "system")
	configFile := filepath.Join(base, "etc", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(configFile), 0o755); err != nil {
		t.Fatal(err)
	}
	c.systemPaths = func() PathSet {
		return PathSet{
			RuntimeDir: filepath.Join(base, "run"),
			ConfigFile: configFile,
			DataDir:    filepath.Join(base, "lib"),
			AuditLog:   filepath.Join(base, "log", "audit.log"),
		}
	}
	c.configFileRootOnly = func(string) (bool, error) { return rootOnly, nil }
	return c, configFile
}

// TestLoadLetsLowPortThroughToTheBindStep covers the startup split: Load
// accepts a privileged port even when the configuration file is writable by
// another account, and CheckLowPort refuses it when the Broker is about to
// bind.
func TestLoadLetsLowPortThroughToTheBindStep(t *testing.T) {
	home := t.TempDir()
	c, path := systemLoadCore(t, home, false)
	if err := os.WriteFile(path, []byte("broker:\n  listen: 127.0.0.1:80\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := c.load(ModeSystem, ""); err != nil {
		t.Fatalf("load: %v", err)
	}
	err := c.checkLowPortAtStartup()
	if !errors.Is(err, ErrLowPortDenied) {
		t.Fatalf("checkLowPortAtStartup error = %v, want ErrLowPortDenied", err)
	}
	if !strings.Contains(err.Error(), "chmod g-w") {
		t.Errorf("startup error %q does not explain how to fix the file", err)
	}
}

// TestCheckLowPortReadsTheLoadedCore covers the exported entry point the
// broker calls in its listen step.
func TestCheckLowPortReadsTheLoadedCore(t *testing.T) {
	if err := CheckLowPort(); !errors.Is(err, ErrNotLoaded) {
		t.Fatalf("CheckLowPort before Load = %v, want ErrNotLoaded", err)
	}

	home := t.TempDir()
	c, path := systemLoadCore(t, home, false)
	if err := os.WriteFile(path, []byte("broker:\n  listen: 127.0.0.1:80\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.load(ModeSystem, ""); err != nil {
		t.Fatalf("load: %v", err)
	}
	global.mu.Lock()
	old := global.core
	global.core = c
	global.mu.Unlock()
	t.Cleanup(func() {
		global.mu.Lock()
		global.core = old
		global.mu.Unlock()
	})

	if err := CheckLowPort(); !errors.Is(err, ErrLowPortDenied) {
		t.Fatalf("CheckLowPort = %v, want ErrLowPortDenied", err)
	}
}

// TestLoadLowPortSucceedsWhenFileRootOnly covers the allowed startup: the same
// configuration loads and the bind-step check accepts it when the file can be
// written only by root.
func TestLoadLowPortSucceedsWhenFileRootOnly(t *testing.T) {
	home := t.TempDir()
	c, path := systemLoadCore(t, home, true)
	if err := os.WriteFile(path, []byte("broker:\n  listen: 127.0.0.1:80\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := c.load(ModeSystem, ""); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := c.effectiveListen.port; got != 80 {
		t.Fatalf("effective listen port = %d, want 80", got)
	}
	if err := c.checkLowPortAtStartup(); err != nil {
		t.Fatalf("checkLowPortAtStartup: %v", err)
	}
}
