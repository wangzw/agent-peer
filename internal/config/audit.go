package config

// The audit log is an append-only file of JSON lines. The PRD states that the
// audit log is only appended to and is never deleted by the system, so this
// implementation never rewrites a record that is already on disk.
//
// Repeated connection_rejected and rate-limited pairing_failed events are
// merged in memory before they are written. The first event for a merge key
// opens a ten-minute window; later events inside the window only increment the
// window's count. When the window expires, or when the log is closed, the log
// appends one line carrying the count. ReadAudit returns the records already in
// the file plus the pending in-memory records, in time order. A crash loses the
// pending windows, which is the documented cost of never rewriting the file.
//
// Every write is a single write call of one complete line, so a reader in
// another process never sees a record spliced into the middle of a line. Reads
// hold the audit lock, and a line that cannot be decoded is skipped with an
// error log entry so a crash-truncated tail does not make the whole log
// unreadable.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	// auditFileMode is the mode used when the audit log is created. Both
	// deployment modes use it: the user log belongs to the account that
	// runs the Broker, and the system log is writable only by root.
	auditFileMode os.FileMode = 0o600

	// auditDirMode is the fallback mode for the audit log's parent directory.
	// Load creates the parent with the mode the deployment requires before it
	// opens the audit log; this value only matters when the directory has
	// disappeared in between, so it uses the stricter user mode.
	auditDirMode os.FileMode = 0o700

	// auditMergeWindow is the interval within which repeated mergeable events
	// are folded into the earlier record.
	auditMergeWindow = 10 * time.Minute

	// auditTimeLayout has nanosecond precision and always writes nine
	// fractional digits. The value is always converted to UTC first.
	auditTimeLayout = "2006-01-02T15:04:05.000000000Z"
)

// errAuditClosed is returned by append and read after the log has been closed.
var errAuditClosed = errors.New("config: audit log is closed")

// auditTimer is the part of time.Timer the audit log uses. afterFunc is a seam
// so a test can move the injected clock past a window and fire the expiry
// callback without waiting ten minutes.
type auditTimer interface {
	Stop()
}

type realAuditTimer struct{ timer *time.Timer }

func (t realAuditTimer) Stop() { t.timer.Stop() }

func newRealAuditTimer(d time.Duration, f func()) auditTimer {
	return realAuditTimer{timer: time.AfterFunc(d, f)}
}

// pendingAudit is one open merge window. event is the window's first event,
// started anchors the ten-minute window, and occurrences counts every event
// the window received.
type pendingAudit struct {
	event       Event
	started     time.Time
	occurrences uint64
}

// auditLog serializes access to the audit file. Its mutex is independent of
// core.mu because Audit may be called from other packages while a configuration
// change holds core.mu.
type auditLog struct {
	mu     sync.Mutex
	file   *os.File
	path   string
	now    func() time.Time
	logger *slog.Logger
	closed bool

	// afterFunc schedules the window-expiry callback. The default uses a real
	// timer; tests replace it to fire the callback on command.
	afterFunc func(time.Duration, func()) auditTimer

	// pending holds the open merge windows, keyed by auditMergeKey. Their
	// events never reach the file until the window closes.
	pending  map[string]*pendingAudit
	timer    auditTimer
	timerDue time.Time
}

// openAudit opens or creates the audit log at path. It follows the signature
// agreed for the package-internal interface; openAuditWithDirMode is the
// variant used by tests to exercise the fallback parent-directory mode.
func openAudit(path string, now func() time.Time, logger *slog.Logger) (*auditLog, error) {
	return openAuditWithDirMode(path, auditDirMode, now, logger)
}

func openAuditWithDirMode(path string, dirMode os.FileMode, now func() time.Time, logger *slog.Logger) (*auditLog, error) {
	if path == "" {
		return nil, errors.New("config: audit log path is empty")
	}
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, dirMode); err != nil {
			return nil, fmt.Errorf("config: cannot create audit log directory %s: %w", dir, err)
		}
	}
	file, err := createOrOpenAuditFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: cannot open audit log %s: %w", path, err)
	}
	if err := terminatePartialLine(file); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("config: cannot repair audit log %s: %w", path, err)
	}
	return &auditLog{
		file:      file,
		path:      path,
		now:       now,
		logger:    logger,
		afterFunc: newRealAuditTimer,
		pending:   make(map[string]*pendingAudit),
	}, nil
}

