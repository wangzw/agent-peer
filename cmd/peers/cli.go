package main

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// Help group IDs. Every visible command is assigned to one of these groups so
// the help output lists it under the matching heading.
const (
	groupPairing = "pairing"
	groupLocal   = "local"
	groupInstall = "install"
)

// errNeedsYes is returned by confirm when there is no terminal and the caller
// did not pass --yes. A command wraps it with its own context and tests it with
// errors.Is.
var errNeedsYes = errors.New("standard input is not a terminal; pass --yes to proceed without asking")

// exitError makes a command end with a chosen process exit code. A RunE that
// returns any other error exits with exitFailure.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

// usageError marks err as a usage error, which exits with exitUsage. An error
// that already carries a code is returned unchanged.
func usageError(err error) error {
	if err == nil {
		return nil
	}
	var exit *exitError
	if errors.As(err, &exit) {
		return err
	}
	return &exitError{code: exitUsage, err: err}
}

// usageErrorf builds a usage error from a format string.
func usageErrorf(format string, args ...any) error {
	return &exitError{code: exitUsage, err: fmt.Errorf(format, args...)}
}

// commandResult converts the exit code of a command body that has already
// written its own diagnostics into an error for a cobra RunE. A successful
// result returns nil.
func commandResult(code int) error {
	if code == exitOK {
		return nil
	}
	return &exitError{code: code}
}

// levelFlags backs the --system and --user flags. The two are mutually
// exclusive; when neither is given the level follows the executing account.
type levelFlags struct {
	systemFlag bool
	userFlag   bool
}

// addLevelFlags adds the mutually exclusive --system and --user flags to cmd.
// The exclusion is checked by system in the command's RunE, because cobra's own
// flag-group errors cannot be mapped to exitUsage reliably.
func addLevelFlags(cmd *cobra.Command) *levelFlags {
	f := &levelFlags{}
	cmd.Flags().BoolVar(&f.systemFlag, "system", false, "operate on the system-level installation")
	cmd.Flags().BoolVar(&f.userFlag, "user", false, "operate on the user-level installation")
	return f
}

// system reports whether the command runs at the system level: --system means
// yes, --user means no, and with neither given only root decides yes. Giving
// both is a usage error.
func (f *levelFlags) system(euid int) (bool, error) {
	if f.systemFlag && f.userFlag {
		return false, usageErrorf("--system and --user are mutually exclusive")
	}
	if f.systemFlag {
		return true, nil
	}
	if f.userFlag {
		return false, nil
	}
	return euid == 0, nil
}

// addYesFlag adds --yes/-y to cmd and returns the value it sets.
func addYesFlag(cmd *cobra.Command) *bool {
	return cmd.Flags().BoolP("yes", "y", false, "do not ask for confirmation")
}

// confirm asks prompt on standard error and returns the answer. yes skips the
// question and returns true. Without a terminal and without yes, the error
// wraps errNeedsYes so the caller can detect the missing confirmation flag.
func (a *app) confirm(prompt string, yes bool) (bool, error) {
	if yes {
		return true, nil
	}
	if !isTerminal(a.stdin) {
		return false, errNeedsYes
	}
	_, _ = fmt.Fprint(a.stderr, prompt)
	answer, err := readAnswer(a.stdin)
	if err != nil {
		return false, fmt.Errorf("reading the answer: %w", err)
	}
	return isYes(answer), nil
}
