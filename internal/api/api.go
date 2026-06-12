// Package api defines the wire types shared between the openbox CLI/agent and the
// control plane. Keeping them in one place keeps client and server in lockstep.
package api

// RegisterRequest is sent by a node to join a user's network. The enroll token
// authenticates and authorizes the registration and carries preset tags.
type RegisterRequest struct {
	EnrollToken string   `json:"enroll_token"`
	Name        string   `json:"name"`
	HostPubKey  string   `json:"host_pubkey"` // authorized_keys form
	Addr        string   `json:"addr"`
	OS          string   `json:"os"`
	Arch        string   `json:"arch"`
	Tags        []string `json:"tags,omitempty"`
}

// RegisterResponse returns the node's identity plus the trust material it needs:
// a host certificate (so clients can verify it) and the CA public key (so it can
// verify user certs).
type RegisterResponse struct {
	NodeID   string `json:"node_id"`
	HostCert string `json:"host_cert"` // signed host certificate, authorized_keys form
	CAPubKey string `json:"ca_pubkey"` // authorized_keys form
}

// HeartbeatRequest keeps a node marked healthy in the registry.
type HeartbeatRequest struct {
	NodeID string `json:"node_id"`
}

// DispatchRequest is the pre-flight call the CLI makes before connecting to a node.
// The control plane resolves the target (by id, or by tag honoring any session
// binding), issues a short-lived user cert bound to ClientPubKey, and returns
// everything needed to open a verified peer-to-peer connection.
type DispatchRequest struct {
	Tag          string `json:"tag,omitempty"`
	NodeID       string `json:"node_id,omitempty"`
	SessionID    string `json:"session_id,omitempty"`
	ClientPubKey string `json:"client_pubkey"` // authorized_keys form
}

// DispatchResponse carries the resolved node and the trust material for the call.
type DispatchResponse struct {
	NodeID         string `json:"node_id"`
	NodeName       string `json:"node_name"`
	Addr           string `json:"addr"`
	HostPubKey     string `json:"host_pubkey"`     // node host key, authorized_keys form
	CAPubKey       string `json:"ca_pubkey"`       // for host-cert verification
	UserCert       string `json:"user_cert"`       // signed user cert, authorized_keys form
	Principal      string `json:"principal"`       // username baked into the cert
	SessionCreated bool   `json:"session_created"` // true if this call created the session binding
}

// EnrollTokenRequest asks the control plane to mint a node enrollment token.
type EnrollTokenRequest struct {
	Tags       []string `json:"tags,omitempty"`
	TTLSeconds int      `json:"ttl_seconds,omitempty"`
}

// EnrollTokenResponse returns the minted token and the server URL to hand to a node.
type EnrollTokenResponse struct {
	Token  string `json:"token"`
	Server string `json:"server"`
}

// NodeView is the registry representation returned to clients.
type NodeView struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Addr      string   `json:"addr"`
	OS        string   `json:"os"`
	Arch      string   `json:"arch"`
	Tags      []string `json:"tags"`
	LastSeen  int64    `json:"last_seen"`
	CreatedAt int64    `json:"created_at"`
	Online    bool     `json:"online"`
}

// UpdateNodeRequest sets a node's display name and tags (full desired state).
type UpdateNodeRequest struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

// ExecRequest asks the control plane to run a command on a node on the user's behalf
// (the web console). Output streams back as Server-Sent Events.
type ExecRequest struct {
	NodeID    string `json:"node_id,omitempty"`
	Tag       string `json:"tag,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Isolation string `json:"isolation,omitempty"`
	Command   string `json:"command"`
}

// ListNodesResponse is the body of GET /v1/nodes.
type ListNodesResponse struct {
	Nodes []NodeView `json:"nodes"`
}

// SessionView is a session binding returned to clients.
type SessionView struct {
	SessionID string `json:"session_id"`
	NodeID    string `json:"node_id"`
	NodeName  string `json:"node_name"`
	CreatedAt int64  `json:"created_at"`
	LastUsed  int64  `json:"last_used"`
}

// ListSessionsResponse is the body of GET /v1/sessions.
type ListSessionsResponse struct {
	Sessions []SessionView `json:"sessions"`
}

// WhoamiResponse identifies the authenticated user.
type WhoamiResponse struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

// Error is the standard error envelope.
type Error struct {
	Error string `json:"error"`
}
