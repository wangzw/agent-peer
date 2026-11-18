package mcpserver

import (
	"context"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
)

// topicLockError is the error shown when a topic call stops waiting for
// another topic change instead of queueing behind it.
func topicLockError(tool string, err error) error {
	return toolError(tool+" failed", "another topic change was still in progress: "+err.Error(), localpb.ErrorCode_ERROR_CODE_INTERNAL)
}

// ListTopics forwards list_topics and returns the joined and public topics the
// Broker reports.
func (h *handlers) ListTopics(ctx context.Context, _ NoArgs) (ListTopicsOut, error) {
	st, err := h.preflight("list_topics", callGeneric, true)
	if err != nil {
		return ListTopicsOut{}, err
	}
	resp, err := st.Client.ListTopics(ctx, &localpb.ListTopicsRequest{AgentId: st.AgentID})
	if err != nil {
		return ListTopicsOut{}, rpcError("list_topics", callGeneric, permNone, err)
	}

	out := ListTopicsOut{
		Joined: append([]string{}, resp.GetJoinedTopics()...),
		Public: make([]PublicTopicOut, 0, len(resp.GetPublicTopics())),
	}
	for _, t := range resp.GetPublicTopics() {
		out.Public = append(out.Public, PublicTopicOut{
			Name:        t.GetName(),
			Description: t.GetDescription(),
		})
	}
	return out, nil
}

// JoinTopic forwards join_topic and, on success only, records the membership so
// that a later registration can restore it.
func (h *handlers) JoinTopic(ctx context.Context, in JoinTopicIn) (TopicsOut, error) {
	st, err := h.preflight("join_topic", callGeneric, true)
	if err != nil {
		return TopicsOut{}, err
	}
	// The Broker call and the memory write are one critical section: a
	// registration restore of the same topic must not run between them. The
	// call stops when the context ends rather than queueing on the lock, and
	// the RPC itself is bounded so the lock cannot be held indefinitely.
	if err := h.topics.lockTopic(ctx); err != nil {
		return TopicsOut{}, topicLockError("join_topic", err)
	}
	defer h.topics.unlockTopic()
	ctx, cancel := context.WithTimeout(ctx, topicRPCTimeout)
	defer cancel()
	resp, err := st.Client.JoinTopic(ctx, &localpb.JoinTopicRequest{
		AgentId:     st.AgentID,
		Name:        in.Name,
		IsPublic:    in.Public,
		Description: in.Description,
	})
	if err != nil {
		return TopicsOut{}, rpcError("join_topic", callGeneric, permNone, err)
	}
	h.topics.Remember(rememberedTopic{
		Name:        in.Name,
		Public:      in.Public,
		Description: in.Description,
	})
	return TopicsOut{Topics: resp.GetTopics()}, nil
}

// LeaveTopic forwards leave_topic and, on success only, forgets the membership.
func (h *handlers) LeaveTopic(ctx context.Context, in LeaveTopicIn) (TopicsOut, error) {
	st, err := h.preflight("leave_topic", callGeneric, true)
	if err != nil {
		return TopicsOut{}, err
	}
	if err := h.topics.lockTopic(ctx); err != nil {
		return TopicsOut{}, topicLockError("leave_topic", err)
	}
	defer h.topics.unlockTopic()
	ctx, cancel := context.WithTimeout(ctx, topicRPCTimeout)
	defer cancel()
	resp, err := st.Client.LeaveTopic(ctx, &localpb.LeaveTopicRequest{
		AgentId: st.AgentID,
		Name:    in.Name,
	})
	if err != nil {
		return TopicsOut{}, rpcError("leave_topic", callGeneric, permNone, err)
	}
	h.topics.Forget(in.Name)
	return TopicsOut{Topics: resp.GetTopics()}, nil
}