// createOrOpenAuditFile creates the audit file with auditFileMode when it does
// not exist and opens it for appending otherwise. The explicit chmod corrects
// a permissive umask; an existing file keeps its own mode, owner and group.
func createOrOpenAuditFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL|os.O_APPEND, auditFileMode)
	if err == nil {
		if chmodErr := file.Chmod(auditFileMode); chmodErr != nil {
			_ = file.Close()
			return nil, chmodErr
		}
		return file, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	return os.OpenFile(path, os.O_RDWR|os.O_APPEND, 0)
}

// terminatePartialLine appends a newline when the file does not end with one.
// The audit log never rewrites existing bytes, so a record half-written by a
// crash stays unreadable; the newline only keeps that tail from swallowing the
// next record. The file is opened for appending, so the write lands at the end.
func terminatePartialLine(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.Size() == 0 {
		return nil
	}
	last := make([]byte, 1)
	if _, err := file.ReadAt(last, info.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	_, err = file.Write([]byte{'\n'})
	return err
}

// append either writes one complete JSON line or folds the event into an open
// merge window. A write is a single Write call on a file opened with O_APPEND,
// so concurrent appenders and readers never observe a partial record in the
// middle of the file.
//
// Count is an output-only field. A window's line carries the number of events
// the window accumulated; an event that never merged carries no count. An
// incoming Count is therefore discarded.
//
// Secret values and the pairing static key's old/new values are removed before
// encoding. An unknown event kind is dropped with an error log entry.
func (a *auditLog) append(e Event) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return errAuditClosed
	}
	if !validEventKind(e.Event) {
		a.logger.Error("config_audit_unknown_kind", "audit_event", string(e.Event))
		return fmt.Errorf("config: unknown audit event kind %q", e.Event)
	}
	if e.Time.IsZero() {
		e.Time = a.now()
	}
	e.Count = 0
	if e.Event == EventConfigSet || e.Event == EventConfigFileChanged {
		if spec, ok := lookupKey(e.Key); ok && spec.Secret {
			e.Old = nil
			e.New = nil
		}
	}
	if e.Old != nil && e.Old.Kind == KindSecret {
		e.Old = nil
	}
	if e.New != nil && e.New.Kind == KindSecret {
		e.New = nil
	}
	if key, mergeable := auditMergeKey(e); mergeable {
		a.mergeLocked(key, e)
		return nil
	}
	return a.writeEventLocked(e)
}

// mergeLocked adds an event to the open window for key. An event outside the
// window, or one whose clock moved backwards, closes the old window and opens
// a new one at this event's time.
func (a *auditLog) mergeLocked(key string, e Event) {
	a.flushExpiredLocked(a.now())
	if pending, ok := a.pending[key]; ok {
		if !e.Time.Before(pending.started) && e.Time.Sub(pending.started) <= auditMergeWindow {
			pending.occurrences++
			return
		}
		a.writePendingLocked(pending)
		delete(a.pending, key)
	}
	a.pending[key] = &pendingAudit{event: e, started: e.Time, occurrences: 1}
	a.rescheduleLocked()
}

// flushExpiredLocked appends every window whose deadline has passed and
// reschedules the timer for the remaining windows.
func (a *auditLog) flushExpiredLocked(now time.Time) {
	keys := a.sortedPendingKeysLocked()
	for _, key := range keys {
		pending := a.pending[key]
		if now.Before(pending.started.Add(auditMergeWindow)) {
			continue
		}
		a.writePendingLocked(pending)
		delete(a.pending, key)
	}
	a.rescheduleLocked()
}

// writePendingLocked appends the single line that represents a closed window.
func (a *auditLog) writePendingLocked(pending *pendingAudit) {
	event := pending.event
	event.MergeKey = ""
	if pending.occurrences > 1 {
		event.Count = pending.occurrences
	}
	_ = a.writeEventLocked(event)
}

