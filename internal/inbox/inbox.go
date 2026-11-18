package inbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// maxRecordSize is the largest session registry record the package reads. It
// matches the limit Claude Code applies when it reads the registry; a larger
// file is treated as one that cannot be parsed.
const maxRecordSize = 256 << 10

// Session is one session registry record.
type Session struct {
	ConfigDir  string // the configuration directory the record lives under
	PID        int    // session process id, the record file name
	Name       string // session name, verbatim from the record
	Status     string // verbatim from the record, may be empty
	SocketPath string // verbatim from the record; empty until the session binds its inbox
}

var (
	// ErrNoRecord reports that no matching session registry record exists.
	ErrNoRecord = errors.New("inbox: no session registry record")
	// ErrPartial reports that a regular record file exists but its content
	// cannot be parsed, including an empty file.
	ErrPartial = errors.New("inbox: session registry record is incomplete")
	// ErrBadSignature reports that sig is empty or the receiver would change
	// it.
	ErrBadSignature = errors.New("inbox: signature would be changed by the receiver")
	// ErrNotWritten reports that the message was certainly not written.
	ErrNotWritten = errors.New("inbox: message was not written to the inbox socket")
	// ErrUncertain reports that the message may have been written.
	ErrUncertain = errors.New("inbox: message may have been written to the inbox socket")
)

// resolveConfigDir returns the configuration directory to read.
//
// An empty configDir means the current account's default, $HOME/.claude. A
// non-empty value is used as given after a lexical filepath.Clean: a relative
// path stays relative and is resolved by the operating system against the
// caller's working directory. CLAUDE_CONFIG_DIR is deliberately not consulted:
// the design says the default is the account's home directory, and a caller
// that knows a different directory passes it in.
func resolveConfigDir(configDir string) (string, error) {
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("inbox: resolving home directory: %w", err)
		}
		return filepath.Join(home, ".claude"), nil
	}
	return filepath.Clean(configDir), nil
}

// sessionsDir returns the session registry directory of a resolved
// configuration directory.
func sessionsDir(configDir string) string {
	return filepath.Join(configDir, "sessions")
}

// recordFile returns the registry file of one process.
func recordFile(configDir string, pid int) string {
	return filepath.Join(sessionsDir(configDir), strconv.Itoa(pid)+".json")
}

// parseRecordName accepts the canonical "<pid>.json" spelling only. It rejects
// names such as "007.json" because strconv.Itoa would not reproduce them.
func parseRecordName(name string) (int, bool) {
	const suffix = ".json"
	if len(name) <= len(suffix) || name[len(name)-len(suffix):] != suffix {
		return 0, false
	}
	digits := name[:len(name)-len(suffix)]
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, false
		}
	}
	pid, err := strconv.Atoi(digits)
	if err != nil || pid <= 0 || strconv.Itoa(pid) != digits {
		return 0, false
	}
	return pid, true
}
