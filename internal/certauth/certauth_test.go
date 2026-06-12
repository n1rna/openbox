package certauth

import (
	"net"
	"testing"
	"time"

	"openbox.io/openbox/internal/ca"
	"openbox.io/openbox/internal/sshkeys"
)

// fakeConn is a minimal ssh.ConnMetadata for exercising the user-auth callback.
type fakeConn struct{ user string }

func (f fakeConn) User() string          { return f.user }
func (f fakeConn) SessionID() []byte     { return []byte("sid") }
func (f fakeConn) ClientVersion() []byte { return []byte("c") }
func (f fakeConn) ServerVersion() []byte { return []byte("s") }
func (f fakeConn) RemoteAddr() net.Addr  { return &net.IPAddr{} }
func (f fakeConn) LocalAddr() net.Addr   { return &net.IPAddr{} }

func newCA(t *testing.T) *ca.CA {
	t.Helper()
	signer, err := sshkeys.NewEd25519Signer()
	if err != nil {
		t.Fatal(err)
	}
	return ca.New(signer)
}

func TestUserAuth(t *testing.T) {
	authority := newCA(t)
	other := newCA(t)
	clientKey, _ := sshkeys.NewEd25519Signer()

	cert, err := authority.SignUserCert(clientKey.PublicKey(), "alice", "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cb := UserAuthCallback(authority.PublicKey())

	t.Run("valid cert, matching principal", func(t *testing.T) {
		if _, err := cb(fakeConn{user: "alice"}, cert); err != nil {
			t.Fatalf("want accept, got %v", err)
		}
	})
	t.Run("wrong principal rejected", func(t *testing.T) {
		if _, err := cb(fakeConn{user: "bob"}, cert); err == nil {
			t.Fatal("want reject for principal mismatch")
		}
	})
	t.Run("cert from other CA rejected", func(t *testing.T) {
		if _, err := cb(fakeConn{user: "alice"}, cert); err != nil {
			t.Fatal(err) // sanity
		}
		cbOther := UserAuthCallback(other.PublicKey())
		if _, err := cbOther(fakeConn{user: "alice"}, cert); err == nil {
			t.Fatal("want reject for untrusted CA")
		}
	})
	t.Run("bare key (no cert) rejected", func(t *testing.T) {
		if _, err := cb(fakeConn{user: "alice"}, clientKey.PublicKey()); err == nil {
			t.Fatal("want reject for non-certificate key")
		}
	})
	t.Run("expired cert rejected", func(t *testing.T) {
		expired, err := authority.SignUserCert(clientKey.PublicKey(), "alice", "", -time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cb(fakeConn{user: "alice"}, expired); err == nil {
			t.Fatal("want reject for expired cert")
		}
	})
}

func TestHostAuth(t *testing.T) {
	authority := newCA(t)
	other := newCA(t)
	hostKey, _ := sshkeys.NewEd25519Signer()

	cert, err := authority.SignHostCert(hostKey.PublicKey(), []string{"node_123", "10.0.0.1:7600"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("valid host cert for node id", func(t *testing.T) {
		cb := HostKeyCallback(authority.PublicKey(), "node_123")
		if err := cb("ignored:7600", &net.IPAddr{}, cert); err != nil {
			t.Fatalf("want accept, got %v", err)
		}
	})
	t.Run("wrong node id rejected", func(t *testing.T) {
		cb := HostKeyCallback(authority.PublicKey(), "node_999")
		if err := cb("ignored:7600", &net.IPAddr{}, cert); err == nil {
			t.Fatal("want reject for principal mismatch")
		}
	})
	t.Run("untrusted CA rejected", func(t *testing.T) {
		cb := HostKeyCallback(other.PublicKey(), "node_123")
		if err := cb("ignored:7600", &net.IPAddr{}, cert); err == nil {
			t.Fatal("want reject for untrusted CA")
		}
	})
	t.Run("bare host key rejected", func(t *testing.T) {
		cb := HostKeyCallback(authority.PublicKey(), "node_123")
		if err := cb("ignored:7600", &net.IPAddr{}, hostKey.PublicKey()); err == nil {
			t.Fatal("want reject for non-certificate host key")
		}
	})
}
