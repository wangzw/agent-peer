package broker

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/wangzw/peers/internal/inbox"
	"github.com/wangzw/peers/internal/testkit/fakesession"
)

// fakeAgents is a localAgents fake backed by a map of agentViews. Its Reread
// mirrors the parts of Broker.rereadAgent the delivery path depends on: an
// unknown or vanished record is errAgentGone, a partial record keeps the last
// good snapshot, and a complete record replaces the snapshot with the
// normalized name the record carries.
type fakeAgents struct {
	mu   sync.Mutex
	byID map[string]agentView
	// rereadErr forces the result of Reread for an ID: a non-nil error is
	// handled like the matching inbox.Reread result, and nil reports a
	// successful reread that returns the current snapshot unchanged.
	rereadErr map[string]error
}

// newFakeAgents returns a fakeAgents holding views, keyed by ID.
func newFakeAgents(views ...agentView) *fakeAgents {
	f := &fakeAgents{byID: make(map[string]agentView, len(views))}
	for _, view := range views {
		f.byID[view.ID] = view
	}
	return f
}

func (f *fakeAgents) Lookup(id string) (agentView, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	view, ok := f.byID[id]
	return view, ok
}

// All returns the snapshots sorted by ID, like Broker.allAgents.
func (f *fakeAgents) All() []agentView {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]agentView, 0, len(f.byID))
	for _, view := range f.byID {
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (f *fakeAgents) Reread(id string) (agentView, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	view, ok := f.byID[id]
	if !ok {
		return agentView{}, errAgentGone
	}
	if err, forced := f.rereadErr[id]; forced {
		if err == nil {
			return view, nil
		}
		return f.applyRereadLocked(id, view, inbox.Session{}, err)
	}
	next, err := inbox.Reread(view.Session)
	return f.applyRereadLocked(id, view, next, err)
}

// applyRereadLocked stores the reread result the way Broker.refreshAgent
// treats it. Callers must hold f.mu.
func (f *fakeAgents) applyRereadLocked(id string, view agentView, next inbox.Session, err error) (agentView, error) {
	switch {
	case err == nil:
		view.Session = next
		view.Name = inbox.NormalizeName(next.Name)
		f.byID[id] = view
		return view, nil
	case errors.Is(err, inbox.ErrPartial):
		return view, nil
	case errors.Is(err, inbox.ErrNoRecord):
		delete(f.byID, id)
		return agentView{}, errAgentGone
	default:
		return agentView{}, err
	}
}

// testAgent builds an empty-table snapshot for socketPath. The ID is derived
// the way the Broker derives Agent IDs, and the ref is the shortest prefix the
// table assigns to a unique name. Session carries only the socket path; use
// liveTestAgent when the record must be readable.
func testAgent(name, socketPath string) agentView {
	sum := sha256.Sum256([]byte("session:" + socketPath))
	id := hex.EncodeToString(sum[:])
	return agentView{
		ID:      id,
		Name:    inbox.NormalizeName(name),
		Ref:     id[:6],
		Session: inbox.Session{SocketPath: socketPath},
		Inbox:   inboxPackage{},
	}
}

// liveTestAgent returns testAgent for a running fakesession, with Session
// replaced by the record inbox.Lookup reads so that fakeAgents.Reread can
// follow the record.
func liveTestAgent(t *testing.T, fake *fakesession.Fake, name string) agentView {
	t.Helper()
	view := testAgent(name, fake.SocketPath())
	session, err := inbox.Lookup(fake.ConfigDir(), fake.SocketPath())
	if err != nil {
		t.Fatalf("inbox.Lookup: %v", err)
	}
	view.Session = session
	return view
}

var _ localAgents = (*fakeAgents)(nil)

// TestFakeAgentsRereadGone checks that the fake reports a removed record the
// same way the Broker does. A fake that kept returning the last snapshot would
// make the delivery tests that depend on an Agent disappearing pass for the
// wrong reason.
func TestFakeAgentsRereadGone(t *testing.T) {
	fake := fakesession.New(t, fakesession.Options{Name: "api-worker"})
	view := liveTestAgent(t, fake, "api-worker")
	agents := newFakeAgents(view)

	fake.RemoveRecord()

	if _, err := agents.Reread(view.ID); !errors.Is(err, errAgentGone) {
		t.Fatalf("Reread after RemoveRecord = %v, want errAgentGone", err)
	}
}

// TestFakeAgentsRereadRenamed checks that the fake applies the record's new
// name through inbox.NormalizeName, as the Broker does. A fake that kept the
// old name would hide rename bugs in the delivery tests.
func TestFakeAgentsRereadRenamed(t *testing.T) {
	fake := fakesession.New(t, fakesession.Options{Name: "api-worker"})
	view := liveTestAgent(t, fake, "api-worker")
	agents := newFakeAgents(view)

	fake.Rename("API Worker")

	got, err := agents.Reread(view.ID)
	if err != nil {
		t.Fatalf("Reread after rename: %v", err)
	}
	if got.Name != "api-worker" {
		t.Fatalf("Name after rename = %q, want %q", got.Name, "api-worker")
	}
	if got.Session.Name != "API Worker" {
		t.Fatalf("Session.Name after rename = %q, want the record value %q", got.Session.Name, "API Worker")
	}
}
