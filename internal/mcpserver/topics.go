package mcpserver

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/wangzw/peers/internal/broker/localpb"
)

const (
	// topicRPCTimeout bounds the whole of one topic tool call, including every
	// Broker call a nameless call makes for its default topics, so the call
	// returns instead of holding the topic lock while the Broker does not
	// answer.
	topicRPCTimeout = 10 * time.Second
	// topicRestoreTimeout bounds the restore of one remembered topic,
	// including the fallback join made without a public declaration.
	topicRestoreTimeout = 10 * time.Second
)

// topicMemory remembers the topics this process joined, in insertion order. A
// later registration restores the membership from it.
type topicMemory struct {
	// mu protects topics.
	mu sync.Mutex
	// ops is a one-slot semaphore that serializes a Broker membership call with
	// the memory write it causes. The topic tools and the restorer both take
	// it, so a join or leave cannot interleave with the restore of the same
	// topic. A semaphore, not a mutex, lets an acquisition wait on a context:
	// a call stops instead of queueing behind a stuck one.
	ops     chan struct{}
	opsOnce sync.Once
	topics  []rememberedTopic
}

// lockTopic and unlockTopic serialize a Broker membership call and the memory
// write it causes. The topic tools and the restorer hold this lock across
// joining or leaving one topic. lockTopic returns the context's error instead
// of waiting when ctx ends first.
func (m *topicMemory) lockTopic(ctx context.Context) error {
	m.opsOnce.Do(func() { m.ops = make(chan struct{}, 1) })
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case m.ops <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *topicMemory) unlockTopic() { <-m.ops }

// get returns the remembered entry with the given name.
func (m *topicMemory) get(name string) (rememberedTopic, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.topics {
		if t.Name == name {
			return t, true
		}
	}
	return rememberedTopic{}, false
}

// Remember replaces the entry with the same name in place, or appends a new one.
func (m *topicMemory) Remember(t rememberedTopic) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.topics {
		if m.topics[i].Name == t.Name {
			m.topics[i] = t
			return
		}
	}
	m.topics = append(m.topics, t)
}

// Forget drops the entry with the given name, if any.
func (m *topicMemory) Forget(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.topics {
		if m.topics[i].Name == name {
			m.topics = append(m.topics[:i], m.topics[i+1:]...)
			return
		}
	}
}

// List returns a copy of the remembered topics in insertion order.
func (m *topicMemory) List() []rememberedTopic {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]rememberedTopic, len(m.topics))
	copy(out, m.topics)
	return out
}

// restorer joins the default topics after the first registration and restores
// the remembered topics after every registration. onComplete matches the
// completeFunc signature; holding mu keeps two completions from interleaving.
type restorer struct {
	mem      *topicMemory
	defaults func(ctx context.Context, brokerAutoJoin bool) defaultLookup
	log      *slog.Logger
	// rpcTimeout bounds the restore of one topic. Zero means
	// topicRestoreTimeout; tests set a short value.
	rpcTimeout time.Duration

	mu          sync.Mutex
	initialized bool
}

// onComplete joins the default topics on the first call, then restores every
// remembered topic. It stops when ctx is done.
func (r *restorer) onComplete(ctx context.Context, c localpb.LocalClient, agentID string, autoJoin bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// The first registration computes the defaults once; a later one restores
	// what the memory holds, which already includes them.
	if !r.initialized {
		res := r.defaults(ctx, autoJoin)
		// An interrupted registration may have computed the defaults with a
		// cancelled context, in which case the lookup failed and the memory is
		// incomplete; there is nothing to restore on this connection either.
		if ctx.Err() != nil {
			return
		}
		// Record the topics before joining them, so an interrupted join is
		// restored by the next registration.
		for _, name := range res.topics {
			r.mem.Remember(rememberedTopic{Name: name})
		}
		// A lookup that did not settle, such as a git command that timed out,
		// leaves the defaults unknown. Leave initialized false so the next
		// registration computes them again instead of losing them for the life
		// of the process.
		if res.settled {
			r.initialized = true
		}
	}

	for _, t := range r.mem.List() {
		if ctx.Err() != nil {
			return
		}
		if !r.restoreOne(ctx, c, agentID, t.Name) {
			return
		}
	}
}

// restoreOne joins one remembered topic under the topic lock. It reads the
// memory again after taking the lock: a tool may have left the topic since the
// snapshot, in which case it is skipped, or changed its public declaration or
// description, in which case the memory wins. It reports whether the restore
// ran; a context that ended while waiting for the lock stops the loop.
func (r *restorer) restoreOne(ctx context.Context, c localpb.LocalClient, agentID, name string) bool {
	if err := r.mem.lockTopic(ctx); err != nil {
		return false
	}
	defer r.mem.unlockTopic()

	t, ok := r.mem.get(name)
	if !ok {
		return true
	}
	timeout := r.rpcTimeout
	if timeout <= 0 {
		timeout = topicRestoreTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	r.restore(ctx, c, agentID, t)
	return true
}

// restore joins one remembered topic. A topic the Broker rejects is joined
// once more without its public declaration; if that also fails because the
// Broker answered with an ErrorDetail, the topic is dropped from the memory.
// An error without an ErrorDetail means the connection broke, so the entry
// stays for the next registration.
func (r *restorer) restore(ctx context.Context, c localpb.LocalClient, agentID string, t rememberedTopic) {
	_, err := c.JoinTopic(ctx, joinTopicRequest(agentID, t))
	if err == nil {
		return
	}
	if errorDetail(err) == nil {
		return
	}
	if t.Public {
		t.Public = false
		_, retryErr := c.JoinTopic(ctx, joinTopicRequest(agentID, t))
		if retryErr == nil {
			r.mem.Remember(t)
			return
		}
		if errorDetail(retryErr) == nil {
			return
		}
		err = retryErr
	}
	r.mem.Forget(t.Name)
	if r.log != nil {
		r.log.Warn("mcpserver: dropping a topic that could not be restored", "topic", t.Name, "error", err)
	}
}

// joinTopicRequest builds the Local request that joins t as agentID.
func joinTopicRequest(agentID string, t rememberedTopic) *localpb.JoinTopicRequest {
	return &localpb.JoinTopicRequest{
		AgentId:     agentID,
		Name:        t.Name,
		IsPublic:    t.Public,
		Description: t.Description,
	}
}
