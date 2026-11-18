package config

import "testing"

// TestNewCoreUsesHomeDirSource checks that newCore reads the account home
// directory through the homeDirSource seam, so the agentpeer_testenv build can
// replace it. It cannot run in parallel because it changes a package-level
// variable.
func TestNewCoreUsesHomeDirSource(t *testing.T) {
	old := homeDirSource
	t.Cleanup(func() { homeDirSource = old })

	homeDirSource = func() (string, error) { return "/seam/home", nil }

	got, err := newCore().homeDir()
	if err != nil {
		t.Fatalf("homeDir() error = %v, want nil", err)
	}
	if got != "/seam/home" {
		t.Fatalf("homeDir() = %q, want %q", got, "/seam/home")
	}
}

// TestHomeDirSourceDefaultsToAccountHomeDir checks that the seam defaults to
// accountHomeDir, so the production build still resolves the same home
// directory.
func TestHomeDirSourceDefaultsToAccountHomeDir(t *testing.T) {
	gotSource, errSource := homeDirSource()
	gotAccount, errAccount := accountHomeDir()
	if gotSource != gotAccount {
		t.Fatalf("homeDirSource() = %q, accountHomeDir() = %q", gotSource, gotAccount)
	}
	if (errSource == nil) != (errAccount == nil) {
		t.Fatalf("homeDirSource() error = %v, accountHomeDir() error = %v", errSource, errAccount)
	}
}
