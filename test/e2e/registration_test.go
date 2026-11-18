//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/mcpserver"
	"github.com/wangzw/agent-peer/test/e2e/harness"
	"github.com/wangzw/agent-peer/test/e2e/node"
)

// TestLocalRegistration covers registration, naming and listing on one host.
// The Broker runs as a real process and every session is a fakesession record
// plus, unless the case says otherwise, a real agent-peer mcp process. These
// cases do not send messages, so they need no Broker delivery.
func TestLocalRegistration(t *testing.T) {
	host := harness.NewLocalHost(t, harness.LocalOptions{Name: "registration"})
	registrationStartBroker(t, host, harness.ConfigYAML("broker:\n  listen: "+host.FreeAddr(t)))
	workspace := registrationWorkspace(t)

	t.Run("REQ-agent-messaging-001 self address in list", func(t *testing.T) {
		api := registrationSession(t, host, "api-worker", workspace)
		defer api.End(t)

		out := registrationWaitRegistered(t, api, "api-worker")
		if name := registrationAddressName(out.Self); name != "api-worker" {
			t.Fatalf("self_address is %q, want the name part api-worker", out.Self)
		}
		if !strings.HasSuffix(out.Self, "@"+registrationAlias(t, out.Self)) {
			t.Fatalf("self_address %q does not end with @<alias>", out.Self)
		}
	})

	t.Run("REQ-agent-messaging-002 session without tools", func(t *testing.T) {
		api := registrationSession(t, host, "api-worker", workspace)
		defer api.End(t)
		self := registrationWaitRegistered(t, api, "api-worker").Self

		// ghost has a registry record and a bound inbox socket but no MCP
		// server process, so it must not become an agent.
		ghost := host.StartSession(t, harness.SessionSpec{Name: "ghost", Workdir: workspace})
		defer ghost.End(t)

		harness.Consistently(t, 1*time.Second, "a session with no MCP server process stays out of the list", func() (bool, string) {
			out, state := registrationListAgents(t, api, nil)
			if state != "" {
				return false, state
			}
			for _, agent := range out.Agents {
				if strings.Contains(agent.Address, "ghost") {
					return false, fmt.Sprintf("ghost appears as %q", agent.Address)
				}
			}
			return true, ""
		})

		r := api.Call(t, "send_message", map[string]any{
			"to":      "ghost@" + registrationAlias(t, self),
			"message": "are you there?",
		})
		r.MustCode(t, "not_found")
		if frames := ghost.Fake().Frames(); len(frames) != 0 {
			t.Fatalf("the session without tools received %d frames", len(frames))
		}
	})

	t.Run("REQ-agent-messaging-003 name normalization", func(t *testing.T) {
		api := registrationSession(t, host, " API Worker ", workspace)
		defer api.End(t)

		out := registrationWaitRegistered(t, api, "api-worker")
		if name := registrationAddressName(out.Self); name != "api-worker" {
			t.Fatalf("self_address is %q, want the name part api-worker", out.Self)
		}
	})

	t.Run("REQ-agent-messaging-004 same name gets refs", func(t *testing.T) {
		first := registrationSession(t, host, "twin", workspace)
		defer first.End(t)
		second := registrationSession(t, host, "twin", workspace)
		defer second.End(t)
		observer := registrationSession(t, host, "observer", workspace)
		defer observer.End(t)
		registrationWaitRegistered(t, observer, "observer")

		refs := map[string]bool{}
		harness.Eventually(t, 3*time.Second, "both agents named twin carry a ref", func() (bool, string) {
			out, state := registrationListAgents(t, observer, nil)
			if state != "" {
				return false, state
			}
			refs = map[string]bool{}
			for _, agent := range out.Agents {
				name, ref := registrationSplitAddress(agent.Address)
				if name != "twin" {
					continue
				}
				if ref == "" {
					return false, fmt.Sprintf("%q carries no ref while two twins exist", agent.Address)
				}
				refs[ref] = true
			}
			if len(refs) != 2 {
				return false, fmt.Sprintf("found %d distinct twin refs in %v", len(refs), out.Agents)
			}
			return true, ""
		})

		// A lone twin no longer needs the ref.
		second.End(t)
		harness.Eventually(t, 3*time.Second, "the remaining twin carries no ref", func() (bool, string) {
			out, state := registrationListAgents(t, observer, nil)
			if state != "" {
				return false, state
			}
			twins := 0
			for _, agent := range out.Agents {
				name, ref := registrationSplitAddress(agent.Address)
				if name != "twin" {
					continue
				}
				twins++
				if ref != "" {
					return false, fmt.Sprintf("%q still carries a ref", agent.Address)
				}
			}
			if twins != 1 {
				return false, fmt.Sprintf("found %d twins, want one", twins)
			}
			return true, ""
		})
	})

	t.Run("REQ-agent-messaging-006 ref algorithm", func(t *testing.T) {
		first := registrationSession(t, host, "twin", workspace)
		defer first.End(t)
		second := registrationSession(t, host, "twin", workspace)
		defer second.End(t)
		observer := registrationSession(t, host, "observer", workspace)
		defer observer.End(t)
		registrationWaitRegistered(t, observer, "observer")

		bySocket := map[string]bool{
			first.Fake().SocketPath():  true,
			second.Fake().SocketPath(): true,
		}
		harness.Eventually(t, 3*time.Second, "the twin refs are the prefixes of their agent IDs", func() (bool, string) {
			out, state := registrationListAgents(t, observer, nil)
			if state != "" {
				return false, state
			}
			seen := 0
			for _, agent := range out.Agents {
				name, ref := registrationSplitAddress(agent.Address)
				if name != "twin" {
					continue
				}
				seen++
				if !registrationRefMatchesAny(ref, bySocket) {
					return false, fmt.Sprintf("ref %q is not a prefix of either sha256(\"session:\"+socket)", ref)
				}
			}
			if seen != 2 {
				return false, fmt.Sprintf("found %d twins, want two", seen)
			}
			return true, ""
		})

		// A name that changes under signature cleaning always carries a ref,
		// and that ref depends on the socket path only, so renaming and
		// clearing the conversation leave it unchanged.
		odd := registrationSession(t, host, "a<b", workspace)
		defer odd.End(t)
		var refBefore string
		harness.Eventually(t, 3*time.Second, "the cleaned name ab carries a ref", func() (bool, string) {
			out, state := registrationListAgents(t, observer, nil)
			if state != "" {
				return false, state
			}
			for _, agent := range out.Agents {
				name, ref := registrationSplitAddress(agent.Address)
				if name == "ab" && ref != "" {
					refBefore = ref
					return true, ""
				}
			}
			return false, "no ab [ref] entry yet"
		})

		odd.Fake().Rename("c<d")
		odd.Fake().SetSessionID("after-clear")
		harness.Eventually(t, 3*time.Second, "the ref survives a rename and a conversation clear", func() (bool, string) {
			out, state := registrationListAgents(t, observer, nil)
			if state != "" {
				return false, state
			}
			for _, agent := range out.Agents {
				name, ref := registrationSplitAddress(agent.Address)
				if name == "cd" {
					if ref != refBefore {
						return false, fmt.Sprintf("ref is now %q, want %q", ref, refBefore)
					}
					return true, ""
				}
			}
			return false, "no cd entry yet"
		})
	})

	t.Run("REQ-agent-messaging-009 shared topics", func(t *testing.T) {
		a := registrationSession(t, host, "a", workspace)
		defer a.End(t)
		b := registrationSession(t, host, "b", workspace)
		defer b.End(t)
		registrationWaitRegistered(t, a, "a")
		registrationWaitRegistered(t, b, "b")
		registrationJoin(t, a, "t1")
		registrationJoin(t, a, "t2")
		registrationJoin(t, b, "t1")
		registrationJoin(t, b, "t2")

		want := []string{"github.com/org/repo", "t1", "t2"}
		harness.Eventually(t, 3*time.Second, "b's shared topics are the intersection", func() (bool, string) {
			out, state := registrationListAgents(t, a, nil)
			if state != "" {
				return false, state
			}
			entry, ok := registrationFindAgent(out, "b")
			if !ok {
				return false, "b is not listed"
			}
			return registrationSameSet(entry.SharedTopics, want), fmt.Sprintf("shared topics are %v, want %v", entry.SharedTopics, want)
		})

		// A topic filter selects entries but must not trim shared_topics.
		out, state := registrationListAgents(t, a, map[string]any{"topic": "t1"})
		if state != "" {
			t.Fatalf("list_agents with a topic filter failed: %s", state)
		}
		entry, ok := registrationFindAgent(out, "b")
		if !ok {
			t.Fatalf("b is missing from the t1-filtered list: %v", out.Agents)
		}
		if !registrationSameSet(entry.SharedTopics, want) {
			t.Fatalf("shared topics after filtering are %v, want %v", entry.SharedTopics, want)
		}
	})

	t.Run("REQ-agent-messaging-011 self entry", func(t *testing.T) {
		api := registrationSession(t, host, "api-worker", workspace)
		defer api.End(t)
		out := registrationWaitRegistered(t, api, "api-worker")
		if out.Self == "" {
			t.Fatal("self_address is empty")
		}
		for _, agent := range out.Agents {
			if agent.Address == out.Self {
				t.Fatalf("the list contains the caller's own address %q", out.Self)
			}
		}
	})

	t.Run("REQ-agent-messaging-030 unusable names", func(t *testing.T) {
		observer := registrationSession(t, host, "observer", workspace)
		defer observer.End(t)
		registrationWaitRegistered(t, observer, "observer")

		// The Broker's not_registered event carries a reason that names the
		// first failing rule, and the MCP server renders that detail verbatim.
		// The exact sentence is asserted so that the general "empty, or contains
		// @ or *" sentence cannot satisfy any of these cases.
		rejected := func(s *harness.Session, name, detail string, listed func(addr string) bool) {
			t.Helper()
			harness.Eventually(t, harness.RegistrationReportBound, fmt.Sprintf("session name %q is reported unusable", name), func() (bool, string) {
				r := s.Call(t, "list_agents", nil)
				if !r.IsError {
					return false, "list_agents succeeded"
				}
				if code := r.Code(); code != "not_registered" {
					return false, "error code is " + code
				}
				if !strings.Contains(r.Text, detail) {
					return false, fmt.Sprintf("the error does not carry the cause %q: %s", detail, r.Text)
				}
				return true, ""
			})
			harness.Consistently(t, 500*time.Millisecond, fmt.Sprintf("no agent is listed for the unusable name %q", name), func() (bool, string) {
				out, state := registrationListAgents(t, observer, nil)
				if state != "" {
					return false, state
				}
				for _, agent := range out.Agents {
					if listed(agent.Address) {
						return false, fmt.Sprintf("an agent with address %q was listed", agent.Address)
					}
				}
				return true, ""
			})
		}

		// Renaming the session after the Broker reported the "@" lets the
		// registration connection that was left open finish: the MCP server
		// process is not restarted and no second session is started.
		at := registrationSession(t, host, "a@b", workspace)
		defer at.End(t)
		rejected(at, "a@b", `the session name contains "@"`, func(addr string) bool {
			return strings.Contains(addr, "a@b")
		})

		at.Fake().SetName("ab")
		harness.Eventually(t, 3*time.Second, "the renamed session receives registration complete on the same connection", func() (bool, string) {
			out, state := registrationListAgents(t, at, nil)
			if state != "" {
				return false, state
			}
			if name := registrationAddressName(out.Self); name != "ab" {
				return false, fmt.Sprintf("self_address is %q, want the name part ab", out.Self)
			}
			return true, ""
		})

		star := registrationSession(t, host, "a*b", workspace)
		defer star.End(t)
		rejected(star, "a*b", `the session name contains "*"`, func(addr string) bool {
			return strings.Contains(addr, "a*b")
		})

		empty := registrationSession(t, host, "", workspace)
		defer empty.End(t)
		rejected(empty, "", "the session name is empty", func(addr string) bool {
			return registrationAddressName(addr) == ""
		})
	})

	t.Run("REQ-agent-messaging-035 second MCP process", func(t *testing.T) {
		main := registrationSession(t, host, "main", workspace)
		defer main.End(t)
		mainOut := registrationWaitRegistered(t, main, "main")
		mainAddr := mainOut.Self
		observer := registrationSession(t, host, "observer", workspace)
		defer observer.End(t)
		observerOut := registrationWaitRegistered(t, observer, "observer")
		observerAddr := observerOut.Self

		main.StartExtraMCP(t)

		// The extra process registers through the same inbox socket, so it
		// shares the main session's agent ID and must not add a second entry.
		harness.Consistently(t, 1*time.Second, "the extra MCP process does not become a second agent", func() (bool, string) {
			out, state := registrationListAgents(t, observer, nil)
			if state != "" {
				return false, state
			}
			entries := 0
			for _, agent := range out.Agents {
				if name := registrationAddressName(agent.Address); name == "main" {
					entries++
					if agent.Address != mainAddr {
						return false, fmt.Sprintf("address is %q, want %q", agent.Address, mainAddr)
					}
				}
			}
			return entries == 1, fmt.Sprintf("found %d entries for main, want one", entries)
		})

		// The harness has no tool channel to the extra process itself; the
		// observable identity is checked by sending through the session's
		// process while the extra one is running. The delivered signature must
		// still be the single main address.
		r := main.Call(t, "send_message", map[string]any{"to": observerAddr, "message": "from the main session"})
		r.MustOK(t)
		frame := observer.WaitFrame(t, 3*time.Second)
		if frame.FromName != mainAddr {
			t.Fatalf("the message is signed %q, want %q", frame.FromName, mainAddr)
		}
	})
}