// writeEventLocked encodes and appends one record. The merge key is an
// in-memory grouping aid and never reaches the file.
func (a *auditLog) writeEventLocked(e Event) error {
	e.MergeKey = ""
	line, err := encodeEvent(e)
	if err != nil {
		a.logger.Error("config_audit_encode_failed", "audit_event", string(e.Event), "err", err)
		return err
	}
	if _, err := a.file.Write(line); err != nil {
		a.logger.Error("config: cannot append audit record", "path", a.path, "err", err)
		return err
	}
	return nil
}

// rescheduleLocked arms one timer for the earliest open window. The timer
// callback flushes every window whose deadline has passed.
func (a *auditLog) rescheduleLocked() {
	if a.closed {
		a.stopTimerLocked()
		return
	}
	keys := a.sortedPendingKeysLocked()
	if len(keys) == 0 {
		a.stopTimerLocked()
		return
	}
	earliest := a.pending[keys[0]].started.Add(auditMergeWindow)
	for _, key := range keys[1:] {
		deadline := a.pending[key].started.Add(auditMergeWindow)
		if deadline.Before(earliest) {
			earliest = deadline
		}
	}
	if a.timer != nil && a.timerDue.Equal(earliest) {
		return
	}
	a.stopTimerLocked()
	delay := earliest.Sub(a.now())
	if delay < 0 {
		delay = 0
	}
	a.timer = a.afterFunc(delay, a.expire)
	a.timerDue = earliest
}

func (a *auditLog) stopTimerLocked() {
	if a.timer != nil {
		a.timer.Stop()
	}
	a.timer = nil
	a.timerDue = time.Time{}
}

// expire is the timer callback. It takes the audit lock because the timer runs
// on its own goroutine.
func (a *auditLog) expire() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.flushExpiredLocked(a.now())
}

// sortedPendingKeysLocked returns the pending merge keys in a deterministic
// order, so equal deadlines flush in a stable file order.
func (a *auditLog) sortedPendingKeysLocked() []string {
	keys := make([]string, 0, len(a.pending))
	for key := range a.pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// read loads the whole log, filters by since when it is not zero, adds the
// pending in-memory windows, and returns the result sorted by event time
// ascending. Equal timestamps keep their file order, and pending records come
// after file records with the same time.
//
// Filtering compares the record's time: a merged window is represented by its
// first event's time, so a window that opened before since is omitted even if
// a later event inside it falls after since.
func (a *auditLog) read(since time.Time) ([]Event, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, errAuditClosed
	}
	if _, err := a.file.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("config: cannot seek audit log %s: %w", a.path, err)
	}
	data, err := io.ReadAll(a.file)
	if err != nil {
		return nil, fmt.Errorf("config: cannot read audit log %s: %w", a.path, err)
	}
	events := make([]Event, 0, bytes.Count(data, []byte{'\n'}))
	for len(data) > 0 {
		var line []byte
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = data[:i], data[i+1:]
		} else {
			line, data = data, nil
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		event, err := decodeEvent(line)
		if err != nil {
			a.logger.Error("config: skipping unreadable audit record", "path", a.path, "err", err)
			continue
		}
		if !since.IsZero() && event.Time.Before(since) {
			continue
		}
		events = append(events, event)
	}
	for _, key := range a.sortedPendingKeysLocked() {
		pending := a.pending[key]
		event := pending.event
		if pending.occurrences > 1 {
			event.Count = pending.occurrences
		}
		if !since.IsZero() && event.Time.Before(since) {
			continue
		}
		events = append(events, event)
	}
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Time.Before(events[j].Time)
	})
	// MergeKey is an in-memory grouping aid, never part of a returned record.
	for index := range events {
		events[index].MergeKey = ""
	}
	return events, nil
}

// close stops the expiry timer, appends every pending window, and closes the
// file. The pending records reach the file only here or when their window
// expires; a crash before either loses them.
func (a *auditLog) close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	a.stopTimerLocked()
	for _, key := range a.sortedPendingKeysLocked() {
		a.writePendingLocked(a.pending[key])
		delete(a.pending, key)
	}
	return a.file.Close()
}

