package inbox

import "os"

// InSession reports whether the current process runs inside the process tree of
// a Claude Code session, which Claude Code marks by exporting the session's
// inbox socket path.
func InSession() bool {
	return os.Getenv("CLAUDE_CODE_MESSAGING_SOCKET") != ""
}
