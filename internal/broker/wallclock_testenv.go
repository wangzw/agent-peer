//go:build agentpeer_testenv

package broker

import (
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// testClockFileEnv names a file whose content is a time.ParseDuration string,
// such as "144h", added to the clock the directory reads. It is consulted only
// by builds carrying the agentpeer_testenv tag.
const testClockFileEnv = "AGENT_PEER_TEST_CLOCK_FILE"

func init() {
	path := os.Getenv(testClockFileEnv)
	if path == "" {
		return
	}
	wallClock = offsetClock(path)
}

// offsetClock returns a clock that adds the duration stored in path to the
// current time. The file is re-read whenever its modification time or size
// changes, so a test can move time forward by rewriting the file.
//
// A missing or unparsable file yields an offset of zero. A warning is logged
// once when the file becomes unusable, and the warning is armed again only
// after a later read succeeds.
func offsetClock(path string) func() time.Time {
	var (
		mu       sync.Mutex
		modTime  time.Time
		size     int64
		offset   time.Duration
		loaded   bool
		unusable bool
	)
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()

		info, err := os.Stat(path)
		if err != nil {
			offset = 0
			loaded = false
			if !unusable {
				slog.Default().Warn("test_clock_file_unusable", "path", path, "error", err)
				unusable = true
			}
			return time.Now()
		}
		if !loaded || !info.ModTime().Equal(modTime) || info.Size() != size {
			d, err := readClockOffset(path)
			modTime, size, loaded = info.ModTime(), info.Size(), true
			if err != nil {
				offset = 0
				if !unusable {
					slog.Default().Warn("test_clock_file_unusable", "path", path, "error", err)
					unusable = true
				}
			} else {
				offset = d
				unusable = false
			}
		}
		return time.Now().Add(offset)
	}
}

// readClockOffset parses the single duration line held by path.
func readClockOffset(path string) (time.Duration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return time.ParseDuration(strings.TrimSpace(string(data)))
}
