package mcpserver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/testkit/fakelocal"
)

// topicsTestClient dials a fake Local service without going through conn.go, so
// this work item depends only on the fake service and the state types.
func topicsTestClient(t *testing.T, sock string) localpb.LocalClient {
	t.Helper()
	cc, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("creating the gRPC client: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	return localpb.NewLocalClient(cc)
}

// topicsJoinRequests returns the JoinTopic requests the fake service received,
// in arrival order.
func topicsJoinRequests(srv *fakelocal.Server) []*localpb.JoinTopicRequest {
	var out []*localpb.JoinTopicRequest
	for _, call := range srv.Calls() {
		if call.Method == "JoinTopic" {
			out = append(out, call.Request.(*localpb.JoinTopicRequest))
		}
	}
	return out
}

// topicsLogger returns a logger that discards its output; the restorer only
// logs when it drops a topic.
func topicsLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestTopicMemory(t *testing.T) {
	var m topicMemory
	m.Remember(rememberedTopic{Name: "a", Description: "one"})
	m.Remember(rememberedTopic{Name: "b"})
	m.Remember(rememberedTopic{Name: "a", Public: true, Description: "two"})

	want := []rememberedTopic{
		{Name: "a", Public: true, Description: "two"},
		{Name: "b"},
	}
	if got := m.List(); !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %+v, want %+v", got, want)
	}

	// List must return a copy: mutating it must not change the memory.
	got := m.List()
	got[0].Name = "changed"
	if m.List()[0].Name != "a" {
		t.Fatalf("List() returned the internal slice")
	}

	m.Forget("a")
	if got := m.List(); !reflect.DeepEqual(got, []rememberedTopic{{Name: "b"}}) {
		t.Fatalf("after Forget(a): %+v", got)
	}
	m.Forget("missing") // must not panic
}

// REQ-topics-019: default topics are joined without a public declaration.
func TestRestorerFirstRegistration(t *testing.T) {
	sock := fakelocal.SocketPath(t)
	srv := fakelocal.Start(t, sock, fakelocal.Handlers{
		JoinTopic: func(ctx context.Context, req *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
			return &localpb.JoinTopicResponse{Topics: []string{req.GetName()}}, nil
		},
	})

	r := &restorer{
		mem: &topicMemory{},
		defaults: func(ctx context.Context, brokerAutoJoin bool) defaultLookup {
			return defaultLookup{topics: []string{"a", "b"}, settled: true}
		},
		log: topicsLogger(),
	}
	r.onComplete(context.Background(), topicsTestClient(t, sock), "agent-1", true)

	reqs := topicsJoinRequests(srv)
	if len(reqs) != 2 {
		t.Fatalf("got %d JoinTopic calls, want 2", len(reqs))
	}
	for i, name := range []string{"a", "b"} {
		req := reqs[i]
		if req.GetAgentId() != "agent-1" || req.GetName() != name {
			t.Errorf("request %d = {agent_id: %q, name: %q}, want {agent-1, %s}",
				i, req.GetAgentId(), req.GetName(), name)
		}
		if req.GetIsPublic() {
			t.Errorf("request %d for %q declared the topic public", i, name)
		}
	}
}

