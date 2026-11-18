package harness

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/test/e2e/ctl"
)

// agentSocketBound bounds the wait for a node agent's control socket after
// compose up returns.
const agentSocketBound = 10 * time.Second

// Topology describes one compose project: which file to start, which services
// it has, which network each service is addressed on, and what to do once the
// containers are running. The fault work item adds its topologies with the
// same fields.
type Topology struct {
	// file is the file name under compose/ that describes the project.
	file string
	// nodes are the compose service names, in start order.
	nodes []string
	// networks names the network each node is addressed on.
	networks map[string]string
	// env adds compose variables on top of E2E_IMAGE and CTL_<NODE>.
	env map[string]string
	// init runs once every node agent answers, before Up returns.
	init func(t *testing.T, c *Cluster)
}

// TopoSingle is one host on one internal network.
var TopoSingle = Topology{
	file:     "single.yaml",
	nodes:    []string{"hostA"},
	networks: map[string]string{"hostA": "net"},
}

// Cluster is one running compose project. A test drives its nodes through the
// same Node and Session types as a local node.
type Cluster struct {
	t           *testing.T
	project     string
	composePath string
	env         []string
	image       string

	ctlDirs map[string]string
	order   []string

	mu           sync.Mutex
	nodes        map[string]*Node
	clients      map[string]*ctl.Client
	containerIDs map[string]string

	downOnce sync.Once
}

// Up starts the compose project described by topo and returns it once every
// node agent answers. The project is torn down when the test ends.
func Up(t *testing.T, topo Topology) *Cluster {
	t.Helper()
	if len(topo.nodes) == 0 {
		t.Fatalf("harness: topology %q has no nodes", topo.file)
	}

	image := Image(t)
	project := projectName(t.Name())
	dir := t.TempDir()
	source, err := composeSourcePath(topo.file)
	if err != nil {
		t.Fatalf("harness: locating compose file %s: %v", topo.file, err)
	}
	composeData, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("harness: reading compose file %s: %v", topo.file, err)
	}
	composePath := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(composePath, composeData, 0o644); err != nil {
		t.Fatalf("harness: writing %s: %v", composePath, err)
	}

	c := &Cluster{
		t:            t,
		project:      project,
		composePath:  composePath,
		image:        image,
		ctlDirs:      map[string]string{},
		order:        append([]string(nil), topo.nodes...),
		nodes:        map[string]*Node{},
		clients:      map[string]*ctl.Client{},
		containerIDs: map[string]string{},
	}

	// Each node gets a host directory mounted at /ctl. It is a t.TempDir, so
	// the host user owns it and the container's peer account, which carries
	// the same uid, can write there.
	env := []string{"E2E_IMAGE=" + image}
	for _, node := range topo.nodes {
		dir := t.TempDir()
		c.ctlDirs[node] = dir
		env = append(env, "CTL_"+strings.ToUpper(node)+"="+dir)
	}
	for name, value := range topo.env {
		env = append(env, name+"="+value)
	}
	c.env = append(os.Environ(), env...)

	// Register the teardown after the temporary directories so it runs before
	// their cleanup removes the mounted directories.
	t.Cleanup(c.cleanup)

	up := c.composeCommand("up", "-d", "--wait", "--wait-timeout", "60")
	if out, err := up.CombinedOutput(); err != nil {
		t.Fatalf("harness: starting compose project %s: %v\n%s", project, err, strings.TrimSpace(string(out)))
	}

	for _, node := range topo.nodes {
		node := node
		socket := filepath.Join(c.ctlDirs[node], "ctl.sock")
		client := ctl.Dial(socket)
		c.clients[node] = client
		Eventually(t, agentSocketBound, "node agent "+node, func() (bool, string) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := client.Procs(ctx, ctl.ProcsRequest{Kind: "all"}); err != nil {
				return false, err.Error()
			}
			return true, ""
		})
	}

	if topo.init != nil {
		topo.init(t, c)
	}
	return c
}

