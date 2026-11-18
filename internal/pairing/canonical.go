package pairing

import (
	"net"
	"net/netip"
	"strconv"
	"strings"
)

// CanonicalAddress returns a normalized host:port form for comparing two
// advertised addresses. The host is lowercased; an IP host is written in
// netip's canonical form, which also compresses IPv6; and a decimal port loses
// its leading zeros. An address without a parsable host and port is returned
// with the whole string lowercased.
func CanonicalAddress(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return strings.ToLower(addr)
	}
	host = strings.ToLower(host)
	if ip, err := netip.ParseAddr(host); err == nil {
		host = ip.String()
	}
	if n, err := strconv.Atoi(port); err == nil && n >= 0 && n <= 65535 {
		port = strconv.Itoa(n)
	}
	return net.JoinHostPort(host, port)
}
