package webhook

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// THE CLOUD METADATA ADDRESS IS THE WHOLE POINT.
//
// 169.254.169.254 hands out instance credentials to anything that asks on most
// cloud providers. A webhook endpoint is a user-supplied URL that the SERVER opens
// a connection to, so an unvalidated one turns "register a webhook" into "read
// this instance's IAM credentials". Every test below is that one concern; the rest
// is bookkeeping.

func mustIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	require.NotNil(t, ip, "test fixture %q is not an IP", s)
	return ip
}

func TestTheMetadataAddressIsRejected(t *testing.T) {
	// The single most important assertion in this file.
	err := ValidateTarget("http://169.254.169.254/latest/meta-data/iam/",
		[]net.IP{mustIP(t, "169.254.169.254")})

	require.Error(t, err)
	var unsafe *ErrUnsafeTarget
	require.ErrorAs(t, err, &unsafe)
	assert.Equal(t, "resolves to a non-public address", unsafe.Reason)
}

func TestEveryNonPublicRangeIsRejected(t *testing.T) {
	cases := []struct {
		ip    string
		why   string
		notes string
	}{
		{"127.0.0.1", "loopback", "the instance itself"},
		{"127.1.2.3", "the whole 127/8 is loopback, not just 127.0.0.1", ""},
		{"::1", "IPv6 loopback", ""},
		{"10.0.0.5", "RFC1918", "the instance's own VPC"},
		{"172.16.0.1", "RFC1918", "and 172.16-172.31, the range people forget"},
		{"172.31.255.254", "RFC1918 upper bound", ""},
		{"192.168.1.1", "RFC1918", "a home router"},
		{"169.254.169.254", "link-local", "CLOUD CREDENTIALS"},
		{"fe80::1", "IPv6 link-local", ""},
		{"0.0.0.0", "unspecified", "routes to localhost on most stacks"},
		{"::", "unspecified v6", ""},
		{"224.0.0.1", "multicast", "not a destination under any reading"},
		{"ff02::1", "multicast v6", ""},
		{"100.64.0.1", "CGNAT", "RFC 6598 shared space: NOT RFC1918, routinely " +
			"internal in cloud deployments. Missing this is a common bypass because " +
			"it looks public"},
		{"::ffff:127.0.0.1", "IPv4-mapped loopback", "IS 127.0.0.1. A 16-byte prefix " +
			"comparison misses it entirely; net.IP.IsLoopback handles the conversion"},
		{"::ffff:169.254.169.254", "IPv4-mapped metadata", "same bypass, same " +
			"cloud credentials, in the form that fools a naive checker"},
	}

	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			assert.False(t, isPublicIP(mustIP(t, tc.ip)),
				"%s must not be treated as a public destination (%s) %s",
				tc.ip, tc.why, tc.notes)
		})
	}
}

func TestPublicAddressesAreAccepted(t *testing.T) {
	for _, ip := range []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700::1111"} {
		assert.True(t, isPublicIP(mustIP(t, ip)), "%s is public", ip)
	}
}

// A NIL address means resolution failed. "I could not tell" must not mean
// "probably fine" in the function that decides whether to open a socket.
func TestANilAddressIsRejected(t *testing.T) {
	assert.False(t, isPublicIP(nil))
	assert.False(t, isPublicIP(net.IP{}))
}

// The scheme check is not about privacy -- file:// and gopher:// are protocol
// handlers that read local files, and a client that follows them is the bug.
func TestOnlyHTTPSchemesAreAccepted(t *testing.T) {
	public := []net.IP{mustIP(t, "1.1.1.1")}

	for _, raw := range []string{
		"file:///etc/passwd",
		"gopher://1.1.1.1/",
		"ftp://1.1.1.1/",
		"dict://1.1.1.1:11211/",
		"gopher+sock5://1.1.1.1/",
		"ws://1.1.1.1/",
	} {
		err := ValidateTarget(raw, public)
		require.Error(t, err, "%s must be rejected", raw)

		var unsafe *ErrUnsafeTarget
		require.ErrorAs(t, err, &unsafe)
		assert.Equal(t, "scheme must be http or https", unsafe.Reason, "%s", raw)
	}

	for _, raw := range []string{"http://1.1.1.1/hook", "https://1.1.1.1/hook",
		"HTTPS://1.1.1.1/hook"} {
		assert.NoError(t, ValidateTarget(raw, public), "%s must be accepted", raw)
	}

	// A bare host with no scheme is a relative reference, and treating it as
	// http:// is a guess.
	err := ValidateTarget("1.1.1.1/hook", public)
	require.Error(t, err)
	var unsafe *ErrUnsafeTarget
	require.ErrorAs(t, err, &unsafe)
	assert.Equal(t, "no scheme", unsafe.Reason)
}

