package mcpserver

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/testkit/fakelocal"
)

// topicRecorderStub is a topicRecorder that records the calls it receives.
type topicRecorderStub struct {
	remembered []rememberedTopic
	forgotten  []string
}

func (r *topicRecorderStub) Remember(t rememberedTopic)      { r.remembered = append(r.remembered, t) }
func (r *topicRecorderStub) Forget(name string)              { r.forgotten = append(r.forgotten, name) }
func (r *topicRecorderStub) lockTopic(context.Context) error { return nil }
func (r *topicRecorderStub) unlockTopic()                    {}

// newTopicsHandlers builds handlers whose status is PhaseConnected and whose
// client talks to a fresh fakelocal server. agentID may be empty to simulate a
// connected-but-not-registered session.
func newTopicsHandlers(t *testing.T, h fakelocal.Handlers, agentID string) (*handlers, *fakelocal.Server, *topicRecorderStub) {
	t.Helper()
	path := fakelocal.SocketPath(t)
	srv := fakelocal.Start(t, path, h)

	cc, err := grpc.NewClient("unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dialing the fake Local service: %v", err)
	}
	t.Cleanup(func() { _ = cc.Close() })

	rec := &topicRecorderStub{}
	st := &statusStore{}
	st.Update(func(s *Status) {
		s.Phase = PhaseConnected
		s.Client = localpb.NewLocalClient(cc)
		s.AgentID = agentID
		s.Address = "peer@hostA"
	})
	return &handlers{st: st, topics: rec}, srv, rec
}

// lastTopicCall returns the only unary request the fake server received, failing the
// test when there is not exactly one.
func lastTopicCall(t *testing.T, srv *fakelocal.Server) fakelocal.Call {
	t.Helper()
	calls := srv.Calls()
	if len(calls) != 1 {
		t.Fatalf("fake server received %d calls, want 1: %v", len(calls), calls)
	}
	return calls[0]
}

func TestJoinTopic(t *testing.T) {
	t.Run("forwards the request and remembers the topic", func(t *testing.T) {
		// REQ-topics-001, REQ-topics-010: join_topic forwards the name, the
		// public declaration and the description.
		h, srv, rec := newTopicsHandlers(t, fakelocal.Handlers{
			JoinTopic: func(context.Context, *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
				return &localpb.JoinTopicResponse{Topics: []string{"t", "u"}}, nil
			},
		}, "agent-1")

		out, err := h.JoinTopic(context.Background(), JoinTopicIn{Name: "t", Public: true, Description: "d"})
		if err != nil {
			t.Fatalf("JoinTopic: %v", err)
		}
		if want := []string{"t", "u"}; !reflect.DeepEqual(out.Topics, want) {
			t.Errorf("Topics = %v, want %v", out.Topics, want)
		}

		call := lastTopicCall(t, srv)
		if call.Method != "JoinTopic" {
			t.Fatalf("method = %q, want JoinTopic", call.Method)
		}
		req, ok := call.Request.(*localpb.JoinTopicRequest)
		if !ok {
			t.Fatalf("request type %T, want *localpb.JoinTopicRequest", call.Request)
		}
		if req.GetAgentId() != "agent-1" {
			t.Errorf("agent_id = %q, want agent-1", req.GetAgentId())
		}
		if req.GetName() != "t" {
			t.Errorf("name = %q, want t", req.GetName())
		}
		if !req.GetIsPublic() {
			t.Error("is_public = false, want true")
		}
		if req.GetDescription() != "d" {
			t.Errorf("description = %q, want d", req.GetDescription())
		}

		want := []rememberedTopic{{Name: "t", Public: true, Description: "d"}}
		if !reflect.DeepEqual(rec.remembered, want) {
			t.Errorf("remembered = %+v, want %+v", rec.remembered, want)
		}
	})

	t.Run("a failed public join is not remembered", func(t *testing.T) {
		// REQ-topics-023: a public declaration on a non-public Broker must fail
		// without being remembered, or the next registration would join it
		// silently.
		h, _, rec := newTopicsHandlers(t, fakelocal.Handlers{
			JoinTopic: func(context.Context, *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
				return nil, fakelocal.ErrorStatus(codes.InvalidArgument, &localpb.ErrorDetail{
					Code:    localpb.ErrorCode_ERROR_CODE_INVALID_ARGUMENT,
					Message: "this Broker is not public; make it public first",
				})
			},
		}, "agent-1")

		_, err := h.JoinTopic(context.Background(), JoinTopicIn{Name: "t", Public: true, Description: "d"})
		if err == nil {
			t.Fatal("JoinTopic returned no error, want one")
		}
		if !strings.HasPrefix(err.Error(), "join_topic failed:") {
			t.Errorf("error = %q, want the prefix %q", err, "join_topic failed:")
		}
		if !strings.Contains(err.Error(), "this Broker is not public; make it public first") {
			t.Errorf("error = %q, want the Broker's message verbatim", err)
		}
		if len(rec.remembered) != 0 {
			t.Errorf("remembered = %+v, want nothing", rec.remembered)
		}
	})

	t.Run("not registered", func(t *testing.T) {
		h, srv, rec := newTopicsHandlers(t, fakelocal.Handlers{}, "")

		_, err := h.JoinTopic(context.Background(), JoinTopicIn{Name: "t"})
		if err == nil {
			t.Fatal("JoinTopic returned no error, want not_registered")
		}
		if !strings.Contains(err.Error(), "(code: not_registered)") {
			t.Errorf("error = %q, want code not_registered", err)
		}
		if calls := srv.Calls(); len(calls) != 0 {
			t.Errorf("fake server received %d calls, want none: %v", len(calls), calls)
		}
		if len(rec.remembered) != 0 {
			t.Errorf("remembered = %+v, want nothing", rec.remembered)
		}
	})
}

