package broker

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wangzw/peers/internal/broker/localpb"
	"github.com/wangzw/peers/internal/inbox"
)

// agent is one local agent. The socket path is its identity; every registration
// connection for that path is attached to the same agent. All fields except
// refreshMu are guarded by Broker.mu.
type agent struct {
	id         AgentID
	ref        string
	socketPath string // normalized
	caller     Caller // of the first registering connection
	backend    SessionBackend
	session    inbox.Session // last good record
	name       string        // normalized
	status     string        // verbatim from the record
	prevName   string
	prevNameAt time.Time
	prevTimer  Timer
	// lastAddress is the display address last pushed to the registration
	// streams. The address changes when the ref changes and also when the
	// number of agents carrying the name changes, so it is compared instead of
	// the ref alone.
	lastAddress string
	public      map[string]string // topic name -> description, joined publicly
	conns       map[*regConn]struct{}
	refreshMu   sync.Mutex

	// introduced reports whether Topics.AgentChanged has been called for this
	// agent at least once. The first call carries the initial roster entry and
	// is not mirrored to the registration streams.
	introduced bool
	// watchDir is the Broker.watches key this agent holds a reference on, and
	// watching says whether it holds one at all. The key carries the owner uid
	// and may have an empty directory (the account's default configuration
	// directory), so the flag is separate.
	watchDir watchKey
	watching bool

	// warnedPeerProtocol is the last session registry frame protocol version
	// logged as unrecognized; 0 means none has been logged.
	warnedPeerProtocol int
}

// regConn is one registration stream. Fields other than attemptMu are guarded
// by Broker.mu; attemptMu serializes the record lookup of this connection.
type regConn struct {
	caller     Caller
	rawPath    string
	id         AgentID
	socketPath string // normalized, empty when invalid
	configDir  string
	backend    SessionBackend
	release    func() // from SessionRouter.Acquire, called once when the stream ends
	agent      *agent // nil while waiting
	waitReason localpb.NotRegisteredReason
	waitDetail string // specific explanation for waitReason, "" when unknown
	attempted  bool   // attempt has stored waitReason; the timer must not report before it
	reportDue  bool   // the wait timer fired before the first attempt stored a reason
	reported   bool   // a not_registered has been sent for the current wait
	waitTimer  Timer
	watchDir   watchKey // Broker.watches key this connection holds; watching says whether it holds one
	watching   bool
	out        *eventQueue

	attemptMu sync.Mutex
	// retry asks the running attempt to reread the agent after it attaches: a
	// registry event arrived while the attempt was reading the record, or the
	// agent's watch was created while Broker.mu was released, so the record may
	// have changed between that read and the attachment.
	retry atomic.Bool
}

// dirWatch is one shared inbox.Watch registration for a configuration
// directory. creating is true while a Watch call is in flight so that another
// acquirer can wait for its result instead of starting a second watcher.
type dirWatch struct {
	stop     func()
	refs     int
	creating bool
	wait     chan struct{}
}

// agentSnapshot is the read-only view of an agent handed to RPC handlers.
type agentSnapshot struct {
	ID       AgentID
	Name     string
	Ref      string
	PrevName string
	Address  string
	Caller   Caller
	Session  inbox.Session
	Inbox    SessionBackend
}

// lookupAgent returns a snapshot of the agent with the given ID.
func (b *Broker) lookupAgent(id AgentID) (agentSnapshot, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a, ok := b.agents[id]
	if !ok {
		return agentSnapshot{}, false
	}
	return b.snapshotLocked(a), true
}

