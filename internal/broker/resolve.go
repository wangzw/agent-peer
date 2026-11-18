package broker

import (
	"sort"
	"strings"

	"github.com/wangzw/agent-peer/internal/broker/localpb"
	"github.com/wangzw/agent-peer/internal/inbox"
)

// parsedAddress is a target address split into its parts. Name is normalized;
// Ref is empty when the address carries no disambiguation ref.
type parsedAddress struct {
	Name  string // after inbox.NormalizeName
	Ref   string // "" when absent
	Alias string
}

// parseTarget trims s, splits it with ParseAddress and normalizes the name. ok
// is false when ParseAddress rejects s or the normalized name is empty.
func parseTarget(s string) (parsedAddress, bool) {
	name, ref, alias, ok := ParseAddress(strings.TrimSpace(s))
	if !ok {
		return parsedAddress{}, false
	}
	name = inbox.NormalizeName(name)
	if name == "" {
		return parsedAddress{}, false
	}
	return parsedAddress{Name: name, Ref: ref, Alias: alias}, true
}

// candidate is one Agent a target address could name. Address is spelled as the
// caller sees it; RefAddress is the same spelling with the ref forced, used to
// disambiguate duplicate names.
type candidate struct {
	Name, Ref, PrevName string
	ID                  string // local only; "" for remote
	Visible             bool
	Address             string // as the caller sees it
	RefAddress          string // same, with the ref forced (unique=false)
}

// addrFunc spells the address of an Agent for the caller; callers pass a
// closure over signatureFor with the alias they see.
type addrFunc func(name, ref string, unique bool) string

// pickTarget returns the index of the unique target or a Not-sent *Error.
// where is appended to messages, e.g. "on hostB" or "on this Broker".
func pickTarget(addr parsedAddress, cands []candidate, refMatch func(c candidate, ref string) bool, where string) (int, *Error) {
	if addr.Ref != "" {
		return pickTargetByRef(addr, cands, refMatch, where)
	}
	return pickTargetByName(addr, cands, where)
}

// pickTargetByRef locates the target by its ref. The name only has to match the
// beginning of the Agent's cleaned current or previous name.
func pickTargetByRef(addr parsedAddress, cands []candidate, refMatch func(c candidate, ref string) bool, where string) (int, *Error) {
	// The name is only a prefix check. Clean it the way the Agent's own name is
	// cleaned before comparing: a pinned roster name may exceed the cleaner's
	// codepoint limit, and a name the cleaner changes must still match the
	// Agent whose address carried a ref for that reason.
	name := inbox.CleanSignature(addr.Name)
	if name == "" {
		// An empty prefix matches every cleaned name, so a name that cleans to
		// nothing names no Agent.
		return -1, notSent(localpb.ErrorCode_ERROR_CODE_NOT_FOUND, "no agent is known by the ref %s %s", addr.Ref, where)
	}

	var matched, invisible []int
	for i, c := range cands {
		if !refMatch(c, addr.Ref) {
			continue
		}
		if c.Visible {
			matched = append(matched, i)
		} else {
			invisible = append(invisible, i)
		}
	}

	switch len(matched) {
	case 0:
		if len(invisible) > 0 {
			return -1, notSent(localpb.ErrorCode_ERROR_CODE_NOT_VISIBLE, "%s is not visible %s", addr.Name, where)
		}
		return -1, notSent(localpb.ErrorCode_ERROR_CODE_NOT_FOUND, "no agent is known by the ref %s %s", addr.Ref, where)
	case 1:
		c := cands[matched[0]]
		if strings.HasPrefix(inbox.CleanSignature(c.Name), name) {
			return matched[0], nil
		}
		if c.PrevName != "" && strings.HasPrefix(inbox.CleanSignature(c.PrevName), name) {
			return -1, notSent(localpb.ErrorCode_ERROR_CODE_NOT_FOUND, "%s is now %s %s", addr.Name, c.Address, where)
		}
		return -1, notSent(localpb.ErrorCode_ERROR_CODE_NOT_FOUND, "%s does not name the agent with ref %s %s", addr.Name, addr.Ref, where)
	default:
		return -1, ambiguousTarget(addr.Name, matched, cands, where)
	}
}