func TestLeaveTopic(t *testing.T) {
	t.Run("forwards the request and forgets the topic", func(t *testing.T) {
		// REQ-topics-003: leave_topic forwards the name and forgets the topic.
		h, srv, rec := newTopicsHandlers(t, fakelocal.Handlers{
			LeaveTopic: func(context.Context, *localpb.LeaveTopicRequest) (*localpb.LeaveTopicResponse, error) {
				return &localpb.LeaveTopicResponse{Topics: []string{"u"}}, nil
			},
		}, "agent-1")

		out, err := h.LeaveTopic(context.Background(), LeaveTopicIn{Name: "t"})
		if err != nil {
			t.Fatalf("LeaveTopic: %v", err)
		}
		if want := []string{"u"}; !reflect.DeepEqual(out.Topics, want) {
			t.Errorf("Topics = %v, want %v", out.Topics, want)
		}

		call := lastTopicCall(t, srv)
		if call.Method != "LeaveTopic" {
			t.Fatalf("method = %q, want LeaveTopic", call.Method)
		}
		req, ok := call.Request.(*localpb.LeaveTopicRequest)
		if !ok {
			t.Fatalf("request type %T, want *localpb.LeaveTopicRequest", call.Request)
		}
		if req.GetAgentId() != "agent-1" {
			t.Errorf("agent_id = %q, want agent-1", req.GetAgentId())
		}
		if req.GetName() != "t" {
			t.Errorf("name = %q, want t", req.GetName())
		}

		if want := []string{"t"}; !reflect.DeepEqual(rec.forgotten, want) {
			t.Errorf("forgotten = %v, want %v", rec.forgotten, want)
		}
	})

	t.Run("a failed leave is not forgotten", func(t *testing.T) {
		h, _, rec := newTopicsHandlers(t, fakelocal.Handlers{
			LeaveTopic: func(context.Context, *localpb.LeaveTopicRequest) (*localpb.LeaveTopicResponse, error) {
				return nil, fakelocal.ErrorStatus(codes.NotFound, &localpb.ErrorDetail{
					Code:    localpb.ErrorCode_ERROR_CODE_NOT_FOUND,
					Message: "no such topic",
				})
			},
		}, "agent-1")

		_, err := h.LeaveTopic(context.Background(), LeaveTopicIn{Name: "t"})
		if err == nil {
			t.Fatal("LeaveTopic returned no error, want one")
		}
		if !strings.HasPrefix(err.Error(), "leave_topic failed:") {
			t.Errorf("error = %q, want the prefix %q", err, "leave_topic failed:")
		}
		if len(rec.forgotten) != 0 {
			t.Errorf("forgotten = %v, want nothing", rec.forgotten)
		}
	})

	t.Run("not registered", func(t *testing.T) {
		h, srv, rec := newTopicsHandlers(t, fakelocal.Handlers{}, "")

		_, err := h.LeaveTopic(context.Background(), LeaveTopicIn{Name: "t"})
		if err == nil {
			t.Fatal("LeaveTopic returned no error, want not_registered")
		}
		if !strings.Contains(err.Error(), "(code: not_registered)") {
			t.Errorf("error = %q, want code not_registered", err)
		}
		if calls := srv.Calls(); len(calls) != 0 {
			t.Errorf("fake server received %d calls, want none: %v", len(calls), calls)
		}
		if len(rec.forgotten) != 0 {
			t.Errorf("forgotten = %v, want nothing", rec.forgotten)
		}
	})
}

