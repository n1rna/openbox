package main

import (
	"fmt"
	"os"
	"path/filepath"

	"openbox.io/openbox/internal/config"
	"openbox.io/openbox/internal/transport"
)

// meshFlags holds the mesh-related flags shared by `agent` and the run path.
type meshFlags struct {
	enabled  bool
	control  string
	authKey  string
	hostname string
	verbose  bool
}

// resolve fills empty fields from environment so mesh settings can come from either
// flags or the environment (handy for agents started by the bootstrap installer).
func (m *meshFlags) resolve() {
	if !m.enabled && os.Getenv("OPENBOX_MESH") != "" {
		m.enabled = true
	}
	if m.control == "" {
		m.control = os.Getenv("OPENBOX_MESH_CONTROL")
	}
	if m.authKey == "" {
		m.authKey = os.Getenv("OPENBOX_MESH_AUTHKEY")
	}
	if m.hostname == "" {
		m.hostname = os.Getenv("OPENBOX_MESH_HOSTNAME")
	}
}

// buildTransport returns the transport for the given role ("agent" or "client").
// Without --mesh it is plain TCP; with --mesh it is an embedded Tailscale node whose
// persistent state lives under the openbox home, namespaced by role.
func buildTransport(role string, m meshFlags) (transport.Transport, error) {
	m.resolve()
	if !m.enabled {
		return transport.NewTCP(), nil
	}
	host := m.hostname
	if host == "" {
		h, _ := os.Hostname()
		if h == "" {
			h = role
		}
		if role == "client" {
			h += "-cli"
		}
		host = h
	}
	dir := filepath.Join(config.Home(), role, "ts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("mesh state dir: %w", err)
	}
	return transport.NewTSNet(transport.TSConfig{
		Hostname:   host,
		AuthKey:    m.authKey,
		ControlURL: m.control,
		Dir:        dir,
		Verbose:    m.verbose,
	}), nil
}
