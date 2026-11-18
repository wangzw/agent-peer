package harness

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/test/e2e/ctl"
)

// Direction selects the netfilter chain a counter watches.
type Direction int

const (
	// Inbound counts packets entering the node: the INPUT chain, SYNs from
	// peerAddr to the watched port.
	Inbound Direction = iota + 1
	// Outbound counts packets leaving the node: the OUTPUT chain, SYNs to
	// peerAddr and the watched port, excluding loopback.
	Outbound
)

// faultPlan describes how a topology is cut. A router topology inserts the
// reject rules on the router's FORWARD chain; a flat topology inserts them on
// both endpoints. peer maps each endpoint to the far end of its established
// connection as that endpoint sees it, so ss -K can tear the socket down.
// Masquerading and DNAT make that address differ from either host's own
// address: the dialer sees the destination it dialed, the acceptor sees the
// address the router rewrote the source to.
type faultPlan struct {
	router  string
	network string
	peer    map[string]string
}

var (
	faultPlanMu sync.Mutex
	faultPlans  = map[*Cluster]*faultPlan{}
	activeCuts  = map[*Cluster]*cutState{}
)

// registerFaults records how Cut and Restore should treat c. The topology's
// init function calls it once Up has created the cluster.
func registerFaults(c *Cluster, plan faultPlan) {
	faultPlanMu.Lock()
	faultPlans[c] = &plan
	faultPlanMu.Unlock()
}

// faultPlanOf returns the plan registered for c.
func faultPlanOf(t testing.TB, c *Cluster) *faultPlan {
	t.Helper()
	faultPlanMu.Lock()
	plan := faultPlans[c]
	faultPlanMu.Unlock()
	if plan == nil {
		t.Fatalf("harness: cluster %s: no fault plan; Cut needs a topology that registers one", c.project)
		return nil
	}
	return plan
}

// cutRule is one rule Cut inserted, remembered with the chain and
// specification Restore deletes it by.
type cutRule struct {
	node  string
	chain string
	spec  []string
}

// cutState is the rules of one active Cut.
type cutState struct {
	rules []cutRule
}