// auditMergeKey returns the key that identifies a merge window. The event kind
// is part of the key so two different event types can never share a group.
// An unmergeable event returns false.
func auditMergeKey(e Event) (string, bool) {
	switch e.Event {
	case EventConnectionRejected:
		if e.Peer != nil {
			var zero [32]byte
			if e.Peer.Fingerprint != zero {
				return "connection_rejected:" + hex.EncodeToString(e.Peer.Fingerprint[:]), true
			}
		}
		// A caller may supply a source address instead of a fingerprint.
		if e.MergeKey != "" {
			return "connection_rejected:" + e.MergeKey, true
		}
		return "", false
	case EventPairingFailed:
		if e.MergeKey == "" {
			return "", false
		}
		return "pairing_failed:" + e.MergeKey, true
	default:
		return "", false
	}
}

func validEventKind(kind EventKind) bool {
	return allEventKinds[kind]
}

// wireEvent is the JSON representation of one line. The merge key is an
// in-memory grouping aid and has no field here.
type wireEvent struct {
	Time    string     `json:"time"`
	Event   EventKind  `json:"event"`
	Actor   string     `json:"actor,omitempty"`
	Account string     `json:"account,omitempty"`
	Peer    *wirePeer  `json:"peer,omitempty"`
	Key     string     `json:"key,omitempty"`
	Old     *wireValue `json:"old,omitempty"`
	New     *wireValue `json:"new,omitempty"`
	Count   uint64     `json:"count,omitempty"`
	Detail  string     `json:"detail,omitempty"`
}

type wirePeer struct {
	Fingerprint string `json:"fingerprint"`
	Alias       string `json:"alias,omitempty"`
}

// wireValue keeps the configuration value's type alongside its data. JSON
// alone cannot distinguish an integer from a string or a duration, so the kind
// travels with the value and read restores the typed Value.
type wireValue struct {
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value,omitempty"`
}

