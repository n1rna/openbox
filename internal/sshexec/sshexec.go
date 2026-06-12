// Package sshexec runs a command on a node over a verified SSH connection. It is the
// shared core used by both the CLI (after an HTTP dispatch) and the control plane's
// web-console exec proxy (after a local dispatch) — so the wire behavior is identical
// no matter who initiates the call.
package sshexec

import (
	"context"
	"fmt"
	"io"

	"openbox.io/openbox/internal/certauth"
	"openbox.io/openbox/internal/transport"

	"golang.org/x/crypto/ssh"
)

// Target identifies and authenticates a node connection.
type Target struct {
	NodeID    string        // expected node identity (verified against the host cert)
	Addr      string        // transport address to dial
	Principal string        // username the user cert is issued for
	CAPub     ssh.PublicKey // CA the host cert must chain to
}

// Run dials the target over tr, presenting the user cert (over signer), runs cmdline
// under the requested isolation, streams stdout/stderr to the given writers, and
// returns the remote exit code.
func Run(ctx context.Context, tr transport.Transport, signer ssh.Signer, userCert *ssh.Certificate, tgt Target, iso, cmdline string, stdout, stderr io.Writer) (int, error) {
	certSigner, err := ssh.NewCertSigner(userCert, signer)
	if err != nil {
		return -1, fmt.Errorf("build user cert signer: %w", err)
	}
	cfg := &ssh.ClientConfig{
		User:            tgt.Principal,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(certSigner)},
		HostKeyCallback: certauth.HostKeyCallback(tgt.CAPub, tgt.NodeID),
	}

	nConn, err := tr.Dial(ctx, tgt.Addr)
	if err != nil {
		return -1, fmt.Errorf("dial: %w", err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(nConn, tgt.Addr, cfg)
	if err != nil {
		return -1, fmt.Errorf("ssh handshake: %w", err)
	}
	cli := ssh.NewClient(sshConn, chans, reqs)
	defer cli.Close()

	sess, err := cli.NewSession()
	if err != nil {
		return -1, fmt.Errorf("new session: %w", err)
	}
	defer sess.Close()
	sess.Stdout = stdout
	sess.Stderr = stderr

	if iso != "" {
		if err := sess.Setenv("OPENBOX_ISOLATION", iso); err != nil {
			return -1, fmt.Errorf("select isolation: %w", err)
		}
	}
	if err := sess.Run(cmdline); err != nil {
		if ee, ok := err.(*ssh.ExitError); ok {
			return ee.ExitStatus(), nil
		}
		return -1, err
	}
	return 0, nil
}
