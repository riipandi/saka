package fetcher

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTheNetworkPolicyNamesEveryRangeOutsideThePublicInternet(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":         true,
		"::1":               true,
		"10.0.0.5":          true,
		"172.16.4.4":        true,
		"192.168.1.1":       true,
		"169.254.169.254":   true, // the cloud metadata address
		"100.100.100.200":   true, // the CGNAT metadata address the old pre-check passed
		"100.64.0.1":        true,
		"192.0.0.8":         true,
		"198.18.0.1":        true,
		"240.0.0.1":         true,
		"0.0.0.0":           true,
		"224.0.0.1":         true,
		"fe80::1":           true,
		"fc00::1":           true,
		"::ffff:127.0.0.1":  true, // an IPv4-mapped loopback is loopback
		"::ffff:10.1.2.3":   true,
		"93.184.216.34":     false,
		"8.8.8.8":           false,
		"2606:4700:4700::1": false,
		"100.128.0.1":       false, // just past the CGNAT block
		"198.20.0.1":        false, // just past the benchmarking block
	}
	for address, refused := range cases {
		assert.Equal(t, refused, refusedAddress(netip.MustParseAddr(address)), address)
	}
}

func TestTheDialGuardJudgesTheResolvedAddress(t *testing.T) {
	assert.ErrorIs(t, dialGuard("tcp", "127.0.0.1:80", nil), errDestinationRefused)
	assert.ErrorIs(t, dialGuard("tcp6", "[::1]:80", nil), errDestinationRefused)
	assert.ErrorIs(t, dialGuard("tcp", "not-an-ip:80", nil), errDestinationRefused, "a name is never a dial address; refuse rather than guess")
	assert.NoError(t, dialGuard("tcp", "93.184.216.34:443", nil))
}

// TestAClientWithThePolicyOnNeverReachesLoopback pins the whole path: a name
// that resolves to loopback is refused at dial time, the server sees nothing,
// the refusal is its own class, and no retry is spent on it.
func TestAClientWithThePolicyOnNeverReachesLoopback(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(server.Close)

	opts := fastRetries(2)
	opts.AllowPrivateNetwork = false
	client := newClient(t, opts)

	_, port, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	require.NoError(t, err)
	for _, target := range []string{server.URL, "http://localhost:" + port} {
		_, err = client.Do(t.Context(), Request{URL: target})
		require.ErrorIs(t, err, ErrDestinationRefused, target)
		assert.NotErrorIs(t, err, ErrRetryExhausted, "a refusal is permanent and spends no retry")
	}
	assert.Zero(t, hits.Load(), "the destination was never connected to")
}

func TestAClientWithThePolicyLiftedReachesLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	opts := fastRetries(0)
	opts.AllowPrivateNetwork = true
	_, err := newClient(t, opts).Do(t.Context(), Request{URL: server.URL})
	require.NoError(t, err)
}