// allAgents returns snapshots of every local agent, sorted by ID.
func (b *Broker) allAgents() []agentSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]agentSnapshot, 0, len(b.agents))
	for _, a := range b.agents {
		out = append(out, b.snapshotLocked(a))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (b *Broker) snapshotLocked(a *agent) agentSnapshot {
	return agentSnapshot{
		ID:       a.id,
		Name:     a.name,
		Ref:      a.ref,
		PrevName: a.prevName,
		Address:  b.addressOf(a),
		Caller:   a.caller,
		Session:  a.session,
		Inbox:    a.backend,
	}
}

// onUnregister registers a callback that runs outside Broker.mu whenever an
// agent leaves the table.
func (b *Broker) onUnregister(f func(id AgentID)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.unregHooks = append(b.unregHooks, f)
}

// publicTopicSet returns the current public topic declarations as a topic name
// to description map, taking the first description seen in Agent-ID order.
func (b *Broker) publicTopicSet() map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := make([]AgentID, 0, len(b.agents))
	for id := range b.agents {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make(map[string]string)
	for _, id := range ids {
		for name, description := range b.agents[id].public {
			if _, ok := out[name]; !ok {
				out[name] = description
			}
		}
	}
	return out
}

// refreshAgents rereads the given agents in order. IDs that have left the table
// are skipped. The reread runs outside Broker.mu.
func (b *Broker) refreshAgents(ids []AgentID) {
	b.mu.Lock()
	fn := b.refreshOne
	agents := make([]*agent, 0, len(ids))
	for _, id := range ids {
		if a, ok := b.agents[id]; ok {
			agents = append(agents, a)
		}
	}
	b.mu.Unlock()
	for _, a := range agents {
		fn(a)
	}
}

// agentFor resolves the agent named by an RPC for the caller that carries ctx.
func (b *Broker) agentFor(ctx context.Context, id string) (*agent, error) {
	caller, ok := CallerFrom(ctx)
	if !ok {
		return nil, Errorf(localpb.ErrorCode_ERROR_CODE_INTERNAL, "no caller identified on this connection")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a, ok := b.agents[AgentID(id)]
	if !ok || a.caller.UID != caller.UID {
		return nil, Errorf(localpb.ErrorCode_ERROR_CODE_NOT_REGISTERED,
			"agent %q is not registered for this account", id)
	}
	return a, nil
}

// localAlias returns the Broker's effective alias. Config.Get fails once the
// configuration has been closed during shutdown, while registration handlers may
// still render addresses; the last valid value is kept so that those addresses
// never lose the alias. Callers must hold Broker.mu.
func (b *Broker) localAlias() string {
	v, err := b.deps.Config.Get("broker.alias")
	if err != nil {
		return b.lastAlias
	}
	if ValidAlias(v.String) {
		b.lastAlias = v.String
	}
	return v.String
}

// addressOf renders the address of a. Callers must hold Broker.mu.
func (b *Broker) addressOf(a *agent) string {
	return FormatAddress(a.name, a.ref, b.localAlias(), b.nameCountLocked(a.name) > 1)
}

// nameCountLocked counts the agents that carry name. Callers must hold
// Broker.mu.
func (b *Broker) nameCountLocked(name string) int {
	n := 0
	for _, a := range b.agents {
		if a.name == name {
			n++
		}
	}
	return n
}

// recomputeRefsLocked recalculates every agent's disambiguation ref. An agent
// seen for the first time is introduced to the topic table with its initial
// roster entry and does not get an agent_changed event. An existing agent whose
// display address changed is pushed to each of its registration streams, and an
// agent whose ref changed is also pushed to the topic table. The display address
// changes when the ref changes and when another agent starts or stops carrying
// the same name, which adds or removes the " [ref]" suffix. Callers must hold
// Broker.mu.
func (b *Broker) recomputeRefsLocked() {
	ids := make([]AgentID, 0, len(b.agents))
	for id := range b.agents {
		ids = append(ids, id)
	}
	refs := ComputeRefs(ids)
	for id, a := range b.agents {
		ref := refs[id]
		if !a.introduced {
			a.ref = ref
			a.introduced = true
			a.lastAddress = b.addressOf(a)
			b.deps.Topics.AgentChanged(id, a.name, a.prevName, ref, topicStatus(a.status))
			continue
		}
		refChanged := a.ref != ref
		a.ref = ref
		address := b.addressOf(a)
		if refChanged {
			b.deps.Topics.AgentChanged(id, a.name, a.prevName, ref, topicStatus(a.status))
		}
		if refChanged || address != a.lastAddress {
			b.pushAgentChangedLocked(a)
		}
	}
}

// unregisterLocked removes a from the table and clears its topic memberships.
// It returns the watch stops the caller must run outside Broker.mu. Callers
// must hold Broker.mu.
func (b *Broker) unregisterLocked(a *agent) []func() {
	delete(b.agents, a.id)
	b.deps.Topics.LeaveAll(a.id)

	names := make([]string, 0, len(a.public))
	for name := range a.public {
		names = append(names, name)
	}
	b.retireOrphanedPublicTopicsLocked(a, names)

	var stops []func()
	if a.prevTimer != nil {
		a.prevTimer.Stop()
		a.prevTimer = nil
	}
	stops = append(stops, b.releaseWatchLocked(a.watchDir, a.watching)...)
	a.watching = false
	a.watchDir = watchKey{}

	b.recomputeRefsLocked()
	b.gone = append(b.gone, a.id)
	b.log.Info(EvAgentUnregistered, "agent", shortID(a.id), "name", a.name)
	return stops
}

// retireOrphanedPublicTopicsLocked retires the public topics in names that no
// other agent joined publicly, then forgets them on a. Callers must hold
// Broker.mu.
func (b *Broker) retireOrphanedPublicTopicsLocked(a *agent, names []string) {
	for _, name := range names {
		if b.publicTopicInUseLocked(name) {
			continue
		}
		if b.deps.Directory != nil {
			b.deps.Directory.RetirePublicTopic(name)
		}
	}
	for _, name := range names {
		delete(a.public, name)
	}
}

func (b *Broker) publicTopicInUseLocked(name string) bool {
	for _, other := range b.agents {
		if _, ok := other.public[name]; ok {
			return true
		}
	}
	return false
}

// unlockAndNotify releases Broker.mu, runs the watch stops, then calls the
// unregistration hooks for agents that left the table. Callers must hold
// Broker.mu.
func (b *Broker) unlockAndNotify(stops []func()) {
	gone := b.gone
	b.gone = nil
	b.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
	for _, id := range gone {
		for _, f := range b.unregHooks {
			f(id)
		}
	}
}

// acquireWatchLocked takes a reference on the inbox watch of configDir for uid,
// creating the watch when it is the first reference. Both the ownership check
// and Watch run outside Broker.mu, and it returns with Broker.mu held again.
// The returned key is the Broker.watches key; ok is false when configDir does
// not belong to uid, or the watch could not be created, and no reference is
// held. Callers must hold Broker.mu.
func (b *Broker) acquireWatchLocked(uid uint32, configDir string, backend SessionBackend) (key watchKey, ok bool) {
	if v, verifies := backend.(configDirVerifier); verifies {
		// The check reads the account database and stats a path the caller
		// chose. Releasing Broker.mu here keeps a blocking path from stopping
		// every account's registration; the check is bounded so it cannot
		// block this caller forever either.
		b.mu.Unlock()
		err := verifyConfigDirBounded(b.ctx, uid, &b.configDirChecks, v, configDir)
		b.mu.Lock()
		if err != nil {
			b.log.Error(EvRegistryWatchFailed, "config_dir", configDir, "uid", uid, "error", err)
			return watchKey{}, false
		}
	}
	key = watchKeyFor(uid, configDir)
	for {
		if w, exists := b.watches[key]; exists {
			if w.creating {
				wait := w.wait
				b.mu.Unlock()
				<-wait
				b.mu.Lock()
				continue
			}
			w.refs++
			return key, true
		}

		w := &dirWatch{refs: 1, creating: true, wait: make(chan struct{})}
		b.watches[key] = w
		b.mu.Unlock()
		callCtx, cancel := sessionCallContext(b.ctx)
		stop, err := backend.Watch(callCtx, key.dir, func(pid int) {
			b.events.push(uid, key.dir, pid)
		})
		cancel()
		b.mu.Lock()
		w.creating = false
		if err != nil {
			delete(b.watches, key)
			close(w.wait)
			b.log.Error(EvRegistryWatchFailed, "config_dir", key.dir, "uid", uid, "error", err)
			return key, false
		}
		w.stop = stop
		close(w.wait)
		return key, true
	}
}

// releaseWatchLocked drops one reference on key. It returns the stop function
// when the last reference went away; the caller runs it outside Broker.mu.
// present says whether the caller actually holds a reference.
func (b *Broker) releaseWatchLocked(key watchKey, present bool) []func() {
	if !present {
		return nil
	}
	w, ok := b.watches[key]
	if !ok {
		return nil
	}
	w.refs--
	if w.refs > 0 {
		return nil
	}
	delete(b.watches, key)
	if w.stop != nil {
		return []func(){w.stop}
	}
	return nil
}

// topicStatus maps a session registry status to the value the topic table
// carries. Unknown values become the empty string.
func topicStatus(s string) string {
	switch s {
	case "idle", "busy", "waiting", "shell":
		return s
	default:
		return ""
	}
}

// statusToProto maps a session registry status to the local interface enum.
func statusToProto(s string) localpb.AgentStatus {
	switch s {
	case "idle":
		return localpb.AgentStatus_AGENT_STATUS_IDLE
	case "busy":
		return localpb.AgentStatus_AGENT_STATUS_BUSY
	case "waiting":
		return localpb.AgentStatus_AGENT_STATUS_WAITING
	case "shell":
		return localpb.AgentStatus_AGENT_STATUS_SHELL
	default:
		return localpb.AgentStatus_AGENT_STATUS_UNSPECIFIED
	}
}

func shortID(id AgentID) string {
	if len(id) > 12 {
		return string(id[:12])
	}
	return string(id)
}
