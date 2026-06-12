// Package client implements the `openbox run` path: ask the control plane to resolve
// a target node and mint a user cert, then connect peer-to-peer to that node over the
// transport and execute the command with mutual certificate verification.
package client

import (
	"context"
	"fmt"
	"io"

	"openbox.io/openbox/internal/api"
	"openbox.io/openbox/internal/certauth"
	"openbox.io/openbox/internal/config"
	"openbox.io/openbox/internal/cpclient"
	"openbox.io/openbox/internal/isolation"
	"openbox.io/openbox/internal/sshexec"
	"openbox.io/openbox/internal/transport"

	"golang.org/x/crypto/ssh"
)

// Target selects which node to run on and how to execute.
type Target struct {
	Tag       string
	NodeID    string
	SessionID string
	Isolation string // isolation spec, e.g. "docker:ubuntu:22.04" (empty = native)
}

// Run resolves target via the control plane and executes cmdline on the chosen node,
// streaming output to stdout/stderr and returning the remote exit code.
func Run(ctx context.Context, cfg *config.ClientConfig, tr transport.Transport, target Target, cmdline string, stdout, stderr io.Writer) (int, error) {
	if !cfg.LoggedIn() {
		return -1, fmt.Errorf("not logged in — run `openbox login --server <url> --token <token>`")
	}
	// Fail fast on a bad isolation spec rather than discovering it on the node.
	if _, err := isolation.Parse(target.Isolation); err != nil {
		return -1, err
	}
	signer, err := cfg.Signer()
	if err != nil {
		return -1, err
	}

	cp := cpclient.New(cfg.Server, cfg.Token)
	disp, err := cp.Dispatch(ctx, api.DispatchRequest{
		Tag:          target.Tag,
		NodeID:       target.NodeID,
		SessionID:    target.SessionID,
		ClientPubKey: string(ssh.MarshalAuthorizedKey(signer.PublicKey())),
	})
	if err != nil {
		return -1, err
	}

	caPub, err := certauth.ParseAuthorizedKey(disp.CAPubKey)
	if err != nil {
		return -1, fmt.Errorf("parse CA pubkey: %w", err)
	}
	userCertPub, err := certauth.ParseAuthorizedKey(disp.UserCert)
	if err != nil {
		return -1, fmt.Errorf("parse user cert: %w", err)
	}
	userCert, ok := userCertPub.(*ssh.Certificate)
	if !ok {
		return -1, fmt.Errorf("dispatch returned a non-certificate")
	}

	tgt := sshexec.Target{NodeID: disp.NodeID, Addr: disp.Addr, Principal: disp.Principal, CAPub: caPub}
	code, err := sshexec.Run(ctx, tr, signer, userCert, tgt, target.Isolation, cmdline, stdout, stderr)
	if err != nil {
		return -1, fmt.Errorf("node %s (%s): %w", disp.NodeName, disp.Addr, err)
	}
	return code, nil
}