// pickTargetByName locates the target by an exact name, falling back to the
// previous names of visible Agents for a rename hint.
func pickTargetByName(addr parsedAddress, cands []candidate, where string) (int, *Error) {
	var matched []int
	for i, c := range cands {
		if c.Visible && c.Name == addr.Name {
			matched = append(matched, i)
		}
	}

	switch len(matched) {
	case 1:
		return matched[0], nil
	case 0:
		for _, c := range cands {
			if !c.Visible && c.Name == addr.Name {
				return -1, notSent(localpb.ErrorCode_ERROR_CODE_NOT_VISIBLE, "%s is not visible %s", addr.Name, where)
			}
		}
		var prev []int
		for i, c := range cands {
			if c.Visible && c.PrevName == addr.Name {
				prev = append(prev, i)
			}
		}
		if len(prev) > 0 {
			addrs := make([]string, 0, len(prev))
			for _, i := range prev {
				addrs = append(addrs, cands[i].Address)
			}
			sort.Strings(addrs)
			return -1, notSent(localpb.ErrorCode_ERROR_CODE_NOT_FOUND, "%s is now %s %s", addr.Name, strings.Join(addrs, ", "), where)
		}
		return -1, notSent(localpb.ErrorCode_ERROR_CODE_NOT_FOUND, "no agent named %q %s", addr.Name, where)
	default:
		return -1, ambiguousTarget(addr.Name, matched, cands, where)
	}
}

// ambiguousTarget lists the ref-forced addresses of the matched Agents in
// lexicographic order.
func ambiguousTarget(name string, matched []int, cands []candidate, where string) *Error {
	addrs := make([]string, 0, len(matched))
	for _, i := range matched {
		addrs = append(addrs, cands[i].RefAddress)
	}
	sort.Strings(addrs)
	return notSent(localpb.ErrorCode_ERROR_CODE_AMBIGUOUS, "%s is ambiguous %s: %s", name, where, strings.Join(addrs, ", "))
}

// localRefMatch matches a local Agent whose ID starts with the address ref.
func localRefMatch(c candidate, ref string) bool { return strings.HasPrefix(c.ID, ref) }

// remoteRefMatch matches a remote member whose ref is a prefix of the address
// ref or vice versa: a roster's ref length changes as Agents come and go.
func remoteRefMatch(c candidate, ref string) bool {
	return strings.HasPrefix(c.Ref, ref) || strings.HasPrefix(ref, c.Ref)
}

// localCandidates builds candidates from all local Agents. A name is unique when
// it occurs once in the whole table, regardless of visibility.
func localCandidates(all []agentView, visible func(agentView) bool, addr addrFunc) []candidate {
	counts := make(map[string]int, len(all))
	for _, a := range all {
		counts[a.Name]++
	}
	cands := make([]candidate, 0, len(all))
	for _, a := range all {
		unique := counts[a.Name] == 1
		cands = append(cands, candidate{
			Name:       a.Name,
			Ref:        a.Ref,
			PrevName:   a.PrevName,
			ID:         a.ID,
			Visible:    visible(a),
			Address:    addr(a.Name, a.Ref, unique),
			RefAddress: addr(a.Name, a.Ref, false),
		})
	}
	return cands
}

// remoteCandidates builds candidates from a peer's roster entries,
// de-duplicated by (Name, Ref). A name is unique when it appears with a single
// distinct Ref.
func remoteCandidates(members []remoteMember, visible func(remoteMember) bool, addr addrFunc) []candidate {
	seen := make(map[string]bool, len(members))
	refsByName := make(map[string]map[string]bool)
	unique := make([]remoteMember, 0, len(members))
	for _, m := range members {
		key := m.Name + "\x00" + m.Ref
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, m)
		if refsByName[m.Name] == nil {
			refsByName[m.Name] = make(map[string]bool)
		}
		refsByName[m.Name][m.Ref] = true
	}

	cands := make([]candidate, 0, len(unique))
	for _, m := range unique {
		isUnique := len(refsByName[m.Name]) == 1
		cands = append(cands, candidate{
			Name:       m.Name,
			Ref:        m.Ref,
			PrevName:   m.PrevName,
			Visible:    visible(m),
			Address:    addr(m.Name, m.Ref, isUnique),
			RefAddress: addr(m.Name, m.Ref, false),
		})
	}
	return cands
}