// The first registration and nameless topic calls read one computed result, so
// a project configuration or origin that changes afterwards does not change
// which topics a nameless call acts on.
func TestRestorerAndNamelessCallsShareTheDefaultList(t *testing.T) {
	sock := fakelocal.SocketPath(t)
	srv := fakelocal.Start(t, sock, fakelocal.Handlers{
		JoinTopic: func(ctx context.Context, req *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
			return &localpb.JoinTopicResponse{Topics: []string{req.GetName()}}, nil
		},
	})

	current := []string{"team"}
	reads := 0
	session := &sessionTopicList{compute: func(context.Context) defaultLookup {
		reads++
		return defaultLookup{topics: append([]string{}, current...), settled: true}
	}}
	r := &restorer{
		mem: &topicMemory{},
		defaults: func(ctx context.Context, autoJoin bool) defaultLookup {
			res := session.lookup(ctx)
			if !autoJoin || res.projectAutoJoinOff {
				res.topics = nil
			}
			return res
		},
		log: topicsLogger(),
	}
	client := topicsTestClient(t, sock)
	r.onComplete(context.Background(), client, "agent-1", true)
	if reqs := topicsJoinRequests(srv); len(reqs) != 1 || reqs[0].GetName() != "team" {
		t.Fatalf("first registration joined %v, want [team]", topicNames(reqs))
	}

	// The configuration changes, but the session keeps the list it registered
	// with.
	current = []string{"other"}
	if got := session.get(context.Background()); !reflect.DeepEqual(got, []string{"team"}) {
		t.Fatalf("nameless call list = %v, want the registration's [team]", got)
	}
	if reads != 1 {
		t.Fatalf("the lookup ran %d times, want once", reads)
	}
}

// REQ-topics-020: the default-topic callback sees the auto_join flag the Broker
// reported in registration_complete.
func TestRestorerDefaultsSeeAutoJoin(t *testing.T) {
	for _, autoJoin := range []bool{true, false} {
		t.Run(fmt.Sprintf("auto_join=%v", autoJoin), func(t *testing.T) {
			var got bool
			r := &restorer{
				mem: &topicMemory{},
				defaults: func(ctx context.Context, brokerAutoJoin bool) defaultLookup {
					got = brokerAutoJoin
					return defaultLookup{settled: true}
				},
				log: topicsLogger(),
			}
			// defaults returns nothing, so no RPC reaches the nil client.
			r.onComplete(context.Background(), nil, "agent-1", autoJoin)
			if got != autoJoin {
				t.Fatalf("defaults saw brokerAutoJoin=%v, want %v", got, autoJoin)
			}
		})
	}
}

// A first registration that is interrupted while the defaults are computed must
// not mark them as done: the lookup ran with a cancelled context (the git call
// then fails), so the memory holds nothing and the next registration has to
// compute the defaults again.
func TestRestorerInterruptedFirstRegistrationKeepsDefaults(t *testing.T) {
	sock := fakelocal.SocketPath(t)
	srv := fakelocal.Start(t, sock, fakelocal.Handlers{
		JoinTopic: func(ctx context.Context, req *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
			return &localpb.JoinTopicResponse{Topics: []string{req.GetName()}}, nil
		},
	})
	mem := &topicMemory{}
	defaultCalls := 0
	r := &restorer{
		mem: mem,
		defaults: func(ctx context.Context, autoJoin bool) defaultLookup {
			defaultCalls++
			if ctx.Err() != nil {
				// What the lookup returns when the git command is cancelled.
				return defaultLookup{settled: false}
			}
			return defaultLookup{topics: []string{"a"}, settled: true}
		},
		log: topicsLogger(),
	}
	client := topicsTestClient(t, sock)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	r.onComplete(cancelled, client, "agent-1", true)

	if got := mem.List(); len(got) != 0 {
		t.Fatalf("memory after the interrupted registration = %+v, want empty", got)
	}

	r.onComplete(context.Background(), client, "agent-1", true)

	reqs := topicsJoinRequests(srv)
	if len(reqs) != 1 || reqs[0].GetName() != "a" {
		t.Fatalf("the second registration joined %v, want [a]; defaults called %d times",
			topicNames(reqs), defaultCalls)
	}
}

// topicNames returns the names of the JoinTopic requests, for error messages.
func topicNames(reqs []*localpb.JoinTopicRequest) []string {
	out := make([]string, len(reqs))
	for i, req := range reqs {
		out[i] = req.GetName()
	}
	return out
}

