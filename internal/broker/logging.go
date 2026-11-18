package broker

import (
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
)

// Event names for the structured runtime log. Each value is the "event" field
// of one log record.
const (
	EvBrokerStarted           = "broker_started"
	EvBrokerStopping          = "broker_stopping"
	EvStartupFailed           = "startup_failed"
	EvLockLost                = "account_lock_lost"
	EvLocalSocketRebound      = "local_socket_rebound"
	EvLocalSocketRebindFailed = "local_socket_rebind_failed"
	EvConnectionRejected      = "local_connection_rejected"
	EvHelloVersionMismatch    = "hello_version_mismatch"
	EvAgentRegistered         = "agent_registered"
	EvAgentUnregistered       = "agent_unregistered"
	EvRegistrationWaiting     = "registration_waiting"
	EvAgentRenamed            = "agent_renamed"
	EvAgentSocketChanged      = "agent_socket_changed"
	EvRecordRereadFailed      = "session_record_reread_failed"
	EvRegistryWatchFailed     = "session_registry_watch_failed"
	EvLogLevelChanged         = "log_level_changed"
	// EvRemoteMemberInvalid is logged when a remote roster entry cannot be
	// rendered into an address because its ref is unusable. The entry is
	// skipped; the ref comes from the peer, so it must not decide whether this
	// process survives.
	EvRemoteMemberInvalid = "remote_member_invalid"
)

// NewLogger returns a logger that writes one JSON object per line. The "msg"
// key becomes "event", the time is rendered in UTC with nanosecond precision,
// and the level is read from level on every record so a later Set takes effect
// immediately. Writes to w are serialized, so a plain bytes.Buffer or an
// io.Writer that is not otherwise protected is safe to pass.
func NewLogger(w io.Writer, level *slog.LevelVar) *slog.Logger {
	handler := slog.NewJSONHandler(&lockedWriter{w: w}, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) != 0 {
				return a
			}
			switch a.Key {
			case slog.MessageKey:
				a.Key = "event"
			case slog.TimeKey:
				a.Value = slog.StringValue(a.Value.Time().UTC().Format(time.RFC3339Nano))
			}
			return a
		},
	})
	return slog.New(handler)
}

// lockedWriter serializes writes so that concurrent log records never
// interleave inside one line.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (lw *lockedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.w.Write(p)
}

// ParseLogLevel converts a configured log level name to a slog.Level. The
// accepted names are lower-case "debug", "info", "warn" and "error", matching
// the values config accepts for log.level.
func ParseLogLevel(s string) (slog.Level, error) {
	switch s {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("broker: unknown log level %q (want debug, info, warn or error)", s)
	}
}
