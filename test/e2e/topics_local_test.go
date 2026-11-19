//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/internal/mcpserver"
	"github.com/wangzw/agent-peer/test/e2e/harness"
	"github.com/wangzw/agent-peer/test/e2e/node"
)

// TestLocalTopics covers joining, leaving and visibility on one host, and
// every branch that decides which topics a new agent joins automatically. The
// Broker and the MCP server processes are real processes; workspaces are
// created per case because the automatic topic depends on the working
// directory.
func TestLocalTopics(t *testing.T) {
	host := harness.NewLocalHost(t, harness.LocalOptions{Name: "topics"})
	listen := host.FreeAddr(t)
	listen = topicsStartBroker(t, host, topicsConfig(listen, true), listen)
	defaultConfig := topicsConfig(listen, true)

	t.Run("REQ-topics-001 join", func(t *testing.T) {
		s := topicsSession(t, host, "joiner", topicsPlain(t))
		defer s.End(t)
		topicsJoin(t, s, "release")
		// The acceptance criterion is the agent's own topic list, not the echo
		// join_topic returns.
		out, state := topicsList(t, s)
		if state != "" {
			t.Fatalf("list_topics failed: %s", state)
		}
		if !topicsContains(out.Joined, "release") {
			t.Fatalf("joined topics after join_topic are %v, want release", out.Joined)
		}
	})

	t.Run("REQ-topics-002 join new name without approval", func(t *testing.T) {
		ws := topicsPlain(t)
		p := topicsSession(t, host, "p", ws)
		defer p.End(t)
		q := topicsSession(t, host, "q", ws)
		defer q.End(t)

		name := fmt.Sprintf("fresh-%d", time.Now().UnixNano())
		topicsJoin(t, p, name)
		topicsJoin(t, q, name)

		topicsWaitVisible(t, p, "q")
		topicsWaitVisible(t, q, "p")
	})

	t.Run("REQ-topics-003 leave", func(t *testing.T) {
		ws := topicsPlain(t)
		p := topicsSession(t, host, "p", ws)
		defer p.End(t)
		q := topicsSession(t, host, "q", ws)
		defer q.End(t)
		topicsJoin(t, p, "shared")
		topicsJoin(t, q, "shared")
		topicsWaitVisible(t, p, "q")

		topicsLeave(t, p, "shared")
		out, state := topicsList(t, p)
		if state != "" {
			t.Fatalf("list_topics failed: %s", state)
		}
		if topicsContains(out.Joined, "shared") {
			t.Fatalf("joined topics still contain shared: %v", out.Joined)
		}
		topicsWaitInvisible(t, q, "p")
	})

	t.Run("REQ-topics-004 many topics", func(t *testing.T) {
		s := topicsSession(t, host, "multi", topicsPlain(t))
		defer s.End(t)
		topicsJoin(t, s, "t1")
		topicsJoin(t, s, "t2")
		// Read the resulting membership from list_topics rather than from the
		// echo of the last join.
		out, state := topicsList(t, s)
		if state != "" {
			t.Fatalf("list_topics failed: %s", state)
		}
		if !topicsSameSet(out.Joined, []string{"t1", "t2"}) {
			t.Fatalf("joined topics are %v, want t1 and t2", out.Joined)
		}
	})

	t.Run("REQ-topics-006 no topic means invisible", func(t *testing.T) {
		lonely := topicsSession(t, host, "lonely", topicsPlain(t))
		defer lonely.End(t)
		harness.Eventually(t, 3*time.Second, "the session with no topics is registered", func() (bool, string) {
			out, state := topicsList(t, lonely)
			if state != "" {
				return false, state
			}
			return len(out.Joined) == 0, fmt.Sprintf("joined topics are %v", out.Joined)
		})

		watcher := topicsSession(t, host, "watcher", topicsPlain(t))
		defer watcher.End(t)
		topicsJoin(t, watcher, "watch")
		harness.Consistently(t, 1*time.Second, "an agent in no topic appears in no list", func() (bool, string) {
			out, state := topicsListAgents(t, watcher, nil)
			if state != "" {
				return false, state
			}
			if _, ok := topicsFindAgent(out, "lonely"); ok {
				return false, "lonely is listed by watcher"
			}
			return true, ""
		})
	})

	t.Run("REQ-topics-015 project topics", func(t *testing.T) {
		ws := topicsProject(t, "topics: [t1, t2]\n", "https://github.com/org/repo")
		s := topicsSession(t, host, "project", ws)
		defer s.End(t)
		topicsWaitJoinedSet(t, s, "t1", "t2")
	})

	t.Run("REQ-topics-016 origin topic", func(t *testing.T) {
		ws := topicsOrigin(t, "https://github.com/org/repo")
		s := topicsSession(t, host, "origin", ws)
		defer s.End(t)
		topicsWaitJoinedSet(t, s, "github.com/org/repo")
	})

	t.Run("REQ-topics-017 spellings", func(t *testing.T) {
		origins := []string{
			"git@github.com:org/repo.git",
			"ssh://git@GitHub.com:22/org/repo.git",
			"https://github.com/org/repo",
		}
		sessions := make([]*harness.Session, 0, len(origins))
		for i, origin := range origins {
			s := topicsSession(t, host, fmt.Sprintf("spelling%d", i), topicsOrigin(t, origin))
			defer s.End(t)
			sessions = append(sessions, s)
			topicsWaitJoinedSet(t, s, "github.com/org/repo")
		}
		for i, s := range sessions {
			for j := range sessions {
				if i == j {
					continue
				}
				topicsWaitVisible(t, s, fmt.Sprintf("spelling%d", j))
			}
		}
	})

	t.Run("REQ-topics-018 non-git", func(t *testing.T) {
		loner := topicsSession(t, host, "loner", topicsPlain(t))
		defer loner.End(t)
		harness.Eventually(t, 3*time.Second, "a session outside a git repository joins no topic", func() (bool, string) {
			out, state := topicsList(t, loner)
			if state != "" {
				return false, state
			}
			return len(out.Joined) == 0, fmt.Sprintf("joined topics are %v", out.Joined)
		})

		watcher := topicsSession(t, host, "watcher", topicsOrigin(t, "https://github.com/org/repo"))
		defer watcher.End(t)
		topicsWaitJoinedSet(t, watcher, "github.com/org/repo")
		harness.Consistently(t, 1*time.Second, "an agent with no topic appears in no list", func() (bool, string) {
			out, state := topicsListAgents(t, watcher, nil)
			if state != "" {
				return false, state
			}
			if _, ok := topicsFindAgent(out, "loner"); ok {
				return false, "loner is listed by watcher"
			}
			return true, ""
		})
	})

	t.Run("REQ-topics-020 broker auto_join off", func(t *testing.T) {
		listen = topicsRestartBroker(t, host, topicsConfig(listen, false), listen)
		defaultConfig = topicsConfig(listen, true)

		s := topicsSession(t, host, "no-join", topicsOrigin(t, "https://github.com/org/repo"))
		defer s.End(t)
		topicsWaitRegistered(t, s)
		// The session is registered; stay long enough for a wrong auto-join to
		// show up.
		harness.Consistently(t, 1*time.Second, "Broker configuration turns automatic joining off", func() (bool, string) {
			out, state := topicsList(t, s)
			if state != "" {
				return false, state
			}
			return len(out.Joined) == 0, fmt.Sprintf("joined topics are %v", out.Joined)
		})
	})

	t.Run("REQ-topics-021 project auto_join off", func(t *testing.T) {
		topicsRestartBroker(t, host, defaultConfig, listen)

		off := topicsSession(t, host, "off", topicsProject(t, "auto_join: false\n", "https://github.com/org/repo"))
		defer off.End(t)
		topicsWaitRegistered(t, off)
		harness.Consistently(t, 1*time.Second, "project configuration turns automatic joining off", func() (bool, string) {
			out, state := topicsList(t, off)
			if state != "" {
				return false, state
			}
			return len(out.Joined) == 0, fmt.Sprintf("joined topics are %v", out.Joined)
		})

		on := topicsSession(t, host, "on", topicsOrigin(t, "https://github.com/org/repo"))
		defer on.End(t)
		topicsWaitJoinedSet(t, on, "github.com/org/repo")
	})

	t.Run("workdir subdirectory", func(t *testing.T) {
		ws := topicsOrigin(t, "https://github.com/org/repo")
		sub := filepath.Join(ws, "sub", "dir")
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("creating the subdirectory: %v", err)
		}
		atRoot := topicsSession(t, host, "at-root", ws)
		defer atRoot.End(t)
		topicsWaitJoinedSet(t, atRoot, "github.com/org/repo")
		inSub := topicsSession(t, host, "in-sub", sub)
		defer inSub.End(t)
		topicsWaitJoinedSet(t, inSub, "github.com/org/repo")
	})
}