// REQ-topics-022, REQ-broker-config-037: a later registration restores the
// memory, including the public declaration and description, and does not join
// the default topics again.
func TestRestorerSecondRegistrationRestoresMemory(t *testing.T) {
	sock := fakelocal.SocketPath(t)
	srv := fakelocal.Start(t, sock, fakelocal.Handlers{
		JoinTopic: func(ctx context.Context, req *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
			return &localpb.JoinTopicResponse{Topics: []string{req.GetName()}}, nil
		},
	})

	mem := &topicMemory{}
	defaultsCalls := 0
	r := &restorer{
		mem: mem,
		defaults: func(ctx context.Context, brokerAutoJoin bool) defaultLookup {
			defaultsCalls++
			return defaultLookup{topics: []string{"a"}, settled: true}
		},
		log: topicsLogger(),
	}
	client := topicsTestClient(t, sock)

	r.onComplete(context.Background(), client, "agent-1", true)
	mem.Forget("a")
	mem.Remember(rememberedTopic{Name: "x", Public: true, Description: "d"})

	before := len(topicsJoinRequests(srv))
	r.onComplete(context.Background(), client, "agent-1", true)

	reqs := topicsJoinRequests(srv)[before:]
	if len(reqs) != 1 {
		t.Fatalf("second registration sent %d JoinTopic calls, want only x", len(reqs))
	}
	req := reqs[0]
	if req.GetName() != "x" || !req.GetIsPublic() || req.GetDescription() != "d" {
		t.Fatalf("restored request = {name: %q, public: %v, description: %q}, want {x, true, d}",
			req.GetName(), req.GetIsPublic(), req.GetDescription())
	}
	if defaultsCalls != 1 {
		t.Fatalf("defaults called %d times, want once", defaultsCalls)
	}
}

// mcpserver design line 108: a public topic the Broker rejects is joined again
// without the public declaration and stays remembered as private.
func TestRestorerPublicFallback(t *testing.T) {
	sock := fakelocal.SocketPath(t)
	srv := fakelocal.Start(t, sock, fakelocal.Handlers{
		JoinTopic: func(ctx context.Context, req *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
			if req.GetName() == "x" && req.GetIsPublic() {
				return nil, fakelocal.ErrorStatus(codes.FailedPrecondition, &localpb.ErrorDetail{
					Code:    localpb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
					Message: "broker is not public",
				})
			}
			return &localpb.JoinTopicResponse{Topics: []string{req.GetName()}}, nil
		},
	})

	mem := &topicMemory{}
	r := &restorer{
		mem:      mem,
		defaults: func(ctx context.Context, brokerAutoJoin bool) defaultLookup { return defaultLookup{settled: true} },
		log:      topicsLogger(),
	}
	client := topicsTestClient(t, sock)

	r.onComplete(context.Background(), client, "agent-1", true)
	mem.Remember(rememberedTopic{Name: "x", Public: true, Description: "d"})
	r.onComplete(context.Background(), client, "agent-1", true)

	reqs := topicsJoinRequests(srv)
	if len(reqs) != 2 {
		t.Fatalf("got %d JoinTopic calls, want 2 (public, then private)", len(reqs))
	}
	if !reqs[0].GetIsPublic() || reqs[1].GetIsPublic() {
		t.Fatalf("is_public sequence = %v, %v; want true, false", reqs[0].GetIsPublic(), reqs[1].GetIsPublic())
	}
	if reqs[1].GetName() != "x" || reqs[1].GetDescription() != "d" {
		t.Fatalf("fallback request = {name: %q, description: %q}, want {x, d}",
			reqs[1].GetName(), reqs[1].GetDescription())
	}
	if got := mem.List(); len(got) != 1 || got[0].Name != "x" || got[0].Public {
		t.Fatalf("memory after fallback = %+v, want x remembered as private", got)
	}
}