func encodeEvent(e Event) ([]byte, error) {
	wire := wireEvent{
		Time:    e.Time.UTC().Format(auditTimeLayout),
		Event:   e.Event,
		Actor:   e.Actor,
		Account: e.Account,
		Key:     e.Key,
		Count:   e.Count,
		Detail:  e.Detail,
	}
	if e.Peer != nil {
		wire.Peer = &wirePeer{
			Fingerprint: hex.EncodeToString(e.Peer.Fingerprint[:]),
			Alias:       e.Peer.Alias,
		}
	}
	if e.Old != nil {
		value, err := encodeValue(*e.Old)
		if err != nil {
			return nil, err
		}
		wire.Old = value
	}
	if e.New != nil {
		value, err := encodeValue(*e.New)
		if err != nil {
			return nil, err
		}
		wire.New = value
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(wire); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func encodeValue(v Value) (*wireValue, error) {
	switch v.Kind {
	case KindString:
		data, err := json.Marshal(v.String)
		if err != nil {
			return nil, err
		}
		return &wireValue{Kind: "string", Value: data}, nil
	case KindBool:
		data, err := json.Marshal(v.Bool)
		if err != nil {
			return nil, err
		}
		return &wireValue{Kind: "bool", Value: data}, nil
	case KindInt:
		data, err := json.Marshal(v.Int)
		if err != nil {
			return nil, err
		}
		return &wireValue{Kind: "int", Value: data}, nil
	case KindDuration:
		data, err := json.Marshal(v.Duration.String())
		if err != nil {
			return nil, err
		}
		return &wireValue{Kind: "duration", Value: data}, nil
	case KindStringList:
		values := v.Strings
		if values == nil {
			values = []string{}
		}
		data, err := json.Marshal(values)
		if err != nil {
			return nil, err
		}
		return &wireValue{Kind: "string_list", Value: data}, nil
	case KindSecret:
		// A secret value never reaches the audit log. Callers that build an
		// event directly have the field stripped by append, but returning an
		// error here keeps the encoder from ever producing one.
		return nil, errors.New("config: secret value cannot be written to the audit log")
	default:
		return nil, fmt.Errorf("config: unknown value kind %d", v.Kind)
	}
}

func decodeEvent(line []byte) (Event, error) {
	var wire wireEvent
	if err := json.Unmarshal(line, &wire); err != nil {
		return Event{}, err
	}
	if wire.Time == "" {
		return Event{}, errors.New("config: audit record has no time")
	}
	when, err := time.Parse(time.RFC3339Nano, wire.Time)
	if err != nil {
		return Event{}, err
	}
	if !validEventKind(wire.Event) {
		return Event{}, fmt.Errorf("config: unknown audit event kind %q", wire.Event)
	}
	event := Event{
		Time:    when,
		Event:   wire.Event,
		Actor:   wire.Actor,
		Account: wire.Account,
		Key:     wire.Key,
		Count:   wire.Count,
		Detail:  wire.Detail,
	}
	if wire.Peer != nil {
		fingerprint, err := hex.DecodeString(wire.Peer.Fingerprint)
		if err != nil {
			return Event{}, err
		}
		if len(fingerprint) != len(PeerRef{}.Fingerprint) {
			return Event{}, fmt.Errorf("config: audit peer fingerprint has %d bytes", len(fingerprint))
		}
		peer := PeerRef{Alias: wire.Peer.Alias}
		copy(peer.Fingerprint[:], fingerprint)
		event.Peer = &peer
	}
	if wire.Old != nil {
		value, err := decodeValue(*wire.Old)
		if err != nil {
			return Event{}, err
		}
		event.Old = &value
	}
	if wire.New != nil {
		value, err := decodeValue(*wire.New)
		if err != nil {
			return Event{}, err
		}
		event.New = &value
	}
	return event, nil
}

func decodeValue(wire wireValue) (Value, error) {
	switch wire.Kind {
	case "string":
		var value string
		if err := json.Unmarshal(wire.Value, &value); err != nil {
			return Value{}, err
		}
		return Value{Kind: KindString, String: value}, nil
	case "bool":
		var value bool
		if err := json.Unmarshal(wire.Value, &value); err != nil {
			return Value{}, err
		}
		return Value{Kind: KindBool, Bool: value}, nil
	case "int":
		var value int64
		if err := json.Unmarshal(wire.Value, &value); err != nil {
			return Value{}, err
		}
		return Value{Kind: KindInt, Int: value}, nil
	case "duration":
		var text string
		if err := json.Unmarshal(wire.Value, &text); err != nil {
			return Value{}, err
		}
		duration, err := time.ParseDuration(text)
		if err != nil {
			return Value{}, err
		}
		return Value{Kind: KindDuration, Duration: duration}, nil
	case "string_list":
		var values []string
		if err := json.Unmarshal(wire.Value, &values); err != nil {
			return Value{}, err
		}
		return Value{Kind: KindStringList, Strings: values}, nil
	case "secret":
		// The audit log must never carry a secret value, not even the
		// "is set" flag. Treat a line that claims one as unreadable.
		return Value{}, errors.New("config: audit record contains a secret value")
	default:
		return Value{}, fmt.Errorf("config: unknown audit value kind %q", wire.Kind)
	}
}

// auditAppend is the package-level Audit path. It may run while a
// configuration change holds core.mu, so it reads the audit pointer under
// auditMu instead; Close clears that pointer under the same lock. append logs
// its own write failures. A closed log and a cleared pointer are both reported
// here, so an event produced during or after Close is never dropped silently.
func (c *core) auditAppend(e Event) {
	c.auditMu.RLock()
	log := c.audit
	c.auditMu.RUnlock()
	if log == nil {
		c.loggerOr().Error("config_audit_not_open", "audit_event", string(e.Event))
		return
	}
	if err := log.append(e); errors.Is(err, errAuditClosed) {
		c.loggerOr().Error("config_audit_closed", "audit_event", string(e.Event))
	}
}

func (c *core) loggerOr() *slog.Logger {
	if c.logger != nil {
		return c.logger
	}
	return defaultLogger()
}

// readAudit is the package-level ReadAudit path.
func (c *core) readAudit(since time.Time) ([]Event, error) {
	c.auditMu.RLock()
	log := c.audit
	c.auditMu.RUnlock()
	if log == nil {
		return nil, ErrNotLoaded
	}
	return log.read(since)
}
