package topic

import (
	"bytes"
	"cmp"
	"errors"
	"slices"

	"github.com/wangzw/agent-peer/internal/topic/topicpb"
)

// statusToWire maps a Member.Status to its wire value. Only the four known
// lowercase values are recognized; everything else means unknown.
func statusToWire(s string) topicpb.AgentStatus {
	switch s {
	case "idle":
		return topicpb.AgentStatus_AGENT_STATUS_IDLE
	case "busy":
		return topicpb.AgentStatus_AGENT_STATUS_BUSY
	case "waiting":
		return topicpb.AgentStatus_AGENT_STATUS_WAITING
	case "shell":
		return topicpb.AgentStatus_AGENT_STATUS_SHELL
	default:
		return topicpb.AgentStatus_AGENT_STATUS_UNSPECIFIED
	}
}

// statusFromWire maps a wire status to Member.Status. UNSPECIFIED and any
// unknown enum value mean unknown, which is the empty string.
func statusFromWire(s topicpb.AgentStatus) string {
	switch s {
	case topicpb.AgentStatus_AGENT_STATUS_IDLE:
		return "idle"
	case topicpb.AgentStatus_AGENT_STATUS_BUSY:
		return "busy"
	case topicpb.AgentStatus_AGENT_STATUS_WAITING:
		return "waiting"
	case topicpb.AgentStatus_AGENT_STATUS_SHELL:
		return "shell"
	default:
		return ""
	}
}

// memberToWire converts a Member to its wire form.
func memberToWire(m Member) *topicpb.Member {
	return &topicpb.Member{
		Name:         m.Name,
		Ref:          m.Ref,
		Status:       statusToWire(m.Status),
		PreviousName: m.PrevName,
	}
}

// memberFromWire converts a wire member to a Member. A nil message is the zero
// Member.
func memberFromWire(p *topicpb.Member) Member {
	if p == nil {
		return Member{}
	}
	return Member{
		Name:     p.Name,
		Ref:      p.Ref,
		Status:   statusFromWire(p.Status),
		PrevName: p.PreviousName,
	}
}

// digestsMsg builds a TopicDigests with the digests in ascending byte order.
func digestsMsg(ds [][32]byte) *topicpb.TopicDigests {
	sorted := slices.Clone(ds)
	slices.SortFunc(sorted, func(a, b [32]byte) int {
		return bytes.Compare(a[:], b[:])
	})
	out := &topicpb.TopicDigests{Digests: make([][]byte, len(sorted))}
	for i, d := range sorted {
		b := make([]byte, len(d))
		copy(b, d[:])
		out.Digests[i] = b
	}
	return out
}

// rosterMsg builds a Roster whose members are in ascending Ref order.
func rosterMsg(d [32]byte, ms []Member) *topicpb.Roster {
	sorted := sortMembers(slices.Clone(ms))
	out := &topicpb.Roster{
		Digest:  d[:],
		Members: make([]*topicpb.Member, len(sorted)),
	}
	for i, m := range sorted {
		out.Members[i] = memberToWire(m)
	}
	return out
}

// deltaMsg builds a RosterDelta whose three lists are each in ascending Ref
// order.
func deltaMsg(d [32]byte, added []Member, removed []memberKey, changed []Member) *topicpb.RosterDelta {
	sortedAdded := sortMembers(slices.Clone(added))
	sortedRemoved := slices.Clone(removed)
	slices.SortFunc(sortedRemoved, func(a, b memberKey) int {
		return cmp.Compare(a.Ref, b.Ref)
	})
	sortedChanged := sortMembers(slices.Clone(changed))

	out := &topicpb.RosterDelta{
		Digest:  d[:],
		Added:   make([]*topicpb.Member, len(sortedAdded)),
		Removed: make([]*topicpb.MemberKey, len(sortedRemoved)),
		Changed: make([]*topicpb.Member, len(sortedChanged)),
	}
	for i, m := range sortedAdded {
		out.Added[i] = memberToWire(m)
	}
	for i, k := range sortedRemoved {
		out.Removed[i] = &topicpb.MemberKey{Name: k.Name, Ref: k.Ref}
	}
	for i, m := range sortedChanged {
		out.Changed[i] = memberToWire(m)
	}
	return out
}

// sortMembers returns ms sorted by Ref ascending.
func sortMembers(ms []Member) []Member {
	slices.SortFunc(ms, func(a, b Member) int {
		return cmp.Compare(a.Ref, b.Ref)
	})
	return ms
}

// errBadDigest reports a digest that is not exactly 32 bytes.
var errBadDigest = errors.New("topic: digest is not 32 bytes")

// parseDigest checks that b is a 32-byte digest and copies it.
func parseDigest(b []byte) ([32]byte, error) {
	var d [32]byte
	if len(b) != len(d) {
		return d, errBadDigest
	}
	copy(d[:], b)
	return d, nil
}

// parseDigests returns the set of digests in m. Any item of the wrong length
// rejects the whole frame.
func parseDigests(m *topicpb.TopicDigests) (map[[32]byte]struct{}, error) {
	out := map[[32]byte]struct{}{}
	if m == nil {
		return out, nil
	}
	for _, b := range m.Digests {
		d, err := parseDigest(b)
		if err != nil {
			return nil, err
		}
		out[d] = struct{}{}
	}
	return out, nil
}