// registrationSession starts one simulated session with a real MCP server
// process working in workdir.
func registrationSession(t *testing.T, host *harness.Node, name, workdir string) *harness.Session {
	t.Helper()
	return host.StartSession(t, harness.SessionSpec{Name: name, StartMCP: true, Workdir: workdir})
}

// registrationStartBroker starts the node's Broker and fails when it does not
// come up.
func registrationStartBroker(t *testing.T, host *harness.Node, config string) {
	t.Helper()
	start := host.StartBroker(t, harness.BrokerSpec{ConfigYAML: config})
	if !start.Running {
		t.Fatalf("the Broker did not start: exit code %d: %s", start.ExitCode, strings.TrimSpace(start.StderrLast))
	}
}

// registrationGitEnv is the environment of the git commands that create test
// workspaces. It never reads the host's git configuration.
func registrationGitEnv(t *testing.T) []string {
	t.Helper()
	return []string{
		"HOME=" + t.TempDir(),
		"PATH=" + os.Getenv("PATH"),
		"LANG=C.UTF-8",
		"GIT_CONFIG_NOSYSTEM=1",
	}
}

// registrationWorkspace creates the shared git workspace whose origin names
// the default topic github.com/org/repo.
func registrationWorkspace(t *testing.T) string {
	t.Helper()
	ws, err := node.GitWorkspace(filepath.Join(t.TempDir(), "repo"), "https://github.com/org/repo", registrationGitEnv(t))
	if err != nil {
		t.Fatalf("creating the git workspace: %v", err)
	}
	return ws
}