// THE PARSER-CONFUSION CASE, and it is the subtlest one here because no amount of
// IP checking catches it: the host a naive reader sees is not the host contacted.
func TestUserinfoCannotDisguiseTheRealHost(t *testing.T) {
	// The URL says expected.example.com. The connection goes to 127.0.0.1.
	err := ValidateTarget("http://expected.example.com@127.0.0.1/hook",
		[]net.IP{mustIP(t, "1.1.1.1")})
	require.Error(t, err,
		"the userinfo is 127.0.0.1 and the real host is 127.0.0.1 -- the "+
			"credentials are a disguise and the address is the target. Resolving "+
			"'expected.example.com' and approving on that answer would be a "+
			"complete bypass")

	var unsafe *ErrUnsafeTarget
	require.ErrorAs(t, err, &unsafe)
	assert.Equal(t, "userinfo is not allowed", unsafe.Reason)
}

// DNS REBINDING: a name with two A records, one public and one private. Checking
// only the FIRST address is checking the one the attacker chose to put first.
func TestEveryResolvedAddressIsCheckedNotJustTheFirst(t *testing.T) {
	public := mustIP(t, "1.1.1.1")
	private := mustIP(t, "169.254.169.254")

	// Public first -- the case a first-address-only check passes.
	err := ValidateTarget("http://rebind.example.com/hook",
		[]net.IP{public, private})
	require.Error(t, err,
		"the second address is the one a rebinding resolver would hand out on "+
			"the retry, so a check that only reads the first is a check the "+
			"attacker decides the outcome of")
	assert.Contains(t, err.Error(), "non-public")

	// Private first -- obviously rejected, and worth pinning so a future change
	// that reverses the loop does not accidentally make this the only case.
	require.Error(t, ValidateTarget("http://rebind.example.com/hook",
		[]net.IP{private, public}))
}

func TestAHostThatDoesNotResolveIsRejected(t *testing.T) {
	err := ValidateTarget("http://nothing.example.com/hook", nil)
	require.Error(t, err)

	var unsafe *ErrUnsafeTarget
	require.ErrorAs(t, err, &unsafe)
	assert.Equal(t, "host did not resolve", unsafe.Reason,
		"an empty resolution is not permission")
}

// Local NAMES are rejected before resolution, so a name in /etc/hosts cannot reach
// the IP check with a stale or missing answer.
func TestLocalHostNamesAreRejected(t *testing.T) {
	public := []net.IP{mustIP(t, "1.1.1.1")}

	for _, raw := range []string{
		"http://localhost/hook",
		"http://localhost:8080/hook",
		"http://LOCALHOST/hook",
		"http://localhost./hook", // trailing dot: the same host to DNS
		"http://localhost.localdomain/hook",
		"http://ip6-localhost/hook",
		"http://metadata.google.internal/hook",
		"http://instance-data/hook",
	} {
		err := ValidateTarget(raw, public)
		require.Error(t, err, "%s must be rejected", raw)

		var unsafe *ErrUnsafeTarget
		require.ErrorAs(t, err, &unsafe)
		assert.Equal(t, "local host name", unsafe.Reason, "%s", raw)
	}
}

// A trailing dot is a common way to slip past a string comparison, so it is
// trimmed before EVERY name check.
func TestATrailingDotDoesNotBypassTheNameCheck(t *testing.T) {
	assert.True(t, isLocalName("localhost."))
	assert.True(t, isLocalName("LOCALHOST."))
	assert.True(t, isLocalName("LocalHost"))

	// And through the full validator, not just the helper.
	require.Error(t, ValidateTarget("http://localhost./hook",
		[]net.IP{mustIP(t, "1.1.1.1")}))
}

// Ports are NOT restricted, and the reason is worth pinning because a later
// reviewer will be tempted to add a port allowlist.
func TestNonStandardPortsAreAccepted(t *testing.T) {
	assert.NoError(t, ValidateTarget("http://1.1.1.1:9000/hook",
		[]net.IP{mustIP(t, "1.1.1.1")}),
		"a webhook on a non-standard port is a legitimate want. The IP check is "+
			"what matters; a port allowlist stops real users and blocks nothing an "+
			"attacker wants, since the metadata service needs no exotic port")

	assert.NoError(t, ValidateTarget("https://1.1.1.1:65535/hook",
		[]net.IP{mustIP(t, "1.1.1.1")}))
}

// The error must not echo the secret-bearing part of the URL, and it must name the
// reason so a test can assert on something stable.
func TestTheRejectionErrorNamesTheHostAndTheReason(t *testing.T) {
	err := ValidateTarget("http://10.0.0.1/hook", []net.IP{mustIP(t, "10.0.0.1")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "10.0.0.1")
	assert.Contains(t, err.Error(), "non-public",
		"the reason is the part a test can assert on; prose is not")
}
