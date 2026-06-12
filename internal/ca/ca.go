// Package ca implements the openbox SSH certificate authority. It is the root of
// trust for the whole system:
//
//   - Nodes get a HOST certificate at registration, so clients can verify they are
//     talking to the real node (not a man-in-the-middle on the overlay).
//   - Users get a short-lived USER certificate each time they run a command, so
//     nodes can verify the caller is an authenticated openbox user without the node
//     ever holding per-user secrets.
//
// This is the Teleport / Netflix-BLESS model: one CA, ephemeral certs, no static
// key distribution. The CA private key lives only on the control plane.
package ca

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"
)

// CA is a certificate authority backed by an ed25519 key.
type CA struct {
	signer ssh.Signer
	now    func() time.Time
}

// New wraps an existing ssh.Signer as a CA.
func New(signer ssh.Signer) *CA {
	return &CA{signer: signer, now: time.Now}
}

// PublicKey returns the CA's public key. Nodes and clients pin trust to this:
// a cert is trusted iff it is signed by this key.
func (c *CA) PublicKey() ssh.PublicKey { return c.signer.PublicKey() }

// AuthorizedKey returns the CA public key in authorized_keys / known_hosts form.
func (c *CA) AuthorizedKey() []byte {
	return ssh.MarshalAuthorizedKey(c.signer.PublicKey())
}

// SessionExtension is the cert extension that carries the bound session id from the
// control plane to the node. Unknown extensions are ignored by SSH, so this is safe.
const SessionExtension = "openbox-session@openbox.io"

// SignUserCert issues a short-lived user certificate binding pub to the given
// principal (the openbox username). If sessionID is non-empty it is embedded as an
// extension, cryptographically authorizing the node to route the command into that
// persistent session. validFor bounds the certificate lifetime.
func (c *CA) SignUserCert(pub ssh.PublicKey, principal, sessionID string, validFor time.Duration) (*ssh.Certificate, error) {
	now := c.now()
	exts := map[string]string{
		// Minimal capabilities; openbox does its own exec authorization.
		"permit-pty": "",
	}
	if sessionID != "" {
		exts[SessionExtension] = sessionID
	}
	cert := &ssh.Certificate{
		Key:             pub,
		Serial:          uint64(now.UnixNano()),
		CertType:        ssh.UserCert,
		KeyId:           principal,
		ValidPrincipals: []string{principal},
		ValidAfter:      uint64(now.Add(-1 * time.Minute).Unix()),
		ValidBefore:     uint64(now.Add(validFor).Unix()),
		Permissions:     ssh.Permissions{Extensions: exts},
	}
	if err := cert.SignCert(rand.Reader, c.signer); err != nil {
		return nil, fmt.Errorf("sign user cert: %w", err)
	}
	return cert, nil
}

// SignHostCert issues a host certificate for a node, binding its host key to the
// set of principals (node id and reachable addresses) clients may verify against.
func (c *CA) SignHostCert(hostPub ssh.PublicKey, principals []string, validFor time.Duration) (*ssh.Certificate, error) {
	now := c.now()
	cert := &ssh.Certificate{
		Key:             hostPub,
		Serial:          uint64(now.UnixNano()),
		CertType:        ssh.HostCert,
		KeyId:           principals[0],
		ValidPrincipals: principals,
		ValidAfter:      uint64(now.Add(-1 * time.Minute).Unix()),
		ValidBefore:     uint64(now.Add(validFor).Unix()),
	}
	if err := cert.SignCert(rand.Reader, c.signer); err != nil {
		return nil, fmt.Errorf("sign host cert: %w", err)
	}
	return cert, nil
}

// LoadOrCreate loads a CA key from path, generating and persisting a new one if it
// does not exist. The key is stored as a PEM-wrapped OpenSSH private key.
func LoadOrCreate(path string) (*CA, error) {
	if data, err := os.ReadFile(path); err == nil {
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("parse CA key %s: %w", path, err)
		}
		return New(signer), nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read CA key %s: %w", path, err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}
	pemBlock, err := ssh.MarshalPrivateKey(priv, "openbox-ca")
	if err != nil {
		return nil, fmt.Errorf("marshal CA key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(pemBlock), 0o600); err != nil {
		return nil, fmt.Errorf("write CA key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(pem.EncodeToMemory(pemBlock))
	if err != nil {
		return nil, err
	}
	return New(signer), nil
}