// A topic the Broker rejects with an ErrorDetail and which cannot be joined
// privately either is dropped from the memory.
func TestRestorerForgetsTopicOnDetailError(t *testing.T) {
	sock := fakelocal.SocketPath(t)
	srv := fakelocal.Start(t, sock, fakelocal.Handlers{
		JoinTopic: func(ctx context.Context, req *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
			if req.GetName() == "y" {
				return nil, fakelocal.ErrorStatus(codes.FailedPrecondition, &localpb.ErrorDetail{
					Code:    localpb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
					Message: "unknown topic",
				})
			}
			return &localpb.JoinTopicResponse{Topics: []string{req.GetName()}}, nil
		},
	})

	mem := &topicMemory{}
	r := &restorer{
		mem:      mem,
		defaults: func(ctx context.Context, brokerAutoJoin bool) defaultLookup { return defaultLookup{settled: true} },
		log:      topicsLogger(),
	}
	client := topicsTestClient(t, sock)

	r.onComplete(context.Background(), client, "agent-1", true)
	mem.Remember(rememberedTopic{Name: "y"})
	r.onComplete(context.Background(), client, "agent-1", true)

	if got := mem.List(); len(got) != 0 {
		t.Fatalf("memory after a rejected join = %+v, want empty", got)
	}
	if reqs := topicsJoinRequests(srv); len(reqs) != 1 {
		t.Fatalf("got %d JoinTopic calls, want 1 (a private topic is not retried)", len(reqs))
	}
}

// An error without an ErrorDetail means the connection broke; the entry stays
// for the next registration.
func TestRestorerKeepsTopicOnConnectionError(t *testing.T) {
	sock := fakelocal.SocketPath(t)
	fakelocal.Start(t, sock, fakelocal.Handlers{
		JoinTopic: func(ctx context.Context, req *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
			if req.GetName() == "z" {
				return nil, status.Error(codes.Unavailable, "connection lost")
			}
			return &localpb.JoinTopicResponse{Topics: []string{req.GetName()}}, nil
		},
	})

	mem := &topicMemory{}
	r := &restorer{
		mem:      mem,
		defaults: func(ctx context.Context, brokerAutoJoin bool) defaultLookup { return defaultLookup{settled: true} },
		log:      topicsLogger(),
	}
	client := topicsTestClient(t, sock)

	r.onComplete(context.Background(), client, "agent-1", true)
	mem.Remember(rememberedTopic{Name: "z"})
	r.onComplete(context.Background(), client, "agent-1", true)

	if got := mem.List(); len(got) != 1 || got[0].Name != "z" {
		t.Fatalf("memory after a connection error = %+v, want z kept", got)
	}
}

// topicsRaceHandlers builds handlers whose session is registered and whose
// topic recorder is the same memory the restorer uses, so the tool calls and
// the restore contend for the same serialization.
func topicsRaceHandlers(client localpb.LocalClient, mem *topicMemory) *handlers {
	st := &statusStore{}
	st.Update(func(s *Status) {
		s.Phase = PhaseConnected
		s.Client = client
		s.AgentID = "agent-1"
		s.Address = "peer@hostA"
	})
	return &handlers{st: st, topics: mem, log: topicsLogger()}
}

// waitClosed waits up to three seconds for done to be closed.
func waitClosed(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not finish within 3s", what)
	}
}

// waitErr waits up to three seconds for an error from a call goroutine.
func waitErr(t *testing.T, done <-chan error, what string) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(3 * time.Second):
		t.Fatalf("%s did not return within 3s", what)
		return nil
	}
}

