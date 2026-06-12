// Package certauth holds the SSH certificate verification logic shared by the agent
// (verifying user certs) and the client (verifying host certs). Both sides trust the
// same openbox CA public key; everything else is ephemeral.
package certauth

import (
	"bytes"
	"fmt"
	"net"

	"golang.org/x/crypto/ssh"
)

// signedBy reports whether cert was signed by the given CA public key.
func signedBy(cert *ssh.Certificate, caPub ssh.PublicKey) bool {
	return cert.SignatureKey != nil && bytes.Equal(cert.SignatureKey.Marshal(), caPub.Marshal())
}

// UserAuthCallback returns a PublicKeyCallback for the agent's ssh.ServerConfig. It
// accepts a connection iff the client presents a user certificate signed by caPub
// and valid for the connecting username (the cert principal).
func UserAuthCallback(caPub ssh.PublicKey) func(ssh.ConnMetadata, ssh.PublicKey) (*ssh.Permissions, error) {
	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return bytes.Equal(auth.Marshal(), caPub.Marshal())
		},
	}
	return func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
		cert, ok := key.(*ssh.Certificate)
		if !ok {
			return nil, fmt.Errorf("client did not present a certificate")
		}
		if !signedBy(cert, caPub) {
			return nil, fmt.Errorf("user cert not signed by openbox CA")
		}
		return checker.Authenticate(conn, key)
	}
}

// HostKeyCallback returns a HostKeyCallback for the client. It verifies the node
// presents a host certificate signed by caPub and valid for the expected nodeID
// (matched as a principal — independent of the dial address, which avoids host:port
// matching pitfalls on the overlay).
func HostKeyCallback(caPub ssh.PublicKey, nodeID string) ssh.HostKeyCallback {
	checker := &ssh.CertChecker{
		IsHostAuthority: func(auth ssh.PublicKey, _ string) bool {
			return bytes.Equal(auth.Marshal(), caPub.Marshal())
		},
	}
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		cert, ok := key.(*ssh.Certificate)
		if !ok {
			return fmt.Errorf("node did not present a host certificate")
		}
		if cert.CertType != ssh.HostCert {
			return fmt.Errorf("node presented a non-host certificate")
		}
		if !signedBy(cert, caPub) {
			return fmt.Errorf("host cert not signed by openbox CA")
		}
		// CheckCert validates the cert window and that nodeID is a valid principal,
		// matching on node identity rather than the dial address.
		return checker.CheckCert(nodeID, cert)
	}
}

// ParseAuthorizedKey parses a single authorized_keys line into a public key.
func ParseAuthorizedKey(s string) (ssh.PublicKey, error) {
	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(s))
	return pk, err
}
