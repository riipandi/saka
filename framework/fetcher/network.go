package fetcher

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"syscall"
)

// errDestinationRefused is the dial-time refusal. classify turns it into
// ErrDestinationRefused; the retry condition never retries it, because the
// address is a fact about the destination, not a transient failure.
var errDestinationRefused = errors.New("fetcher: destination address refused")

// refusedRanges are the address ranges outside the public internet that an
// outbound call must not reach unless the deployment says so: the ranges
// netip classifies (loopback, private, link-local, unspecified, multicast)
// do not cover carrier-grade NAT, which cloud metadata services use
// (100.100.100.200), the IETF protocol block, the benchmarking block or the
// reserved class E.
var refusedRanges = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

// refusedAddress reports whether the policy keeps an outbound call from this
// address. An IPv4-mapped IPv6 address is judged as the IPv4 address it names.
func refusedAddress(address netip.Addr) bool {
	address = address.Unmap()
	if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() || address.IsInterfaceLocalMulticast() ||
		address.IsMulticast() || address.IsUnspecified() {
		return true
	}
	for _, prefix := range refusedRanges {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

// dialGuard is the net.Dialer.Control that enforces the policy. It runs after
// the name was resolved, with the address the socket is about to connect to,
// so a hostname that answers a public address to a pre-check and a private one
// to the dial — DNS rebinding — is refused here, and so is every redirect hop
// and every retry: each of them dials.
func dialGuard(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %s", errDestinationRefused, "the dial address is unreadable")
	}
	parsed, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("%w: %s", errDestinationRefused, "the dial address is not an IP")
	}
	if refusedAddress(parsed) {
		return errDestinationRefused
	}
	return nil
}