// mcpserver design line 119: a tool call returns instead of queueing while a
// topic change is stuck. The second topic call stops when its context ends,
// and a registration restore is not left waiting for the same lock.
func TestLeaveTopicStopsWaitingForTheTopicLock(t *testing.T) {
	sock := fakelocal.SocketPath(t)

	joinEntered := make(chan struct{})
	var enteredOnce sync.Once
	releaseJoin := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseJoin) }) }
	// Unblock the stuck join when the test fails, so its goroutine can exit.
	t.Cleanup(release)

	fakelocal.Start(t, sock, fakelocal.Handlers{
		JoinTopic: func(ctx context.Context, req *localpb.JoinTopicRequest) (*localpb.JoinTopicResponse, error) {
			if req.GetName() == "x" {
				enteredOnce.Do(func() { close(joinEntered) })
				<-releaseJoin
			}
			return &localpb.JoinTopicResponse{Topics: []string{req.GetName()}}, nil
		},
	})
	client := topicsTestClient(t, sock)

	mem := &topicMemory{}
	r := &restorer{
		mem:      mem,
		defaults: func(context.Context, bool) []string { return nil },
		log:      topicsLogger(),
	}
	r.onComplete(context.Background(), client, "agent-1", true) // the first registration joins nothing
	mem.Remember(rememberedTopic{Name: "a"})
	h := topicsRaceHandlers(client, mem)

	// A join_topic holds the topic lock while the Broker does not answer it.
	joinDone := make(chan error, 1)
	go func() {
		_, err := h.JoinTopic(context.Background(), JoinTopicIn{Name: "x"})
		joinDone <- err
	}()
	<-joinEntered

	// The second topic call stops when its context ends instead of queueing
	// on the lock.
	ctx, cancel := context.WithCancel(context.Background())
	leaveDone := make(chan error, 1)
	go func() {
		_, err := h.LeaveTopic(ctx, LeaveTopicIn{Name: "a"})
		leaveDone <- err
	}()
	cancel()
	select {
	case err := <-leaveDone:
		if err == nil {
			t.Fatal("leave_topic returned no error after its context ended")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("leave_topic kept waiting for the topic lock after its context ended")
	}

	// A registration that ends while it waits for the lock returns too, so a
	// reconnect is not dragged behind the stuck call.
	streamCtx, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	restoreDone := make(chan struct{})
	go func() {
		defer close(restoreDone)
		r.onComplete(streamCtx, client, "agent-1", true)
	}()
	cancelStream()
	waitClosed(t, restoreDone, "the restore whose registration ended")

	// The Broker recovers and the next registration restores the memory.
	release()
	if err := waitErr(t, joinDone, "join_topic"); err != nil {
		t.Fatalf("join_topic: %v", err)
	}
	reconnectDone := make(chan struct{})
	go func() {
		defer close(reconnectDone)
		r.onComplete(context.Background(), client, "agent-1", true)
	}()
	waitClosed(t, reconnectDone, "the restore after the Broker recovered")
}

func TestListTopics(t *testing.T) {
	t.Run("forwards both groups", func(t *testing.T) {
		// REQ-topics-013: list_topics returns the joined and the public groups.
		h, srv, _ := newTopicsHandlers(t, fakelocal.Handlers{
			ListTopics: func(context.Context, *localpb.ListTopicsRequest) (*localpb.ListTopicsResponse, error) {
				return &localpb.ListTopicsResponse{
					JoinedTopics: []string{"a", "b"},
					PublicTopics: []*localpb.PublicTopic{
						{Name: "p", Description: "public one"},
						{Name: "q"},
					},
				}, nil
			},
		}, "agent-1")

		out, err := h.ListTopics(context.Background(), NoArgs{})
		if err != nil {
			t.Fatalf("ListTopics: %v", err)
		}
		if want := []string{"a", "b"}; !reflect.DeepEqual(out.Joined, want) {
			t.Errorf("Joined = %v, want %v", out.Joined, want)
		}
		want := []PublicTopicOut{{Name: "p", Description: "public one"}, {Name: "q"}}
		if !reflect.DeepEqual(out.Public, want) {
			t.Errorf("Public = %+v, want %+v", out.Public, want)
		}

		call := lastTopicCall(t, srv)
		if call.Method != "ListTopics" {
			t.Fatalf("method = %q, want ListTopics", call.Method)
		}
		req, ok := call.Request.(*localpb.ListTopicsRequest)
		if !ok {
			t.Fatalf("request type %T, want *localpb.ListTopicsRequest", call.Request)
		}
		if req.GetAgentId() != "agent-1" {
			t.Errorf("agent_id = %q, want agent-1", req.GetAgentId())
		}
	})

	t.Run("nil groups become empty arrays", func(t *testing.T) {
		h, _, _ := newTopicsHandlers(t, fakelocal.Handlers{
			ListTopics: func(context.Context, *localpb.ListTopicsRequest) (*localpb.ListTopicsResponse, error) {
				return &localpb.ListTopicsResponse{}, nil
			},
		}, "agent-1")

		out, err := h.ListTopics(context.Background(), NoArgs{})
		if err != nil {
			t.Fatalf("ListTopics: %v", err)
		}
		if out.Joined == nil || len(out.Joined) != 0 {
			t.Errorf("Joined = %#v, want a non-nil empty slice", out.Joined)
		}
		if out.Public == nil || len(out.Public) != 0 {
			t.Errorf("Public = %#v, want a non-nil empty slice", out.Public)
		}
	})

	t.Run("not registered", func(t *testing.T) {
		h, srv, _ := newTopicsHandlers(t, fakelocal.Handlers{}, "")

		_, err := h.ListTopics(context.Background(), NoArgs{})
		if err == nil {
			t.Fatal("ListTopics returned no error, want not_registered")
		}
		if !strings.Contains(err.Error(), "(code: not_registered)") {
			t.Errorf("error = %q, want code not_registered", err)
		}
		if calls := srv.Calls(); len(calls) != 0 {
			t.Errorf("fake server received %d calls, want none: %v", len(calls), calls)
		}
	})
}
