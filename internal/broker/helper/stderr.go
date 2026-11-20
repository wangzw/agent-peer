package helper

import (
	"bytes"
	"log/slog"
	"sync"
)

// maxHelperStderrLine bounds the bytes held for one unterminated stderr line,
// so a helper that never writes a newline cannot grow the Broker's memory.
const maxHelperStderrLine = 64 << 10

// helperStderr receives a helper child's standard error and logs it with the
// account the child was started for. The Broker no longer inherits its own
// stderr into helpers, so a helper cannot write to the Broker's terminal or
// logs without its uid attached.
type helperStderr struct {
	log *slog.Logger
	uid uint32

	mu  sync.Mutex
	buf []byte
}

func newHelperStderr(log *slog.Logger, uid uint32) *helperStderr {
	return &helperStderr{log: log, uid: uid}
}

func (w *helperStderr) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	n := len(p)
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		w.logLine(string(bytes.TrimSuffix(w.buf[:i], []byte{'\r'})))
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) > maxHelperStderrLine {
		w.logLine(string(w.buf[:maxHelperStderrLine]) + " (truncated)")
		w.buf = w.buf[:0]
	}
	return n, nil
}

// flush logs a last line that the child wrote without a trailing newline.
func (w *helperStderr) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.logLine(string(bytes.TrimSuffix(w.buf, []byte{'\r'})))
		w.buf = nil
	}
}

func (w *helperStderr) logLine(line string) {
	w.log.Warn("account_helper_stderr",
		"uid", w.uid,
		"line", line)
}
