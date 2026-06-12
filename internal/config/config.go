// Package config manages on-disk state for the openbox CLI (client) and the node
// daemon (agent), both rooted at $OPENBOX_HOME (default ~/.openbox).
package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// Home returns the openbox state directory, honoring $OPENBOX_HOME.
func Home() string {
	if h := os.Getenv("OPENBOX_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".openbox"
	}
	return filepath.Join(home, ".openbox")
}

// ClientConfig is the CLI's login state. The private key binds dispatch-issued
// user certs to this client.
type ClientConfig struct {
	Server string `json:"server"`
	Token  string `json:"token"`
	KeyPEM string `json:"key_pem"`
	User   string `json:"user"`
	dirty  bool
}

func clientPath() string { return filepath.Join(Home(), "config.json") }

// LoadClient reads the client config, returning a zero config if none exists.
func LoadClient() (*ClientConfig, error) {
	data, err := os.ReadFile(clientPath())
	if os.IsNotExist(err) {
		return &ClientConfig{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c ClientConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse client config: %w", err)
	}
	return &c, nil
}

// Save persists the client config with 0600 perms.
func (c *ClientConfig) Save() error { return writeJSON(clientPath(), c) }

// LoggedIn reports whether the client has a server and token.
func (c *ClientConfig) LoggedIn() bool { return c.Server != "" && c.Token != "" }

// Signer returns the client's ssh.Signer, generating and persisting a key on first
// use.
func (c *ClientConfig) Signer() (ssh.Signer, error) {
	if c.KeyPEM == "" {
		pemStr, err := newKeyPEM("openbox-client")
		if err != nil {
			return nil, err
		}
		c.KeyPEM = pemStr
		c.dirty = true
	}
	return ssh.ParsePrivateKey([]byte(c.KeyPEM))
}

// Dirty reports whether Signer generated a new key that must be saved.
func (c *ClientConfig) Dirty() bool { return c.dirty }

// AgentConfig is the node daemon's persisted identity.
type AgentConfig struct {
	Server   string `json:"server"`
	NodeID   string `json:"node_id"`
	CAPubKey string `json:"ca_pubkey"` // authorized_keys form
	HostCert string `json:"host_cert"` // signed host cert, authorized_keys form
	KeyPEM   string `json:"host_key_pem"`
}

func agentDir() string  { return filepath.Join(Home(), "agent") }
func agentPath() string { return filepath.Join(agentDir(), "agent.json") }

// LoadAgent reads the agent config, returning a zero config if none exists.
func LoadAgent() (*AgentConfig, error) {
	data, err := os.ReadFile(agentPath())
	if os.IsNotExist(err) {
		return &AgentConfig{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c AgentConfig
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse agent config: %w", err)
	}
	return &c, nil
}

// Save persists the agent config.
func (c *AgentConfig) Save() error { return writeJSON(agentPath(), c) }

// Registered reports whether the agent has completed registration.
func (c *AgentConfig) Registered() bool { return c.NodeID != "" && c.CAPubKey != "" }

// HostSigner returns the node host key, generating and persisting one on first use.
func (c *AgentConfig) HostSigner() (ssh.Signer, error) {
	if c.KeyPEM == "" {
		pemStr, err := newKeyPEM("openbox-host")
		if err != nil {
			return nil, err
		}
		c.KeyPEM = pemStr
	}
	return ssh.ParsePrivateKey([]byte(c.KeyPEM))
}

func newKeyPEM(comment string) (string, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(block)), nil
}

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
