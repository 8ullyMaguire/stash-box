package email

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stashapp/stash-box/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #734: "SMTP over TLS is unsupported" -- implicit TLS (port 465) could
// not be used at all, only STARTTLS on 587.
//
// The distinction is what the bytes on the wire look like, not a config label:
//
//	implicit TLS  client speaks TLS immediately; the first byte the server
//	              ever sees is a TLS ClientHello, never "EHLO".
//	STARTTLS      client sends "EHLO" in cleartext first, then upgrades.
//
// A round-trip test against a mock we control would agree with whatever the
// client does, so it cannot tell these apart on its own. These tests therefore
// assert the WIRE behaviour, not merely that Send() returned no error: the
// listener records whether the first thing it read was a TLS handshake.

// implicitTLSServer is a listener that speaks implicit TLS, i.e. what a port 465
// server does. It records how the connection started.
type implicitTLSServer struct {
	addr string

	mu sync.Mutex
	// sawPlaintextFirst is true if the client sent any SMTP command before the
	// TLS handshake. That is the STARTTLS shape and is wrong for port 465.
	sawPlaintextFirst bool
	// tlsHandshakeOK records that a TLS connection was actually established.
	tlsHandshakeOK bool
}

// startImplicitTLSServer runs a minimal SMTP server that requires implicit TLS
// and speaks just enough of the protocol to accept one message.
func startImplicitTLSServer(t *testing.T) *implicitTLSServer {
	t.Helper()

	cert, caPEM, err := generateSelfSignedCert()
	require.NoError(t, err)

	// go-mail verifies against the system pool. Rather than weakening
	// verification in production code, point the TLS stack at a file holding
	// this test's CA. Go's crypto/x509 honours SSL_CERT_FILE on Linux, so the
	// client performs REAL certificate verification against a CA it trusts --
	// which is the behaviour production gets.
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, caPEM, 0o600))
	t.Setenv("SSL_CERT_FILE", caFile)

	srv := &implicitTLSServer{}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{cert},
	})
	require.NoError(t, err)

	srv.addr = ln.Addr().String()

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go srv.handle(conn)
		}
	}()

	t.Cleanup(func() { _ = ln.Close() })

	return srv
}

func (s *implicitTLSServer) handle(conn net.Conn) {
	defer conn.Close()

	// tls.Conn.Accept on the server side completes the handshake. If the client
	// had spoken plaintext SMTP, this is where it would fail.
	if tc, ok := conn.(*tls.Conn); ok {
		_ = tc.SetDeadline(time.Now().Add(10 * time.Second))
		if err := tc.Handshake(); err != nil {
			return
		}
		s.mu.Lock()
		s.tlsHandshakeOK = true
		s.mu.Unlock()
	}

	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)

	// RFC 5321 4.1.1.1: the server speaks first with a 220 greeting. Go's
	// net/smtp reads this inside smtp.NewClient, before any command is sent,
	// so omitting it makes every client see EOF at dial time.
	if _, err := w.WriteString("220 mail.example.com ESMTP ready\r\n"); err != nil {
		return
	}
	if err := w.Flush(); err != nil {
		return
	}

	for {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}

		cmd := firstWord(line)
		switch cmd {
		case "EHLO", "HELO":
			_, _ = w.WriteString("250-mail.example.com\r\n")
			_, _ = w.WriteString("250 SIZE 10240000\r\n")
		case "DATA":
			// RFC 5321 4.1.1.4: 354 tells the client to send the message.
			// Replying 250 here means the client never sends a body and the
			// session stalls.
			_, _ = w.WriteString("354 End data with <CR><LF>.<CR><LF>\r\n")
			if err := w.Flush(); err != nil {
				return
			}
			// Consume the message body until the lone dot.
			for {
				_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" || l == ".\n" {
					break
				}
			}
			_, _ = w.WriteString("250 OK queued\r\n")
		case "QUIT":
			_, _ = w.WriteString("221 mail.example.com closing connection\r\n")
			_ = w.Flush()
			return
		case "MAIL", "RCPT":
			_, _ = w.WriteString("250 OK\r\n")
		default:
			_, _ = w.WriteString("250 OK\r\n")
		}

		if err := w.Flush(); err != nil {
			return
		}
	}
}

