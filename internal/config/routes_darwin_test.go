//go:build darwin

package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// darwinRoute4 is the real "route -n get default" output captured on the
// macOS runner in CI run 44329785438. The gateway, the interface and the
// trailing statistics grid are that runner's.
const darwinRoute4 = `   route to: default
destination: default
       mask: default
    gateway: 192.168.64.1
  interface: en0
      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING,GLOBAL>
 recvpipe  sendpipe  ssthresh  rtt,msec    rttvar  hopcount      mtu     expire
       0         0         0         0         0         0      1500         0
`

const darwinRoute6 = `   route to: default
destination: default
       mask: default
    gateway: fe80::1%en0
  interface: en0
      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING,GLOBAL>
`

// darwinRouteNoInterface is the shape some macOS route builds print when the
// host has no default route: a description of the lookup without an interface
// line. It still names the route and its destination, so the empty result is
// the designed one rather than a parse failure.
const darwinRouteNoInterface = `   route to: default
destination: default
       mask: default
    gateway: 192.0.2.1
      flags: <UP,GATEWAY,DONE,STATIC,PRCLONING,GLOBAL>
`

// darwinRouteNotInTable is the exact "route -n get -inet6 default" output
// captured on the macOS runner in CI run 44329785438, where the host has no
// IPv6 default route. The command exits zero, so the no-route verdict comes
// from the output rather than the exit status.
const darwinRouteNotInTable = "route: writing to routing socket: not in table\n"

const darwinIfconfig = `en0: flags=8863<UP,BROADCAST,SMART,RUNNING,SIMPLEX,MULTICAST> mtu 1500
	options=400<CHANNEL_IO>
	ether 00:11:22:33:44:55
	inet6 fe80::1%en0 prefixlen 64 secured scopeid 0x4
	inet6 2001:db8::20 prefixlen 64 autoconf secured
	inet6 2001:db8::30 prefixlen 64 autoconf temporary
	inet 192.0.2.20 netmask 0xffffff00 broadcast 192.0.2.255
	inet 169.254.10.1 netmask 0xffff0000 broadcast 169.254.255.255
	nd6 options=201<PERFORMNUD,DAD>
`

func TestParseDarwinRouteInterface(t *testing.T) {
	iface, ok, err := parseDarwinRouteInterface([]byte(darwinRoute4))
	if err != nil {
		t.Fatalf("parseDarwinRouteInterface: %v", err)
	}
	if !ok || iface != "en0" {
		t.Fatalf("interface = %q, ok = %v; want en0", iface, ok)
	}
	// No interface line means no default route, not a parse failure.
	iface, ok, err = parseDarwinRouteInterface([]byte(darwinRouteNoInterface))
	if err != nil {
		t.Fatalf("parseDarwinRouteInterface without an interface line: %v", err)
	}
	if ok || iface != "" {
		t.Fatalf("interface = %q, ok = %v; want no route", iface, ok)
	}
}

// TestParseDarwinRouteInterfaceEmptyValueIsError keeps a malformed interface
// line distinguishable from the "no default route" case.
func TestParseDarwinRouteInterfaceEmptyValueIsError(t *testing.T) {
	if _, _, err := parseDarwinRouteInterface([]byte("interface:\n")); err == nil {
		t.Fatal("an empty interface value was accepted")
	}
}

func TestParseDarwinIfconfigExcludesLinkLocalAndTemporary(t *testing.T) {
	v4, err := parseDarwinIfconfig([]byte(darwinIfconfig), false)
	if err != nil {
		t.Fatalf("parseDarwinIfconfig v4: %v", err)
	}
	if len(v4) != 1 || v4[0].String() != "192.0.2.20" {
		t.Fatalf("IPv4 addresses = %v, want [192.0.2.20]", v4)
	}
	v6, err := parseDarwinIfconfig([]byte(darwinIfconfig), true)
	if err != nil {
		t.Fatalf("parseDarwinIfconfig v6: %v", err)
	}
	if len(v6) != 1 || v6[0].String() != "2001:db8::20" {
		t.Fatalf("IPv6 addresses = %v, want [2001:db8::20]", v6)
	}
}

