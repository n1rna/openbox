// Package sshkeys provides SSH key material helpers shared by the agent (host key)
// and client (auth key). Phase 1 extends this with CA signing and cert issuance.
package sshkeys

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"

	"golang.org/x/crypto/ssh"
)

// NewEd25519Signer generates a fresh ed25519 key and returns it as an ssh.Signer.
func NewEd25519Signer() (ssh.Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, fmt.Errorf("signer from key: %w", err)
	}
	return signer, nil
}
