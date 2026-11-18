//go:build darwin

package config

import (
	"fmt"
	"net/netip"
	"os/exec"
	"slices"
	"strings"
)

// defaultRouteAddresses returns the addresses of the interfaces carrying the
// default routes. IPv4 addresses come first, then IPv6 addresses; within a
// family the addresses are sorted by value.
//
// macOS does not expose IFA_F_TEMPORARY through net.InterfaceAddrs, so the
// addresses are read from ifconfig, whose IPv6 lines mark temporary
// addresses with the "temporary" flag.
func defaultRouteAddresses() ([]netip.Addr, error) {
	return defaultRouteAddressesFrom(runCommand)
}

type commandRunner func(name string, args ...string) ([]byte, error)

func runCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// defaultRouteAddressesFrom is the testable core of defaultRouteAddresses.
// The command runner lets tests supply fixed route and ifconfig output
// without running the real tools.
func defaultRouteAddressesFrom(run commandRunner) ([]netip.Addr, error) {
	var out []netip.Addr

	iface4, ok, err := darwinDefaultInterface(run, false)
	if err != nil {
		return nil, err
	}
	if ok {
		data, err := run("/sbin/ifconfig", iface4)
		if err != nil {
			return nil, fmt.Errorf("config: ifconfig %s: %w: %s", iface4, err, darwinOutputSnippet(data))
		}
		addrs, err := parseDarwinIfconfig(data, false)
		if err != nil {
			return nil, fmt.Errorf("config: ifconfig %s: %w: %s", iface4, err, darwinOutputSnippet(data))
		}
		out = append(out, addrs...)
	}

	iface6, ok, err := darwinDefaultInterface(run, true)
	if err != nil {
		return nil, err
	}
	if ok {
		data, err := run("/sbin/ifconfig", iface6)
		if err != nil {
			return nil, fmt.Errorf("config: ifconfig %s: %w: %s", iface6, err, darwinOutputSnippet(data))
		}
		addrs, err := parseDarwinIfconfig(data, true)
		if err != nil {
			return nil, fmt.Errorf("config: ifconfig %s: %w: %s", iface6, err, darwinOutputSnippet(data))
		}
		out = append(out, addrs...)
	}

	out = dedupeAddrs(out)
	slices.SortFunc(out, netip.Addr.Compare)
	return out, nil
}

// darwinDefaultInterface runs "route -n get [-inet6] default" and returns the
// interface named on its "interface:" line. A missing default route is not an
// error: ok is false in that case. The route tool reports a host without a
// default route either with a "not in table" style message or, on some builds,
// with a route description that carries no interface line; both mean "no
// default route".
//
// The "not in table" message is recognized from the output regardless of the
// exit status, because macOS is not consistent about it: an absent IPv6
// default route prints "route: writing to routing socket: not in table" and
// exits zero, while an absent IPv4 default route prints the same message with
// a nonzero status. A command that could not run, that failed for any other
// reason, or whose successful output does not describe a route at all, is an
// error.
func darwinDefaultInterface(run commandRunner, inet6 bool) (iface string, ok bool, err error) {
	args := []string{"-n", "get"}
	if inet6 {
		args = append(args, "-inet6")
	}
	args = append(args, "default")
	out, err := run("/sbin/route", args...)
	if isDarwinNoRoute(out) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("config: route %s: %w: %s", strings.Join(args, " "), err, darwinOutputSnippet(out))
	}
	return parseDarwinRouteInterface(out)
}

// darwinOutputSnippet formats raw command output for an error message. It
// trims surrounding whitespace, truncates the result to a safe length, and
// escapes line breaks so the whole message stays on one line in CI logs.
func darwinOutputSnippet(out []byte) string {
	const maxBytes = 500
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "<empty>"
	}
	if len(s) > maxBytes {
		s = strings.ToValidUTF8(s[:maxBytes], "") + "...(truncated)"
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	s = strings.ReplaceAll(s, "\t", `\t`)
	return s
}

func isDarwinNoRoute(out []byte) bool {
	s := strings.ToLower(string(out))
	return strings.Contains(s, "not in table") || strings.Contains(s, "no such process")
}

// parseDarwinRouteInterface returns the interface named by a route command's
// output. Output that names the route and destination but no interface line
// means the host has no default route, which is not an error. Output that does
// not describe a route at all is garbage, so it is an error rather than a
// silent empty result. An interface line with no value is malformed.
func parseDarwinRouteInterface(out []byte) (string, bool, error) {
	var iface string
	sawRoute := false
	sawInterface := false
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "route to:") || strings.HasPrefix(line, "destination:") {
			sawRoute = true
		}
		value, ok := strings.CutPrefix(line, "interface:")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return "", false, fmt.Errorf("config: route output has an empty interface: %s", darwinOutputSnippet(out))
		}
		if !sawInterface {
			iface = value
			sawInterface = true
		}
	}
	if !sawRoute {
		return "", false, fmt.Errorf("config: route output does not describe a route: %s", darwinOutputSnippet(out))
	}
	return iface, sawInterface, nil
}

// parseDarwinIfconfig extracts the IPv4 or IPv6 addresses from ifconfig
// output. IPv4 link-local addresses, IPv6 link-local addresses and IPv6
// temporary addresses are excluded.
func parseDarwinIfconfig(out []byte, inet6 bool) ([]netip.Addr, error) {
	var addrs []netip.Addr
	for i, raw := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(raw))
		if len(fields) < 2 {
			continue
		}
		keyword := "inet"
		if inet6 {
			keyword = "inet6"
		}
		if fields[0] != keyword {
			continue
		}
		text := fields[1]
		if percent := strings.IndexByte(text, '%'); percent >= 0 {
			text = text[:percent]
		}
		addr, err := netip.ParseAddr(text)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", i+1, err)
		}
		if inet6 {
			if !addr.Is6() || isIPv6LinkLocal(addr) {
				continue
			}
			if hasToken(fields[2:], "temporary") {
				continue
			}
		} else {
			if !addr.Is4() || isIPv4LinkLocal(addr) {
				continue
			}
		}
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

func hasToken(fields []string, want string) bool {
	for _, f := range fields {
		if f == want {
			return true
		}
	}
	return false
}