// A leave_topic that runs while a restore is stuck joining a must not be
// undone by the restore joining the left topic from its old snapshot. The tool
// and the restore serialize their Broker call and memory write through the
// same lock, so whatever order they take once the stuck join returns, b is
// left in the end.
func TestRestorerLeaveRace(t *testing.T) {
	sock := fakelocal.SocketPath(t)

	var mu sync.Mutex
	joined := map[string]bool{}
	var order []string
	add := func(name string) {
		if !joined[name] {
			joined[name] = true
			order = append(order, name)
		}
	}
	remove := func(name string) {
		if !joined[name] {
			return
		}
		delete(joined, name)
		for i, n := range order {
			if n == name {
				order = append(order[:i], order[i+1:]...)
				return
			}
		}
	}
	current := func() []string { return append([]string{}, order...) }

	bJoinEntered := make(chan struct{})
	releaseB := make(chan struct{})

	fakelocal.Start(t, sock, fakelocal.Handlers{
		JoinTopic: func(ctx context.Context, req *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
			// The restore joins a at once and blocks on b, holding the topic
			// lock while the leave below is issued.
			if req.GetName() == "b" {
				close(bJoinEntered)
				<-releaseB
			}
			mu.Lock()
			add(req.GetName())
			topics := current()
			mu.Unlock()
			return &localpb.JoinTopicResponse{Topics: topics}, nil
		},
		LeaveTopic: func(ctx context.Context, req *localpb.LeaveTopicRequest) (*localpb.LeaveTopicResponse, error) {
			// A real Broker applies the leave when it receives the request.
			mu.Lock()
			remove(req.GetName())
			topics := current()
			mu.Unlock()
			return &localpb.LeaveTopicResponse{Topics: topics}, nil
		},
		ListTopics: func(ctx context.Context, req *localpb.ListTopicsRequest) (*localpb.ListTopicsResponse, error) {
			mu.Lock()
			topics := current()
			mu.Unlock()
			return &localpb.ListTopicsResponse{JoinedTopics: topics}, nil
		},
	})
	client := topicsTestClient(t, sock)

	mem := &topicMemory{}
	r := &restorer{
		mem:      mem,
		defaults: func(context.Context, bool) defaultLookup { return defaultLookup{settled: true} },
		log:      topicsLogger(),
	}
	// The first registration joins nothing; the second restores a and b in
	// that order.
	r.onComplete(context.Background(), client, "agent-1", true)
	mem.Remember(rememberedTopic{Name: "a"})
	mem.Remember(rememberedTopic{Name: "b"})
	h := topicsRaceHandlers(client, mem)

	restoreDone := make(chan struct{})
	go func() {
		defer close(restoreDone)
		r.onComplete(context.Background(), client, "agent-1", true)
	}()
	<-bJoinEntered

	leaveDone := make(chan error, 1)
	go func() {
		_, err := h.LeaveTopic(context.Background(), LeaveTopicIn{Name: "b"})
		leaveDone <- err
	}()
	close(releaseB)

	waitClosed(t, restoreDone, "the restore")
	if err := waitErr(t, leaveDone, "leave_topic"); err != nil {
		t.Fatalf("leave_topic: %v", err)
	}

	mu.Lock()
	isMember := joined["b"]
	mu.Unlock()
	if isMember {
		t.Fatal("the restore rejoined b after leave_topic removed it")
	}

	out, err := h.ListTopics(context.Background(), NoArgs{})
	if err != nil {
		t.Fatalf("list_topics: %v", err)
	}
	for _, name := range out.Joined {
		if name == "b" {
			t.Fatalf("list_topics still lists b: %v", out.Joined)
		}
	}
}