func (s *implicitTLSServer) handshakeSucceeded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tlsHandshakeOK
}

func firstWord(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' || s[i] == '\r' || s[i] == '\n' {
			return s[:i]
		}
	}
	return s
}

// generateSelfSignedCert returns a self-signed CA cert plus its PEM encoding,
// so the test client can be told to trust it via SSL_CERT_FILE.
func generateSelfSignedCert() (tls.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mail.example.com"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// configure points the email config at a host/port/mode triple for one test.
// No credentials: the local server does not speak AUTH, and go-mail's default
// of always attempting it fails with "535 Authentication not implemented".
func configure(t *testing.T, host string, port int, tlsMode string) {
	t.Helper()

	restore := config.SetEmailSettingsForTest(host, port, "", "", "stash@example.com", tlsMode)
	t.Cleanup(restore)
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	require.NoError(t, err)
	return host, port
}

// The regression for #734: with mode "implicit", the client must complete a TLS
// handshake with a 465-style server. Before the fix, "implicit" was not a
// recognized value, so it fell through to "mandatory" (STARTTLS) and the
// handshake against an implicit-TLS listener could never succeed.
func TestSendImplicitTLSEstablishesTLSBeforeSMTP(t *testing.T) {
	srv := startImplicitTLSServer(t)
	host, port := splitHostPort(t, srv.addr)
	configure(t, host, port, "implicit")

	m := NewManager()
	err := m.Send("user@example.com", "subject", "text body", "<p>html body</p>")

	require.NoError(t, err, "implicit TLS send should succeed against a port 465 server")
	assert.True(t, srv.handshakeSucceeded(),
		"the connection must be wrapped in TLS -- an implicit-TLS server never sees plaintext")
}

func TestGetEmailTLSModeRecognizesImplicit(t *testing.T) {
	// "implicit" must be recognized by the config layer, otherwise it silently
	// degrades to STARTTLS and the client waits for an EHLO a 465 server never
	// sends. This pins the exact bug.
	restore := config.SetEmailSettingsForTest("h", 465, "", "", "stash@example.com", "implicit")
	defer restore()

	assert.Equal(t, "implicit", config.GetEmailTLSMode(),
		"\"implicit\" must be a recognized mode, not silently downgraded to STARTTLS (#734)")

	// The existing modes must keep working.
	for _, mode := range []string{"mandatory", "opportunistic", "none"} {
		restoreMode := config.SetEmailSettingsForTest("h", 25, "", "", "stash@example.com", mode)
		assert.Equal(t, mode, config.GetEmailTLSMode(), "mode %q should be preserved", mode)
		restoreMode()
	}

	// Unknown values still fail closed.
	restoreUnknown := config.SetEmailSettingsForTest("h", 25, "", "", "stash@example.com", "banana")
	assert.Equal(t, "mandatory", config.GetEmailTLSMode(),
		"an unrecognized mode must fall back to the secure default")
	restoreUnknown()
}

// The distinguishing test: mode "mandatory" against the SAME implicit-TLS
// server must FAIL, because STARTTLS needs a plaintext EHLO first. If this ever
// passes, the two modes have stopped being distinguishable and the test above
// would no longer be proving anything.
func TestSendMandatoryFailsAgainstImplicitTLSServer(t *testing.T) {
	srv := startImplicitTLSServer(t)
	host, port := splitHostPort(t, srv.addr)
	configure(t, host, port, "mandatory")

	m := NewManager()
	err := m.Send("user@example.com", "subject", "text body", "<p>html body</p>")

	require.Error(t, err,
		"STARTTLS cannot talk to an implicit-TLS listener; this difference is what makes the implicit test meaningful")
	assert.False(t, srv.handshakeSucceeded(),
		"no TLS session should have been established by the STARTTLS client")
}
