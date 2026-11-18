package config

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// TestSetListenValidatesDerivedAlias covers the case where broker.alias is not
// configured: changing broker.listen derives a new effective alias. The
// broker.alias validators must run on that derived value too; otherwise the
// derived alias can become equal to a peer's effective alias, which
// design-config.md forbids. A refusal leaves both keys unchanged and the file
// untouched.
func TestSetListenValidatesDerivedAlias(t *testing.T) {
	c, path := newValueCore(t, "broker:\n  public: false\n", "")
	installCoreForTest(t, c)

	rejected := errors.New("alias equals a peer's effective alias")
	var checkedOld, checkedNew string
	Validate("broker.alias", func(old, new Value) error {
		checkedOld, checkedNew = old.String, new.String
		return rejected
	})

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Set(Actor{}, "broker.listen", stringValue("127.0.0.1:27443"))
	if !errors.Is(err, ErrInvalidValue) {
		t.Fatalf("Set(broker.listen) error = %v, want ErrInvalidValue", err)
	}
	if checkedOld != "hostA-17443" || checkedNew != "hostA-27443" {
		t.Fatalf("validator saw old = %q, new = %q; want the derived alias change hostA-17443 -> hostA-27443",
			checkedOld, checkedNew)
	}
	if v, _ := Get("broker.listen"); v.String != ":17443" {
		t.Fatalf("broker.listen = %q after a rejected Set, want the old value", v.String)
	}
	if v, _ := Get("broker.alias"); v.String != "hostA-17443" {
		t.Fatalf("broker.alias = %q after a rejected Set, want the old derived value", v.String)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("the configuration file changed after a rejected Set:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if n := countEvent(t, c, EventConfigSet, ""); n != 0 {
		t.Fatalf("config_set records = %d, want 0", n)
	}
}

// TestReloadListenValidatesDerivedAlias covers the reload path: a file that
// changes broker.listen derives a new effective alias, and the broker.alias
// validators must decide whether it may be adopted. A refusal keeps the old
// alias and logs the rejection.
func TestReloadListenValidatesDerivedAlias(t *testing.T) {
	c, path := newValueCore(t, "broker:\n  listen: \":17443\"\n", "")
	installCoreForTest(t, c)
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewTextHandler(&logs, nil))

	rejected := errors.New("alias equals a peer's effective alias")
	Validate("broker.alias", func(old, new Value) error { return rejected })

	writeConfigBody(t, path, "broker:\n  listen: \"127.0.0.1:27443\"\n")
	c.rescanOnce()

	if got := aliasOf(t, c); got != "hostA-17443" {
		t.Fatalf("broker.alias = %q after a rejected reload, want the old derived value hostA-17443", got)
	}
	if !strings.Contains(logs.String(), "validator rejected the derived alias") {
		t.Fatalf("the rejected derived alias was not logged; log = %q", logs.String())
	}
	if n := countEvent(t, c, EventConfigFileChanged, "broker.alias"); n != 0 {
		t.Fatalf("config_file_changed records for broker.alias = %d, want 0", n)
	}
}

// TestReloadRejectedDerivedAliasKeepsExplicitAlias covers the state left
// behind when the file removes an explicit broker.alias and the derived value
// is rejected. The old explicit alias stays in effect, so explicitAlias must
// stay true too; otherwise a later broker.listen change would be treated as
// deriving the alias and would overwrite the explicit one.
func TestReloadRejectedDerivedAliasKeepsExplicitAlias(t *testing.T) {
	c, path := newValueCore(t, "broker:\n  listen: \":17443\"\n  alias: \"hostB\"\n", "")
	installCoreForTest(t, c)
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewTextHandler(&logs, nil))

	rejected := errors.New("alias equals a peer's effective alias")
	Validate("broker.alias", func(old, new Value) error { return rejected })

	writeConfigBody(t, path, "broker:\n  listen: \"127.0.0.1:27443\"\n")
	c.rescanOnce()

	if got := aliasOf(t, c); got != "hostB" {
		t.Fatalf("broker.alias = %q after a rejected reload, want the old explicit value hostB", got)
	}
	if v, _ := Get("broker.listen"); v.String != ":17443" {
		t.Fatalf("broker.listen = %q after a rejected reload, want the old value", v.String)
	}
	if !c.explicitAlias {
		t.Fatal("explicitAlias = false after a rejected derivation, want the old explicit alias to stay in effect")
	}
	if !strings.Contains(logs.String(), "validator rejected the derived alias") {
		t.Fatalf("the rejected derived alias was not logged; log = %q", logs.String())
	}
	if n := countEvent(t, c, EventConfigFileChanged, "broker.alias"); n != 0 {
		t.Fatalf("config_file_changed records for broker.alias = %d, want 0", n)
	}
}

// TestReloadExplicitAliasValidatesConfiguredValue covers the other reload
// branch: the file configures broker.alias explicitly. The broker.alias
// validators must see the new value there too; a rejection keeps the previous
// alias and rolls the listen and advertise group back with it, as a rejected
// derived alias does.
func TestReloadExplicitAliasValidatesConfiguredValue(t *testing.T) {
	c, path := newValueCore(t, "broker:\n  listen: \":17443\"\n", "")
	installCoreForTest(t, c)
	var logs bytes.Buffer
	c.logger = slog.New(slog.NewTextHandler(&logs, nil))

	rejected := errors.New("alias equals a peer's effective alias")
	var checkedOld, checkedNew string
	Validate("broker.alias", func(old, new Value) error {
		checkedOld, checkedNew = old.String, new.String
		if new.String == "hostB" {
			return rejected
		}
		return nil
	})

	writeConfigBody(t, path, "broker:\n  listen: \"127.0.0.1:27443\"\n  alias: \"hostB\"\n")
	c.rescanOnce()

	if checkedOld != "hostA-17443" || checkedNew != "hostB" {
		t.Fatalf("validator saw old = %q, new = %q; want the configured change hostA-17443 -> hostB",
			checkedOld, checkedNew)
	}
	if got := aliasOf(t, c); got != "hostA-17443" {
		t.Fatalf("broker.alias = %q after a rejected reload, want the old derived value hostA-17443", got)
	}
	// Keeping the old alias rolls the listen address group back with it, as the
	// derived-alias path does.
	if v, _ := Get("broker.listen"); v.String != ":17443" {
		t.Fatalf("broker.listen = %q after a rejected reload, want the old value", v.String)
	}
	if !strings.Contains(logs.String(), "validator rejected the reloaded alias") {
		t.Fatalf("the rejected configured alias was not logged; log = %q", logs.String())
	}
	if n := countEvent(t, c, EventConfigFileChanged, "broker.alias"); n != 0 {
		t.Fatalf("config_file_changed records for broker.alias = %d, want 0", n)
	}
}