// Node returns the node named name. The same Node is returned on every call.
func (c *Cluster) Node(name string) *Node {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n, ok := c.nodes[name]; ok {
		return n
	}
	dir, ok := c.ctlDirs[name]
	if !ok {
		c.t.Fatalf("harness: cluster %s has no node %q", c.project, name)
		return nil
	}
	n := &Node{impl: &remoteNode{
		t:       c.t,
		name:    name,
		socket:  filepath.Join(dir, "ctl.sock"),
		client:  c.clients[name],
		homeDir: "/home/peer",
	}}
	c.nodes[name] = n
	return n
}

// CTLDir returns the host directory mounted at /ctl on node.
func (c *Cluster) CTLDir(t testing.TB, node string) string {
	t.Helper()
	dir, ok := c.ctlDirs[node]
	if !ok {
		t.Fatalf("harness: cluster %s has no node %q", c.project, node)
	}
	return dir
}

// Addr returns the IPv4 address of node on network.
func (c *Cluster) Addr(node, network string) string {
	return c.address(node, network, "IPAddress")
}

// Addr6 returns the global IPv6 address of node on network.
func (c *Cluster) Addr6(node, network string) string {
	return c.address(node, network, "GlobalIPv6Address")
}

// address reads one address field from docker inspect.
func (c *Cluster) address(node, network, field string) string {
	format := fmt.Sprintf("{{with index .NetworkSettings.Networks %q}}{{.%s}}{{end}}", c.project+"_"+network, field)
	cmd := exec.Command("docker", "inspect", "--format", format, c.containerID(node))
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		c.t.Fatalf("harness: inspecting node %s on network %s: %v\n%s", node, network, err, strings.TrimSpace(string(out)))
		return ""
	}
	addr := strings.TrimSpace(string(out))
	if addr == "" {
		c.t.Fatalf("harness: node %s has no %s on network %s", node, field, network)
	}
	return addr
}

// Root runs argv as root in node and returns its standard output. A non-zero
// exit fails the test with the command output.
func (c *Cluster) Root(t testing.TB, node string, argv ...string) string {
	t.Helper()
	out, err := c.RootOK(t, node, argv...)
	if err != nil {
		t.Fatalf("harness: running %v as root in %s: %v", argv, node, err)
	}
	return out
}

// RootOK is Root that returns the error instead of failing. The returned
// string carries standard output and, on failure, standard error as well.
func (c *Cluster) RootOK(t testing.TB, node string, argv ...string) (string, error) {
	t.Helper()
	if len(argv) == 0 {
		return "", fmt.Errorf("harness: Root needs a command")
	}
	args := append([]string{"exec", "-T", "-u", "root", node}, argv...)
	cmd := c.composeCommand(args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stdout.String()+stderr.String()))
	}
	return stdout.String(), nil
}

// Down tears the project down. It is idempotent and also runs from the test
// cleanup.
func (c *Cluster) Down() {
	c.downOnce.Do(func() {
		cmd := c.composeCommand("down", "-v", "--remove-orphans", "--timeout", "1")
		if out, err := cmd.CombinedOutput(); err != nil {
			c.t.Errorf("harness: tearing down compose project %s: %v\n%s", c.project, err, strings.TrimSpace(string(out)))
		}
	})
}

// composeCommand builds one docker compose invocation for this project.
func (c *Cluster) composeCommand(args ...string) *exec.Cmd {
	argv := append([]string{"compose", "-p", c.project, "-f", c.composePath}, args...)
	cmd := exec.Command("docker", argv...)
	cmd.Env = c.env
	return cmd
}

// containerID returns the docker container id of node.
func (c *Cluster) containerID(node string) string {
	c.mu.Lock()
	id, ok := c.containerIDs[node]
	c.mu.Unlock()
	if ok {
		return id
	}

	cmd := c.composeCommand("ps", "-q", node)
	out, err := cmd.CombinedOutput()
	if err != nil {
		c.t.Fatalf("harness: finding container for node %s: %v\n%s", node, err, strings.TrimSpace(string(out)))
		return ""
	}
	id = strings.TrimSpace(string(out))
	if i := strings.IndexByte(id, '\n'); i >= 0 {
		id = id[:i]
	}
	if id == "" {
		c.t.Fatalf("harness: node %s is not running", node)
		return ""
	}
	c.mu.Lock()
	c.containerIDs[node] = id
	c.mu.Unlock()
	return id
}