// registrationWaitRegistered waits until the session is registered and lists
// itself, and returns that listing.
func registrationWaitRegistered(t *testing.T, s *harness.Session, name string) mcpserver.ListAgentsOut {
	t.Helper()
	var out mcpserver.ListAgentsOut
	harness.Eventually(t, 3*time.Second, "the session is registered and lists itself", func() (bool, string) {
		var state string
		out, state = registrationListAgents(t, s, nil)
		if state != "" {
			return false, state
		}
		if got := registrationAddressName(out.Self); got != name {
			return false, fmt.Sprintf("self_address is %q, want the name part %s", out.Self, name)
		}
		return true, ""
	})
	return out
}

// registrationListAgents calls list_agents and returns the decoded listing. A
// tool error is reported as a state string so Eventually can retry and so a
// caller can inspect the error instead of failing the test inside it.
func registrationListAgents(t *testing.T, s *harness.Session, args map[string]any) (mcpserver.ListAgentsOut, string) {
	t.Helper()
	r := s.Call(t, "list_agents", args)
	if r.IsError {
		return mcpserver.ListAgentsOut{}, r.Text
	}
	out, err := harness.Decode[mcpserver.ListAgentsOut](string(r.Structured))
	if err != nil {
		return mcpserver.ListAgentsOut{}, "decoding list_agents: " + err.Error()
	}
	return out, ""
}

