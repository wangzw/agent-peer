//go:build e2e

package e2e

import (
	"net/netip"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/wangzw/agent-peer/test/e2e/harness"
)

// egressProbeBound bounds one probe that must find no way out of the topology.
// A refused or unreachable destination fails at once; the bound only keeps a
// silent drop from hanging the test.
const egressProbeBound = 2 * time.Second

// egressTargets are the addresses no container may reach: a public resolver
// and the test host's own address.
var egressTargets = []struct {
	what string
	addr string
}{
	{"a public resolver", "1.1.1.1:443"},
	{"the host", "10.12.72.1:443"},
}

// egressForbiddenPrefixes are the pod and service networks of the k8s node the
// tests run on. A new host route that overlaps either one would take cluster
// traffic away from its own bridges.
var egressForbiddenPrefixes = []netip.Prefix{
	netip.MustParsePrefix("10.244.0.0/16"),
	netip.MustParsePrefix("10.96.0.0/12"),
}

// TestInfraNoEgress checks that the three routed topologies have no way out:
// a node behind the router cannot reach a public resolver or the test host.
// It also checks that starting a project adds no host route into the k8s pod
// or service networks, and that Down removes the project's docker networks.
func TestInfraNoEgress(t *testing.T) {
	t.Parallel()

	for _, topo := range []struct {
		name string
		topo harness.Topology
	}{
		{"NAT", harness.TopoNAT},
		{"PortForward", harness.TopoPortForward},
		{"MITM", harness.TopoMITM},
	} {
		topo := topo
		t.Run(topo.name, func(t *testing.T) {
			t.Parallel()

			before := egressHostRoutes(t)
			c := harness.Up(t, topo.topo)
			prefix := egressProjectPrefix(t)

			// hostB carries the default route handed out by the router, so it
			// is the node with the best chance to escape the topology.
			for _, target := range egressTargets {
				if c.TCPReachable(t, "hostB", target.addr, egressProbeBound) {
					t.Errorf("%s: hostB reached %s at %s; the routed topology must not offer a way out", topo.name, target.what, target.addr)
				}
			}

			// Read the routes once while the project is up, so a transient
			// bridge route into a k8s network is caught, and again after Down,
			// so a route that outlives the project is caught too.
			egressAssertNoForbiddenRoutes(t, before, egressHostRoutes(t), "while the project is up")
			c.Down()
			egressAssertNoForbiddenRoutes(t, before, egressHostRoutes(t), "after Down")
			egressAssertNoProjectNetworks(t, prefix, "after Down")
		})
	}
}

// TestInfraNoNetworkResidue checks on its own that a routed project leaves no
// docker network behind after Down, so the residue check also runs when the
// egress assertions are not part of the selection.
func TestInfraNoNetworkResidue(t *testing.T) {
	t.Parallel()

	c := harness.Up(t, harness.TopoNAT)
	prefix := egressProjectPrefix(t)

	c.Down()
	egressAssertNoProjectNetworks(t, prefix, "after Down")
}

// egressProjectPrefix returns the compose project prefix of the running
// cluster and fails when no docker network carries it. The harness does not
// export the project name, so the slug rules of its projectName are mirrored
// here; the container networks are named <project>_<network>.
func egressProjectPrefix(t *testing.T) string {
	t.Helper()
	prefix := "ape2e-" + egressSlug(t.Name()) + "-"
	for _, name := range egressDockerNetworks(t) {
		if strings.HasPrefix(name, prefix) {
			return prefix
		}
	}
	t.Fatalf("no docker network starts with the project prefix %q", prefix)
	return ""
}

// egressSlug mirrors harness.projectName's slug of a test name: lowercase
// ASCII letters, digits and dashes, at most 32 characters.
func egressSlug(testName string) string {
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
	return slug
}

// egressDockerNetworks returns the names of the host's docker networks.
func egressDockerNetworks(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("docker", "network", "ls", "--format", "{{.Name}}").CombinedOutput()
	if err != nil {
		t.Fatalf("listing docker networks: %v: %s", err, strings.TrimSpace(string(out)))
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names
}

// egressHostRoutes returns the host's route table keyed by destination prefix,
// with the raw line as the value. It reads the IPv4 table and, when the host
// has one, the IPv6 table, because either can carry a bridge route.
func egressHostRoutes(t *testing.T) map[netip.Prefix]string {
	t.Helper()
	dests := map[netip.Prefix]string{}
	for _, args := range [][]string{{"route", "show"}, {"-6", "route", "show"}} {
		out, err := exec.Command("ip", args...).CombinedOutput()
		if err != nil {
			if args[0] == "-6" {
				t.Logf("reading the host's IPv6 routes: %v", err)
				continue
			}
			t.Fatalf("reading the host's routes: %v: %s", err, strings.TrimSpace(string(out)))
		}
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			dst := fields[0]
			switch dst {
			case "blackhole", "unreachable", "prohibit", "throw":
				if len(fields) < 2 {
					continue
				}
				dst = fields[1]
			}
			if dst == "default" {
				continue
			}
			prefix, err := netip.ParsePrefix(dst)
			if err != nil {
				continue
			}
			dests[prefix] = line
		}
	}
	return dests
}

// egressAssertNoForbiddenRoutes fails for every destination that appeared
// between before and after and overlaps a k8s pod or service network. An
// exact subnet, a subnet inside the range and a broader route that covers it
// all take cluster traffic away from the cluster's own bridges.
func egressAssertNoForbiddenRoutes(t *testing.T, before, after map[netip.Prefix]string, when string) {
	t.Helper()
	for prefix, line := range after {
		if _, ok := before[prefix]; ok {
			continue
		}
		for _, forbidden := range egressForbiddenPrefixes {
			if prefix.Overlaps(forbidden) {
				t.Errorf("%s: the project added the host route %q, which overlaps the k8s network %s", when, line, forbidden)
			}
		}
	}
}

// egressAssertNoProjectNetworks fails for every docker network whose name
// carries the project prefix.
func egressAssertNoProjectNetworks(t *testing.T, prefix, when string) {
	t.Helper()
	for _, name := range egressDockerNetworks(t) {
		if strings.HasPrefix(name, prefix) {
			t.Errorf("%s: the docker network %s remains; Down must remove every network of the project", when, name)
		}
	}
}