// cleanup closes the control clients, collects artifacts when the test failed,
// and tears the project down.
func (c *Cluster) cleanup() {
	if c.t.Failed() {
		c.collectArtifacts()
	}
	c.mu.Lock()
	clients := c.clients
	c.clients = nil
	c.mu.Unlock()
	for _, client := range clients {
		client.Close()
	}
	c.Down()
}

// collectArtifacts writes the failing project's state to E2E_ARTIFACTS, or logs
// the tail of each log when that variable is unset.
func (c *Cluster) collectArtifacts() {
	dest := os.Getenv("E2E_ARTIFACTS")
	if dest == "" {
		for _, node := range c.order {
			for _, name := range c.nodeLogNames(node) {
				if data, err := os.ReadFile(filepath.Join(c.ctlDirs[node], name)); err == nil {
					c.t.Logf("harness: node %s, %s (tail):\n%s", node, name, tail(string(data), 50))
				}
			}
		}
		out, _ := c.composeCommand("logs", "--no-color", "--tail", "50").CombinedOutput()
		c.t.Logf("harness: compose logs (tail):\n%s", out)
		return
	}

	dir := filepath.Join(dest, c.project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.t.Errorf("harness: creating artifact directory %s: %v", dir, err)
		return
	}
	for _, node := range c.order {
		for _, name := range c.nodeLogNames(node) {
			source := filepath.Join(c.ctlDirs[node], name)
			data, err := os.ReadFile(source)
			if err != nil {
				continue
			}
			if err := os.WriteFile(filepath.Join(dir, node+"-"+name), data, 0o644); err != nil {
				c.t.Errorf("harness: writing artifact for %s: %v", source, err)
			}
		}
		for _, command := range [][]string{
			{"ip", "addr"},
			{"ip", "route"},
			{"iptables-save"},
		} {
			out, _ := c.RootOK(c.t, node, command...)
			name := node + "-" + strings.Join(command, "-") + ".txt"
			if err := os.WriteFile(filepath.Join(dir, name), []byte(out), 0o644); err != nil {
				c.t.Errorf("harness: writing artifact %s: %v", name, err)
			}
		}
	}
	out, _ := c.composeCommand("logs", "--no-color").CombinedOutput()
	if err := os.WriteFile(filepath.Join(dir, "compose.log"), out, 0o644); err != nil {
		c.t.Errorf("harness: writing compose log: %v", err)
	}
}

// nodeLogNames lists the files a node agent and its Brokers write under /ctl.
func (c *Cluster) nodeLogNames(node string) []string {
	entries, err := os.ReadDir(c.ctlDirs[node])
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	return names
}

// tail returns the last n lines of text.
func tail(text string, n int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

var (
	projectNameMu   sync.Mutex
	projectNameLast string
	projectNameSeq  atomic.Uint64
)

// projectName derives a compose project name from a test name:
// ape2e-<slug of the test name, at most 32 characters>-<six hex digits>. Two
// calls never return the same name.
func projectName(testName string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(testName) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 32 {
		slug = strings.Trim(slug[:32], "-")
	}
	if slug == "" {
		slug = "e2e"
	}

	projectNameMu.Lock()
	defer projectNameMu.Unlock()
	for {
		name := "ape2e-" + slug + "-" + randomHex6()
		if name != projectNameLast {
			projectNameLast = name
			return name
		}
	}
}

// randomHex6 returns six random hexadecimal digits. The fallback keeps two
// consecutive names different even if the system random source fails, which
// the test asserts.
func randomHex6() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return fmt.Sprintf("%06x", projectNameSeq.Add(1)&0xffffff)
}