// registrationJoin joins name, retrying while the session is still
// registering.
func registrationJoin(t *testing.T, s *harness.Session, name string) {
	t.Helper()
	harness.Eventually(t, 3*time.Second, "join_topic "+name+" succeeds", func() (bool, string) {
		r := s.Call(t, "join_topic", map[string]any{"name": name})
		if r.IsError {
			return false, r.Text
		}
		return true, ""
	})
}

// registrationAlias returns the Broker alias from an agent address.
func registrationAlias(t *testing.T, addr string) string {
	t.Helper()
	at := strings.LastIndexByte(addr, '@')
	if at < 0 || at == len(addr)-1 {
		t.Fatalf("address %q carries no alias", addr)
	}
	return addr[at+1:]
}

// registrationSplitAddress splits "name [ref]@alias" into name and ref. ref is
// empty when the address carries none.
func registrationSplitAddress(addr string) (name, ref string) {
	at := strings.LastIndexByte(addr, '@')
	if at < 0 {
		return addr, ""
	}
	head := addr[:at]
	if i := strings.LastIndex(head, " ["); i >= 0 && strings.HasSuffix(head, "]") {
		return head[:i], head[i+2 : len(head)-1]
	}
	return head, ""
}

// registrationAddressName returns the name part of an address, without a ref.
func registrationAddressName(addr string) string {
	name, _ := registrationSplitAddress(addr)
	return name
}

// registrationFindAgent returns the entry whose name part is name.
func registrationFindAgent(out mcpserver.ListAgentsOut, name string) (mcpserver.AgentOut, bool) {
	for _, agent := range out.Agents {
		if registrationAddressName(agent.Address) == name {
			return agent, true
		}
	}
	return mcpserver.AgentOut{}, false
}

// registrationSameSet reports whether got and want contain the same strings.
func registrationSameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]bool, len(got))
	for _, value := range got {
		seen[value] = true
	}
	for _, value := range want {
		if !seen[value] {
			return false
		}
	}
	return true
}

// registrationRefMatchesAny reports whether ref is a 6- to 12-character
// lowercase hex prefix of sha256("session:"+socket) for some agent socket.
func registrationRefMatchesAny(ref string, bySocket map[string]bool) bool {
	if len(ref) < 6 || len(ref) > 12 {
		return false
	}
	for socket := range bySocket {
		sum := sha256.Sum256([]byte("session:" + filepath.Clean(socket)))
		if strings.HasPrefix(hex.EncodeToString(sum[:]), ref) {
			return true
		}
	}
	return false
}