// topicsConfig builds the Broker configuration with the given listener and
// automatic-join setting.
func topicsConfig(listen string, autoJoin bool) string {
	parts := []string{"broker:\n  listen: " + listen}
	if !autoJoin {
		parts = append(parts, "topics:\n  auto_join: false")
	}
	return harness.ConfigYAML(parts...)
}

// topicsSession starts one simulated session with a real MCP server process
// working in workdir.
func topicsSession(t *testing.T, host *harness.Node, name, workdir string) *harness.Session {
	t.Helper()
	return host.StartSession(t, harness.SessionSpec{Name: name, StartMCP: true, Workdir: workdir})
}

// topicsStartBroker starts the node's Broker and fails when it does not come
// up. It returns the address the Broker actually uses in place of requested.
func topicsStartBroker(t *testing.T, host *harness.Node, config, requested string) string {
	t.Helper()
	start := host.StartBroker(t, harness.BrokerSpec{ConfigYAML: config})
	if !start.Running {
		t.Fatalf("the Broker did not start: exit code %d: %s", start.ExitCode, strings.TrimSpace(start.StderrLast))
	}
	return start.Address(requested)
}

// topicsRestartBroker stops the Broker and starts it with config, returning the
// address the Broker actually uses in place of requested. A session process may
// start its own Broker first; that Broker refuses to start with exit code 3,
// and the helper then waits for the process the session started.
func topicsRestartBroker(t *testing.T, host *harness.Node, config, requested string) string {
	t.Helper()
	host.StopBroker(t, syscall.SIGTERM)
	start := host.StartBroker(t, harness.BrokerSpec{ConfigYAML: config})
	if start.Running {
		return start.Address(requested)
	}
	if start.ExitCode != 3 {
		t.Fatalf("the Broker did not restart: exit code %d: %s", start.ExitCode, strings.TrimSpace(start.StderrLast))
	}
	bin := harness.BuildBinary(t, runtime.GOOS, runtime.GOARCH, "agentpeer_testenv")
	// The Broker gets the product's ten-second readiness allowance plus three
	// seconds, the same bound harness.StartBroker applies.
	harness.Eventually(t, brokerReadyBound, "the Broker answers after a session process started it", func() (bool, string) {
		result := host.Exec(t, []string{bin, "status"}, nil)
		if result.ExitCode == 0 {
			return true, ""
		}
		return false, fmt.Sprintf("agent-peer status exited %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	})
	// The session's Broker read the configuration file as written, so no retry
	// replaced its address.
	return start.Address(requested)
}

// topicsGitEnv is the environment of the git commands that create test
// workspaces. It never reads the host's git configuration.
func topicsGitEnv(t *testing.T) []string {
	t.Helper()
	return []string{
		"HOME=" + t.TempDir(),
		"PATH=" + os.Getenv("PATH"),
		"LANG=C.UTF-8",
		"GIT_CONFIG_NOSYSTEM=1",
	}
}

// topicsPlain creates a workspace without a repository or a project
// configuration file.
func topicsPlain(t *testing.T) string {
	t.Helper()
	ws, err := node.PlainWorkspace(filepath.Join(t.TempDir(), "plain"))
	if err != nil {
		t.Fatalf("creating the plain workspace: %v", err)
	}
	return ws
}

// topicsOrigin creates a git workspace whose origin is origin.
func topicsOrigin(t *testing.T, origin string) string {
	t.Helper()
	ws, err := node.GitWorkspace(filepath.Join(t.TempDir(), "repo"), origin, topicsGitEnv(t))
	if err != nil {
		t.Fatalf("creating the git workspace: %v", err)
	}
	return ws
}

// topicsProject creates a git workspace with a project configuration file.
func topicsProject(t *testing.T, projectYAML, origin string) string {
	t.Helper()
	ws, err := node.ProjectWorkspace(filepath.Join(t.TempDir(), "repo"), projectYAML, origin, topicsGitEnv(t))
	if err != nil {
		t.Fatalf("creating the project workspace: %v", err)
	}
	return ws
}

// topicsWaitRegistered waits until the session is registered.
func topicsWaitRegistered(t *testing.T, s *harness.Session) {
	t.Helper()
	harness.Eventually(t, 3*time.Second, "the session is registered", func() (bool, string) {
		_, state := topicsList(t, s)
		if state != "" {
			return false, state
		}
		return true, ""
	})
}

// topicsList calls list_topics and returns the decoded listing. A tool error is
// reported as a state string so Eventually can retry.
func topicsList(t *testing.T, s *harness.Session) (mcpserver.ListTopicsOut, string) {
	t.Helper()
	r := s.Call(t, "list_topics", nil)
	if r.IsError {
		return mcpserver.ListTopicsOut{}, r.Text
	}
	out, err := harness.Decode[mcpserver.ListTopicsOut](string(r.Structured))
	if err != nil {
		return mcpserver.ListTopicsOut{}, "decoding list_topics: " + err.Error()
	}
	return out, ""
}

// topicsListAgents calls list_agents and returns the decoded listing.
func topicsListAgents(t *testing.T, s *harness.Session, args map[string]any) (mcpserver.ListAgentsOut, string) {
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

// topicsJoin joins name and returns the resulting topic list, retrying while
// the session is still registering.
func topicsJoin(t *testing.T, s *harness.Session, name string) mcpserver.TopicsOut {
	t.Helper()
	var out mcpserver.TopicsOut
	harness.Eventually(t, 3*time.Second, "join_topic "+name+" succeeds", func() (bool, string) {
		r := s.Call(t, "join_topic", map[string]any{"name": name})
		if r.IsError {
			return false, r.Text
		}
		var err error
		out, err = harness.Decode[mcpserver.TopicsOut](string(r.Structured))
		if err != nil {
			return false, "decoding join_topic: " + err.Error()
		}
		return true, ""
	})
	return out
}

// topicsLeave leaves name, retrying while the session is still registering.
func topicsLeave(t *testing.T, s *harness.Session, name string) {
	t.Helper()
	harness.Eventually(t, 3*time.Second, "leave_topic "+name+" succeeds", func() (bool, string) {
		r := s.Call(t, "leave_topic", map[string]any{"name": name})
		if r.IsError {
			return false, r.Text
		}
		return true, ""
	})
}

// topicsWaitJoinedSet waits until the joined topics are exactly want.
func topicsWaitJoinedSet(t *testing.T, s *harness.Session, want ...string) {
	t.Helper()
	harness.Eventually(t, 3*time.Second, "the joined topics are exactly "+strings.Join(want, ", "), func() (bool, string) {
		out, state := topicsList(t, s)
		if state != "" {
			return false, state
		}
		return topicsSameSet(out.Joined, want), fmt.Sprintf("joined topics are %v, want %v", out.Joined, want)
	})
}

// topicsWaitVisible waits until observer lists an agent whose name part is
// name.
func topicsWaitVisible(t *testing.T, observer *harness.Session, name string) {
	t.Helper()
	harness.Eventually(t, 3*time.Second, "the agent "+name+" is visible", func() (bool, string) {
		out, state := topicsListAgents(t, observer, nil)
		if state != "" {
			return false, state
		}
		if _, ok := topicsFindAgent(out, name); !ok {
			return false, name + " is not listed"
		}
		return true, ""
	})
}

// topicsWaitInvisible waits until observer no longer lists name.
func topicsWaitInvisible(t *testing.T, observer *harness.Session, name string) {
	t.Helper()
	harness.Eventually(t, 3*time.Second, "the agent "+name+" is no longer visible", func() (bool, string) {
		out, state := topicsListAgents(t, observer, nil)
		if state != "" {
			return false, state
		}
		if _, ok := topicsFindAgent(out, name); ok {
			return false, name + " is still listed"
		}
		return true, ""
	})
}

// topicsContains reports whether values contains value.
func topicsContains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// topicsSplitAddress splits "name [ref]@alias" into name and ref.
func topicsSplitAddress(addr string) (name, ref string) {
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

// topicsFindAgent returns the entry whose name part is name.
func topicsFindAgent(out mcpserver.ListAgentsOut, name string) (mcpserver.AgentOut, bool) {
	for _, agent := range out.Agents {
		if got, _ := topicsSplitAddress(agent.Address); got == name {
			return agent, true
		}
	}
	return mcpserver.AgentOut{}, false
}

// topicsSameSet reports whether got and want contain the same strings.
func topicsSameSet(got, want []string) bool {
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