// A join_topic that changes a topic while the restore is stuck joining it must
// keep its new description; the restore's own retry must not overwrite it.
func TestRestorerJoinRace(t *testing.T) {
	sock := fakelocal.SocketPath(t)

	publicEntered := make(chan struct{})
	releasePublic := make(chan struct{})
	fakelocal.Start(t, sock, fakelocal.Handlers{
		JoinTopic: func(ctx context.Context, req *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
			if req.GetName() == "a" && req.GetIsPublic() {
				close(publicEntered)
				<-releasePublic
				return nil, fakelocal.ErrorStatus(codes.InvalidArgument, &localpb.ErrorDetail{
					Code:    localpb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
					Message: "this Broker is not public; make it public first",
				})
			}
			return &localpb.JoinTopicResponse{Topics: []string{req.GetName()}}, nil
		},
	})
	client := topicsTestClient(t, sock)

	mem := &topicMemory{}
	r := &restorer{
		mem:      mem,
		defaults: func(context.Context, bool) defaultLookup { return defaultLookup{settled: true} },
		log:      topicsLogger(),
	}
	r.onComplete(context.Background(), client, "agent-1", true)
	mem.Remember(rememberedTopic{Name: "a", Public: true, Description: "old"})
	h := topicsRaceHandlers(client, mem)

	restoreDone := make(chan struct{})
	go func() {
		defer close(restoreDone)
		r.onComplete(context.Background(), client, "agent-1", true)
	}()
	<-publicEntered

	joinDone := make(chan error, 1)
	go func() {
		_, err := h.JoinTopic(context.Background(), JoinTopicIn{Name: "a", Description: "new"})
		joinDone <- err
	}()

	// The restore holds the lock from before the public join until after its
	// private retry, so the join below can only run once the retry is done.
	close(releasePublic)

	waitClosed(t, restoreDone, "the restore")
	if err := waitErr(t, joinDone, "join_topic"); err != nil {
		t.Fatalf("join_topic: %v", err)
	}

	var got rememberedTopic
	found := false
	for _, topic := range mem.List() {
		if topic.Name == "a" {
			got = topic
			found = true
		}
	}
	if !found {
		t.Fatal("the memory no longer holds a")
	}
	if got.Description != "new" {
		t.Fatalf("remembered description = %q, want %q", got.Description, "new")
	}
}

// A restore whose Broker call never answers releases the topic lock after its
// per-topic bound, so a later registration is not stuck behind it.
func TestRestoreReleasesTheTopicLockAfterItsBound(t *testing.T) {
	sock := fakelocal.SocketPath(t)
	var calls atomic.Int32
	entered := make(chan struct{})
	fakelocal.Start(t, sock, fakelocal.Handlers{
		JoinTopic: func(ctx context.Context, req *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
			if calls.Add(1) == 1 {
				close(entered)
				// The first call is never answered until its context ends.
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return &localpb.JoinTopicResponse{Topics: []string{req.GetName()}}, nil
		},
	})
	client := topicsTestClient(t, sock)

	mem := &topicMemory{}
	r := &restorer{
		mem:        mem,
		defaults:   func(context.Context, bool) defaultLookup { return defaultLookup{settled: true} },
		log:        topicsLogger(),
		rpcTimeout: 100 * time.Millisecond,
	}
	r.onComplete(context.Background(), client, "agent-1", true) // the first registration joins nothing
	mem.Remember(rememberedTopic{Name: "a"})

	streamCtx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		r.onComplete(streamCtx, client, "agent-1", true)
	}()
	<-entered

	// The Broker answers again; a registration that follows must not wait for
	// the wedged restore forever.
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		r.onComplete(context.Background(), client, "agent-1", true)
	}()
	waitClosed(t, secondDone, "the restore after the Broker recovered")
	waitClosed(t, firstDone, "the wedged restore")
}

// Remember, Forget and List may run while a registration restore is in flight;
// the race detector must not report a data race.
func TestTopicMemoryConcurrentAccess(t *testing.T) {
	mem := &topicMemory{}
	sock := fakelocal.SocketPath(t)
	fakelocal.Start(t, sock, fakelocal.Handlers{
		JoinTopic: func(ctx context.Context, req *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
			return &localpb.JoinTopicResponse{Topics: []string{req.GetName()}}, nil
		},
	})
	client := topicsTestClient(t, sock)
	r := &restorer{
		mem: mem,
		defaults: func(ctx context.Context, brokerAutoJoin bool) defaultLookup {
			return defaultLookup{topics: []string{"a", "b"}, settled: true}
		},
		log: topicsLogger(),
	}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.onComplete(context.Background(), client, "agent-1", true)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 100; j++ {
			mem.Remember(rememberedTopic{Name: fmt.Sprintf("t%d", j%7), Public: j%2 == 0})
			mem.Forget(fmt.Sprintf("t%d", j%5))
			_ = mem.List()
		}
	}()
	wg.Wait()
}