// Cut makes node a and node b unreachable from each other. On a topology with
// a router it rejects all forwarded traffic there; on a flat topology it
// rejects the peer address in both directions on both endpoints. Established
// connections are torn down immediately with ss -K. The test cleanup restores
// the network even when the test does not call Restore.
func (c *Cluster) Cut(t testing.TB, a, b string) {
	t.Helper()
	plan := faultPlanOf(t, c)

	faultPlanMu.Lock()
	if _, active := activeCuts[c]; active {
		faultPlanMu.Unlock()
		t.Fatalf("harness: cluster %s: Cut is already active", c.project)
		return
	}
	st := &cutState{}
	activeCuts[c] = st
	faultPlanMu.Unlock()

	insert := func(node, chain string, spec ...string) {
		c.iptables(t, node, append([]string{"-I", chain, "1"}, spec...)...)
		st.rules = append(st.rules, cutRule{node: node, chain: chain, spec: spec})
	}

	if plan.router != "" {
		// The TCP reject comes first so a connection fails with a reset
		// rather than with the ICMP unreachable of the second rule.
		insert(plan.router, "FORWARD", "-j", "REJECT", "--reject-with", "icmp-host-unreachable")
		insert(plan.router, "FORWARD", "-p", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset")
	} else {
		for _, pair := range []struct {
			node, peer string
		}{
			{a, c.Addr(b, plan.network)},
			{b, c.Addr(a, plan.network)},
		} {
			insert(pair.node, "INPUT", "-s", pair.peer, "-j", "REJECT", "--reject-with", "icmp-host-unreachable")
			insert(pair.node, "INPUT", "-s", pair.peer, "-p", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset")
			insert(pair.node, "OUTPUT", "-d", pair.peer, "-j", "REJECT", "--reject-with", "icmp-host-unreachable")
			insert(pair.node, "OUTPUT", "-d", pair.peer, "-p", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset")
		}
	}

	t.Cleanup(func() { c.restore(t) })

	// Break the connections that are already established. gRPC keepalive alone
	// would take up to its ten-second minimum to notice the cut.
	for _, node := range []string{a, b} {
		peer := plan.peer[node]
		if peer == "" {
			if node == a {
				peer = c.Addr(b, plan.network)
			} else {
				peer = c.Addr(a, plan.network)
			}
		}
		for _, selector := range []string{"dst", "src"} {
			if _, err := c.RootOK(t, node, "ss", "-K", selector, peer); err != nil {
				t.Logf("harness: node %s: ss -K %s %s: %v", node, selector, peer, err)
			}
		}
	}
}

// Restore removes the rules Cut inserted. It is idempotent and also runs from
// the test cleanup.
func (c *Cluster) Restore(t testing.TB) {
	t.Helper()
	c.restore(t)
}

// restore implements Restore and the cleanup hook.
func (c *Cluster) restore(t testing.TB) {
	t.Helper()
	faultPlanMu.Lock()
	st := activeCuts[c]
	delete(activeCuts, c)
	faultPlanMu.Unlock()
	if st == nil {
		return
	}
	for i := len(st.rules) - 1; i >= 0; i-- {
		rule := st.rules[i]
		c.iptables(t, rule.node, append([]string{"-D", rule.chain}, rule.spec...)...)
	}
}

// Block rejects all traffic from node toAddr in the node's OUTPUT chain and
// returns a function that removes the rules. The block is one-directional: the
// peer can still reach the node.
func (c *Cluster) Block(t testing.TB, from, toAddr string) (undo func()) {
	t.Helper()
	specs := [][]string{
		{"-d", toAddr, "-j", "REJECT", "--reject-with", "icmp-host-unreachable"},
		{"-d", toAddr, "-p", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset"},
	}
	for _, spec := range specs {
		c.iptables(t, from, append([]string{"-I", "OUTPUT", "1"}, spec...)...)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			for i := len(specs) - 1; i >= 0; i-- {
				c.iptables(t, from, append([]string{"-D", "OUTPUT"}, specs[i]...)...)
			}
		})
	}
}

// Unblock removes the rules Block inserted for the pair. The function Block
// returns is the usual way to undo it; Unblock exists for the case where the
// caller did not keep that function.
func (c *Cluster) Unblock(t testing.TB, from, toAddr string) {
	t.Helper()
	specs := [][]string{
		{"-d", toAddr, "-j", "REJECT", "--reject-with", "icmp-host-unreachable"},
		{"-d", toAddr, "-p", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset"},
	}
	for i := len(specs) - 1; i >= 0; i-- {
		c.iptables(t, from, append([]string{"-D", "OUTPUT"}, specs[i]...)...)
	}
}

// SYNCounter inserts a counting rule for TCP SYNs on node and returns a
// function that reads the packet count. The counter is not removed when the
// test ends; the container goes away with the project.
func (c *Cluster) SYNCounter(t testing.TB, node string, dir Direction, peerAddr string, port int) func() int64 {
	t.Helper()
	var (
		chain string
		spec  []string
	)
	switch dir {
	case Inbound:
		chain = "INPUT"
		spec = []string{"-p", "tcp", "--syn"}
		if peerAddr != "" {
			spec = append(spec, "-s", peerAddr)
		}
		spec = append(spec, "--dport", strconv.Itoa(port), "-j", "ACCEPT")
	case Outbound:
		chain = "OUTPUT"
		spec = []string{"-p", "tcp", "--syn", "!", "-o", "lo"}
		if peerAddr != "" {
			spec = append(spec, "-d", peerAddr)
		}
		spec = append(spec, "--dport", strconv.Itoa(port), "-j", "ACCEPT")
	default:
		t.Fatalf("harness: node %s: unknown direction %d", node, dir)
		return nil
	}
	c.iptables(t, node, append([]string{"-I", chain, "1"}, spec...)...)
	return func() int64 { return c.counter(t, node, chain, true) }
}

// ByteCounter inserts a counting rule for the bytes node receives from
// fromAddr on port and returns a function that reads the byte count. An empty
// fromAddr counts every source.
func (c *Cluster) ByteCounter(t testing.TB, node string, fromAddr string, port int) func() int64 {
	t.Helper()
	spec := []string{"-p", "tcp"}
	if fromAddr != "" {
		spec = append(spec, "-s", fromAddr)
	}
	spec = append(spec, "--dport", strconv.Itoa(port), "-j", "ACCEPT")
	c.iptables(t, node, append([]string{"-I", "INPUT", "1"}, spec...)...)
	return func() int64 { return c.counter(t, node, "INPUT", false) }
}

// counter reads the packets or bytes of the counter rule on chain. A SYN rule
// carries the 0x17/0x02 flag mask; the byte rule deliberately does not, which
// is how the two are told apart when both are present.
func (c *Cluster) counter(t testing.TB, node, chain string, syn bool) int64 {
	t.Helper()
	out := c.Root(t, node, "iptables", "-nvx", "-L", chain)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || !allDigits(fields[0]) || !allDigits(fields[1]) || fields[2] != "ACCEPT" {
			continue
		}
		if hasSYNMask(line) != syn {
			continue
		}
		column := fields[0]
		if !syn {
			column = fields[1]
		}
		value, err := strconv.ParseInt(column, 10, 64)
		if err != nil {
			t.Fatalf("harness: node %s: parsing counter %q: %v", node, column, err)
		}
		return value
	}
	t.Fatalf("harness: node %s: no counter rule in %s: %q", node, chain, out)
	return 0
}

// hasSYNMask reports whether an iptables rule listing matches only SYN
// packets.
func hasSYNMask(line string) bool {
	return strings.Contains(line, "flags:0x17/0x02")
}

// captureBound bounds tcpdump's startup and exit. Both are local process
// operations with no product timer, so the project-wide three-second bound
// applies.
const captureBound = 3 * time.Second

// Capture starts tcpdump on node for TCP port and returns a function that
// stops it and returns the pcap bytes. The capture file lives in the
// container's /tmp and is read back through the node agent.
func (c *Cluster) Capture(t testing.TB, node string, port int) (stop func() []byte) {
	t.Helper()
	path := fmt.Sprintf("/tmp/e2e-capture-%d.pcap", port)
	logPath := fmt.Sprintf("/tmp/e2e-capture-%d.log", port)
	pidPath := fmt.Sprintf("/tmp/e2e-capture-%d.pid", port)
	// The pid file is what lets stop address this tcpdump and no other one on
	// the node. Redirecting both streams lets the exec return while tcpdump
	// keeps running under the container's init process.
	script := fmt.Sprintf("rm -f %s %s %s; nohup tcpdump -i any -w %s -U tcp port %d >%s 2>&1 & echo $! > %s",
		path, logPath, pidPath, path, port, logPath, pidPath)
	c.Root(t, node, "sh", "-c", script)

	// tcpdump prints "listening on <device>" only after it has opened the
	// capture device. The process existing is not enough: a capture started a
	// moment later would miss the first packets.
	Eventually(t, captureBound, "tcpdump on "+node+" to attach", func() (bool, string) {
		out, err := c.RootOK(t, node, "cat", logPath)
		if err != nil {
			return false, fmt.Sprintf("reading %s: %v", logPath, err)
		}
		if !strings.Contains(out, "listening on") {
			return false, strings.TrimSpace(out)
		}
		return true, ""
	})

	pidText := strings.TrimSpace(c.Root(t, node, "cat", pidPath))
	pid, err := strconv.Atoi(pidText)
	if err != nil || pid <= 0 {
		t.Fatalf("harness: node %s: tcpdump wrote no usable pid (%q): %v", node, pidText, err)
	}

	var (
		once sync.Once
		data []byte
	)
	return func() []byte {
		once.Do(func() {
			if _, err := c.RootOK(t, node, "kill", "-INT", strconv.Itoa(pid)); err != nil {
				t.Logf("harness: node %s: stopping tcpdump %d: %v", node, pid, err)
			}
			Eventually(t, captureBound, fmt.Sprintf("tcpdump %d on %s to stop", pid, node), func() (bool, string) {
				if _, err := c.RootOK(t, node, "kill", "-0", strconv.Itoa(pid)); err == nil {
					return false, "tcpdump is still running"
				}
				return true, ""
			})
			data = c.Node(node).ReadFile(t, path)
		})
		return data
	}
}

// HoldPort asks the node agent to listen on addr for the rest of the project
// and returns the identifier ReleasePort takes. Container nodes use this in
// place of a listener on the test host.
func (c *Cluster) HoldPort(t testing.TB, node, addr string) string {
	t.Helper()
	res, err := c.control(node).HoldPort(context.Background(), ctl.PortHoldRequest{Addr: addr})
	if err != nil {
		t.Fatalf("harness: node %s: holding %s: %v", node, addr, err)
		return ""
	}
	return res.ID
}

// ReleasePort closes a port HoldPort took.
func (c *Cluster) ReleasePort(t testing.TB, node, id string) {
	t.Helper()
	if err := c.control(node).ReleasePort(context.Background(), ctl.PortReleaseRequest{ID: id}); err != nil {
		t.Fatalf("harness: node %s: releasing port %s: %v", node, id, err)
	}
}

// TCPReachable reports whether a TCP connection from node to addr succeeds
// within timeout.
func (c *Cluster) TCPReachable(t testing.TB, node, addr string, timeout time.Duration) bool {
	t.Helper()
	res, err := c.control(node).ProbeTCP(context.Background(), ctl.ProbeTCPRequest{
		Addr:      addr,
		TimeoutMS: timeout.Milliseconds(),
	})
	if err != nil {
		t.Fatalf("harness: node %s: probing %s: %v", node, addr, err)
		return false
	}
	return res.OK
}

// OpenConn opens a TCP connection from node to addr and returns the identifier
// ConnAlive takes.
func (c *Cluster) OpenConn(t testing.TB, node, addr string) string {
	t.Helper()
	res, err := c.control(node).OpenConn(context.Background(), ctl.ConnOpenRequest{Addr: addr})
	if err != nil {
		t.Fatalf("harness: node %s: opening a connection to %s: %v", node, addr, err)
		return ""
	}
	return res.ID
}

// ConnAlive reports whether the connection OpenConn made still carries data.
func (c *Cluster) ConnAlive(t testing.TB, node, id string) bool {
	t.Helper()
	res, err := c.control(node).ConnAlive(context.Background(), ctl.ConnAliveRequest{ID: id})
	if err != nil {
		t.Fatalf("harness: node %s: checking connection %s: %v", node, id, err)
		return false
	}
	return res.Alive
}

// SetDefaultRoute replaces node's IPv4 default route. An empty gateway routes
// over the node's first interface; otherwise the gateway is used.
func (c *Cluster) SetDefaultRoute(t testing.TB, node, gateway string) {
	t.Helper()
	if gateway == "" {
		c.setDefaultRouteOn(t, node, "eth0")
		return
	}
	c.Root(t, node, "ip", "route", "replace", "default", "via", gateway)
}

// setDefaultRouteOn installs a device-only default route, the form the plan
// asks for on flat networks.
func (c *Cluster) setDefaultRouteOn(t testing.TB, node, dev string) {
	t.Helper()
	c.Root(t, node, "ip", "route", "replace", "default", "dev", dev)
}

// DelDefaultRoute removes node's IPv4 default route.
func (c *Cluster) DelDefaultRoute(t testing.TB, node string) {
	t.Helper()
	c.Root(t, node, "ip", "route", "del", "default")
}

// AddDefaultRoute6 adds an IPv6 default route over the interface that carries
// node's address on net.
func (c *Cluster) AddDefaultRoute6(t testing.TB, node string) {
	t.Helper()
	addr := c.Addr6(node, "net")
	out := c.Root(t, node, "ip", "-o", "-6", "addr", "show", "to", addr)
	fields := strings.Fields(out)
	if len(fields) < 2 {
		t.Fatalf("harness: node %s: no interface carries %s: %q", node, addr, out)
	}
	dev := strings.TrimSuffix(fields[1], ":")
	c.Root(t, node, "ip", "-6", "route", "add", "default", "dev", dev)
}

// iptables runs one iptables command as root in node.
func (c *Cluster) iptables(t testing.TB, node string, args ...string) string {
	t.Helper()
	return c.Root(t, node, append([]string{"iptables"}, args...)...)
}

// control returns the node agent client of node.
func (c *Cluster) control(node string) *ctl.Client {
	c.mu.Lock()
	client := c.clients[node]
	c.mu.Unlock()
	if client == nil {
		c.t.Fatalf("harness: cluster %s has no control client for node %q", c.project, node)
	}
	return client
}

// allDigits reports whether s is a non-empty string of decimal digits.
func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