func TestREQBrokerConfig030_DarwinDefaultRouteEnumeration(t *testing.T) {
	run := func(name string, args ...string) ([]byte, error) {
		switch name {
		case "/sbin/route":
			for _, a := range args {
				if a == "-inet6" {
					return []byte(darwinRoute6), nil
				}
			}
			return []byte(darwinRoute4), nil
		case "/sbin/ifconfig":
			if len(args) != 1 || args[0] != "en0" {
				return nil, fmt.Errorf("unexpected ifconfig args %v", args)
			}
			return []byte(darwinIfconfig), nil
		default:
			return nil, fmt.Errorf("unexpected command %s", name)
		}
	}

	got, err := defaultRouteAddressesFrom(run)
	if err != nil {
		t.Fatalf("defaultRouteAddressesFrom: %v", err)
	}
	want := []netip.Addr{
		netip.MustParseAddr("192.0.2.20"),
		netip.MustParseAddr("2001:db8::20"),
	}
	if len(got) != len(want) {
		t.Fatalf("addresses = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("addresses = %v, want %v", got, want)
		}
	}
}

func TestDarwinDefaultRouteAbsent(t *testing.T) {
	run := func(name string, args ...string) ([]byte, error) {
		if name == "/sbin/route" {
			return []byte(darwinRouteNotInTable), errors.New("exit status 1")
		}
		t.Fatalf("unexpected command %s", name)
		return nil, nil
	}
	got, err := defaultRouteAddressesFrom(run)
	if err != nil {
		t.Fatalf("defaultRouteAddressesFrom: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("addresses = %v, want none", got)
	}
}

// TestDarwinRouteNotInTableWithZeroExitIsNoRoute reproduces the macOS CI
// evidence: the IPv6 lookup prints the not-in-table message and exits zero.
// The output, not the exit status, decides the meaning, so the IPv6 path must
// yield no route rather than a parse error, and the whole enumeration must
// return an empty list with a nil error.
func TestDarwinRouteNotInTableWithZeroExitIsNoRoute(t *testing.T) {
	run := func(name string, args ...string) ([]byte, error) {
		if name != "/sbin/route" {
			t.Fatalf("unexpected command %s", name)
		}
		if slices.Contains(args, "-inet6") {
			return []byte(darwinRouteNotInTable), nil
		}
		return []byte(darwinRouteNoInterface), nil
	}

	iface, ok, err := darwinDefaultInterface(run, true)
	if err != nil {
		t.Fatalf("darwinDefaultInterface for the IPv6 default route: %v", err)
	}
	if ok || iface != "" {
		t.Fatalf("IPv6 interface = %q, ok = %v; want no route", iface, ok)
	}

	got, err := defaultRouteAddressesFrom(run)
	if err != nil {
		t.Fatalf("defaultRouteAddressesFrom: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("addresses = %v, want none", got)
	}
}

// TestDarwinRouteOutputWithoutRouteLinesIsError pins the boundary of the
// designed empty result: output that never even names the route lookup is
// garbage, not "no default route", so it must be reported.
func TestDarwinRouteOutputWithoutRouteLinesIsError(t *testing.T) {
	if _, _, err := parseDarwinRouteInterface([]byte("some unrelated output\n")); err == nil {
		t.Fatal("output without a route to or destination line was accepted")
	}
}

// TestDarwinIfconfigFailureIsReported keeps an ifconfig failure fatal instead
// of turning it into an empty address list.
func TestDarwinIfconfigFailureIsReported(t *testing.T) {
	run := func(name string, args ...string) ([]byte, error) {
		switch name {
		case "/sbin/route":
			return []byte(darwinRoute4), nil
		case "/sbin/ifconfig":
			return []byte("ifconfig: interface en0 does not exist"), errors.New("exit status 1")
		default:
			t.Fatalf("unexpected command %s", name)
			return nil, nil
		}
	}
	_, err := defaultRouteAddressesFrom(run)
	if err == nil {
		t.Fatal("an ifconfig failure was swallowed")
	}
	if !strings.Contains(err.Error(), "ifconfig") {
		t.Fatalf("error = %v, want it to name ifconfig", err)
	}
}

// TestDarwinRouteOutputWithoutInterfaceIsEmpty pins the other "no default
// route" shape: the command succeeds but its output never names an interface.
// Both address families must produce an empty list with no error.
func TestDarwinRouteOutputWithoutInterfaceIsEmpty(t *testing.T) {
	run := func(name string, args ...string) ([]byte, error) {
		if name != "/sbin/route" {
			t.Fatalf("unexpected command %s", name)
		}
		return []byte(darwinRouteNoInterface), nil
	}
	got, err := defaultRouteAddressesFrom(run)
	if err != nil {
		t.Fatalf("defaultRouteAddressesFrom: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("addresses = %v, want none", got)
	}
}

// TestDarwinRouteCommandNotFoundIsError keeps a genuine failure distinguishable
// from "no default route": the route tool could not be run at all.
func TestDarwinRouteCommandNotFoundIsError(t *testing.T) {
	run := func(string, ...string) ([]byte, error) {
		return nil, exec.ErrNotFound
	}
	if _, err := defaultRouteAddressesFrom(run); err == nil {
		t.Fatal("a missing route tool was treated as no default route")
	}
}

func TestDarwinRouteCommandFailureIsReported(t *testing.T) {
	run := func(name string, args ...string) ([]byte, error) {
		return []byte("route: unexpected failure"), errors.New("exit status 1")
	}
	if _, err := defaultRouteAddressesFrom(run); err == nil {
		t.Fatal("route command failure was swallowed")
	}
}

func TestDarwinIfconfigTemporaryWithoutToken(t *testing.T) {
	// A temporary address must be skipped only when the flag is present.
	out := "inet6 2001:db8::40 prefixlen 64 autoconf secured\n"
	addrs, err := parseDarwinIfconfig([]byte(out), true)
	if err != nil {
		t.Fatalf("parseDarwinIfconfig: %v", err)
	}
	if len(addrs) != 1 || !strings.HasPrefix(addrs[0].String(), "2001:db8::40") {
		t.Fatalf("addresses = %v, want the non-temporary address", addrs)
	}
}

// TestDarwinRouteParseErrorIncludesRawOutput keeps the raw route output in the
// error so a CI failure shows why parsing failed instead of only the verdict.
func TestDarwinRouteParseErrorIncludesRawOutput(t *testing.T) {
	_, _, err := parseDarwinRouteInterface([]byte("some unrelated output\nwith two lines\n"))
	if err == nil {
		t.Fatal("output without a route to or destination line was accepted")
	}
	for _, want := range []string{"some unrelated output", `\nwith two lines`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want it to contain %q", err, want)
		}
	}
}

// TestSmokeRealDefaultRouteAddresses calls the production enumerator against
// this host's real routing table. It only asserts that enumeration succeeds.
// CI runs it on macOS, so runCommand and the real route and ifconfig output
// formats are exercised here even though every other test injects its own
// runner. On failure it logs every command's arguments, exit status and raw
// output before failing, so the CI log carries the evidence.
func TestSmokeRealDefaultRouteAddresses(t *testing.T) {
	addrs, err := defaultRouteAddresses()
	if err != nil {
		logDarwinRouteDiagnostics(t)
		t.Fatalf("defaultRouteAddresses() error = %v", err)
	}
	t.Logf("defaultRouteAddresses() = %v", addrs)
	if _, err := (platformAddressSource{}).DefaultRouteAddresses(); err != nil {
		logDarwinRouteDiagnostics(t)
		t.Fatalf("platformAddressSource.DefaultRouteAddresses() error = %v", err)
	}
}

// logDarwinRouteDiagnostics reruns the enumerator with a runner that logs every
// command's arguments, exit status and raw output. It runs only after a smoke
// test failure, so the extra subprocesses cost nothing on success.
func logDarwinRouteDiagnostics(t *testing.T) {
	t.Helper()
	run := func(name string, args ...string) ([]byte, error) {
		out, err := runCommand(name, args...)
		t.Logf("command %s %s: err=%v, output=%q", name, strings.Join(args, " "), err, string(out))
		return out, err
	}
	if _, err := defaultRouteAddressesFrom(run); err != nil {
		t.Logf("defaultRouteAddressesFrom with logging: %v", err)
	}
}
