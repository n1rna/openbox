// Package control implements the openbox control plane: a thin HTTP service that
// owns the node registry, the SSH CA, enrollment, and the session directory. It is
// deliberately NOT in the data path — it brokers identity and trust, then steps
// aside while the CLI talks peer-to-peer to the node.
package control

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"openbox.io/openbox/internal/api"
	"openbox.io/openbox/internal/ca"
	"openbox.io/openbox/internal/isolation"
	"openbox.io/openbox/internal/sshexec"
	"openbox.io/openbox/internal/sshkeys"
	"openbox.io/openbox/internal/store"
	"openbox.io/openbox/internal/transport"

	"golang.org/x/crypto/ssh"
)

// OnlineWindow is how recently a node must have heartbeated to count as online.
const OnlineWindow = 90 * time.Second

// UserCertTTL bounds the lifetime of issued user certs — short, because one is
// minted per dispatch.
const UserCertTTL = 5 * time.Minute

// Server is the control-plane HTTP handler.
type Server struct {
	st        *store.Store
	ca        *ca.CA
	publicURL string
	tr        transport.Transport // used by the web console to reach nodes
}

// New builds a control-plane Server. publicURL is the externally reachable base URL,
// echoed back in enrollment tokens; tr is the transport the web console uses to reach
// nodes (it must be able to dial nodes' advertised addresses).
func New(st *store.Store, ca *ca.CA, publicURL string, tr transport.Transport) *Server {
	return &Server{st: st, ca: ca, publicURL: publicURL, tr: tr}
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Node-facing (enroll-token / node-id authenticated).
	mux.HandleFunc("POST /v1/nodes/register", s.handleRegister)
	mux.HandleFunc("POST /v1/nodes/heartbeat", s.handleHeartbeat)
	// User-facing (bearer-token authenticated).
	mux.HandleFunc("GET /v1/whoami", s.auth(s.handleWhoami))
	mux.HandleFunc("GET /v1/nodes", s.auth(s.handleListNodes))
	mux.HandleFunc("GET /v1/nodes/{id}", s.auth(s.handleGetNode))
	mux.HandleFunc("PATCH /v1/nodes/{id}", s.auth(s.handleUpdateNode))
	mux.HandleFunc("DELETE /v1/nodes/{id}", s.auth(s.handleDeleteNode))
	mux.HandleFunc("GET /v1/sessions", s.auth(s.handleListSessions))
	mux.HandleFunc("DELETE /v1/sessions/{id}", s.auth(s.handleDeleteSession))
	mux.HandleFunc("POST /v1/dispatch", s.auth(s.handleDispatch))
	mux.HandleFunc("POST /v1/exec", s.auth(s.handleExec))
	mux.HandleFunc("POST /v1/enroll-tokens", s.auth(s.handleEnrollToken))
	// Web dashboard (single-page app; authenticates client-side with a token).
	mux.HandleFunc("GET /{$}", s.handleDashboard)
	return mux
}

//go:embed web/index.html
var dashboardHTML []byte

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(dashboardHTML)
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	sessions, err := s.st.ListSessions(r.Context(), u.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	nodes, _ := s.st.ListNodes(r.Context(), u.ID, "")
	names := make(map[string]string, len(nodes))
	for _, n := range nodes {
		names[n.ID] = n.Name
	}
	resp := api.ListSessionsResponse{Sessions: make([]api.SessionView, 0, len(sessions))}
	for _, sess := range sessions {
		resp.Sessions = append(resp.Sessions, api.SessionView{
			SessionID: sess.SessionID, NodeID: sess.NodeID, NodeName: names[sess.NodeID],
			CreatedAt: sess.CreatedAt.Unix(), LastUsed: sess.LastUsed.Unix(),
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// --- middleware ---

type ctxKey string

const userKey ctxKey = "user"

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if tok == "" {
			writeErr(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		u, err := s.st.UserByToken(r.Context(), tok)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "invalid token")
			return
		}
		ctx := context.WithValue(r.Context(), userKey, u)
		next(w, r.WithContext(ctx))
	}
}

func userOf(r *http.Request) *store.User { return r.Context().Value(userKey).(*store.User) }

// --- node-facing handlers ---

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req api.RegisterRequest
	if !decode(w, r, &req) {
		return
	}
	et, err := s.st.ConsumeEnrollToken(r.Context(), req.EnrollToken)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "enrollment: "+err.Error())
		return
	}
	hostPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(req.HostPubKey))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad host_pubkey")
		return
	}

	nodeID := "node_" + randHex(8)
	tags := mergeTags(et.Tags, req.Tags)
	n := &store.Node{
		ID: nodeID, Name: nonEmpty(req.Name, nodeID), Owner: et.Owner,
		HostPubKey: strings.TrimSpace(req.HostPubKey), Addr: req.Addr,
		OS: req.OS, Arch: req.Arch, Tags: tags,
	}
	if err := s.st.UpsertNode(r.Context(), n); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// A host cert lets clients verify this node. Principals: node id + address.
	principals := []string{nodeID, req.Addr}
	hostCert, err := s.ca.SignHostCert(hostPub, principals, 365*24*time.Hour)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	log.Printf("registered node %s (%s) owner=%s tags=%v", nodeID, n.Name, et.Owner, tags)
	writeJSON(w, http.StatusOK, api.RegisterResponse{
		NodeID:   nodeID,
		HostCert: string(ssh.MarshalAuthorizedKey(hostCert)),
		CAPubKey: string(s.ca.AuthorizedKey()),
	})
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req api.HeartbeatRequest
	if !decode(w, r, &req) {
		return
	}
	// TODO(security): authenticate heartbeats with a per-node token issued at
	// registration. Phase 1 trusts the node id.
	if err := s.st.Touch(r.Context(), req.NodeID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- user-facing handlers ---

func (s *Server) handleWhoami(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	writeJSON(w, http.StatusOK, api.WhoamiResponse{UserID: u.ID, Name: u.Name})
}

func (s *Server) handleListNodes(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	nodes, err := s.st.ListNodes(r.Context(), u.ID, r.URL.Query().Get("tag"))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := api.ListNodesResponse{Nodes: make([]api.NodeView, 0, len(nodes))}
	for _, n := range nodes {
		resp.Nodes = append(resp.Nodes, toView(n))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetNode(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	n, err := s.st.NodeByID(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	writeJSON(w, http.StatusOK, toView(n))
}

func (s *Server) handleUpdateNode(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var req api.UpdateNodeRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := s.st.SetNodeMeta(r.Context(), u.ID, r.PathValue("id"), req.Name, mergeTags(req.Tags, nil)); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	n, err := s.st.NodeByID(r.Context(), u.ID, r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "node not found")
		return
	}
	writeJSON(w, http.StatusOK, toView(n))
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if err := s.st.DeleteNode(r.Context(), u.ID, r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if err := s.st.DeleteSession(r.Context(), u.ID, r.PathValue("id")); err != nil {
		writeErr(w, statusFor(err), err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func statusFor(err error) int {
	if err == store.ErrNotFound {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

func (s *Server) handleDispatch(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var req api.DispatchRequest
	if !decode(w, r, &req) {
		return
	}
	clientPub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(req.ClientPubKey))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad client_pubkey")
		return
	}

	node, created, err := s.resolveTarget(r.Context(), u, req)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}

	cert, err := s.ca.SignUserCert(clientPub, u.Name, req.SessionID, UserCertTTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api.DispatchResponse{
		NodeID:         node.ID,
		NodeName:       node.Name,
		Addr:           node.Addr,
		HostPubKey:     node.HostPubKey,
		CAPubKey:       string(s.ca.AuthorizedKey()),
		UserCert:       string(ssh.MarshalAuthorizedKey(cert)),
		Principal:      u.Name,
		SessionCreated: created,
	})
}

// resolveTarget picks the node for a dispatch: an explicit node id, or the first
// online node matching the tag. If a session id is given, the binding is honored
// (and created on first use), so a session always lands on the same node.
func (s *Server) resolveTarget(ctx context.Context, u *store.User, req api.DispatchRequest) (*store.Node, bool, error) {
	if req.SessionID != "" {
		// If already bound, go straight to the bound node.
		boundID, err := s.st.SessionNode(ctx, u.ID, req.SessionID)
		if err == nil {
			n, err := s.st.NodeByID(ctx, u.ID, boundID)
			if err != nil {
				return nil, false, fmt.Errorf("session %q bound to missing node", req.SessionID)
			}
			return n, false, nil
		}
		if err != store.ErrNotFound {
			return nil, false, err
		}
	}

	candidate, err := s.pickNode(ctx, u, req)
	if err != nil {
		return nil, false, err
	}

	if req.SessionID != "" {
		boundID, created, err := s.st.BindSession(ctx, u.ID, req.SessionID, candidate.ID)
		if err != nil {
			return nil, false, err
		}
		if boundID != candidate.ID { // raced with another dispatch; honor the winner
			candidate, err = s.st.NodeByID(ctx, u.ID, boundID)
			if err != nil {
				return nil, false, err
			}
		}
		return candidate, created, nil
	}
	return candidate, false, nil
}

func (s *Server) pickNode(ctx context.Context, u *store.User, req api.DispatchRequest) (*store.Node, error) {
	if req.NodeID != "" {
		return s.st.NodeByID(ctx, u.ID, req.NodeID)
	}
	nodes, err := s.st.ListNodes(ctx, u.ID, req.Tag)
	if err != nil {
		return nil, err
	}
	// Prefer online nodes; fall back to any match so the error is actionable.
	var fallback *store.Node
	for _, n := range nodes {
		if fallback == nil {
			fallback = n
		}
		if time.Since(n.LastSeen) <= OnlineWindow {
			return n, nil
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	if req.Tag != "" {
		return nil, fmt.Errorf("no node matches tag %q", req.Tag)
	}
	return nil, fmt.Errorf("no nodes available")
}

// handleExec runs a command on a node on the user's behalf (the web console) and
// streams stdout/stderr/exit back as Server-Sent Events. The control plane mints an
// ephemeral user cert for itself acting as the user, so the node's auth is unchanged.
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var req api.ExecRequest
	if !decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Command) == "" {
		writeErr(w, http.StatusBadRequest, "command is required")
		return
	}
	if _, err := isolation.Parse(req.Isolation); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.tr == nil {
		writeErr(w, http.StatusServiceUnavailable, "web console not available: control plane has no node transport")
		return
	}

	node, _, err := s.resolveTarget(r.Context(), u, api.DispatchRequest{
		NodeID: req.NodeID, Tag: req.Tag, SessionID: req.SessionID,
	})
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}

	signer, err := sshkeys.NewEd25519Signer()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	cert, err := s.ca.SignUserCert(signer.PublicKey(), u.Name, req.SessionID, UserCertTTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	log.Printf("web console exec by %s on %s: %s", u.Name, node.ID, req.Command)
	tgt := sshexec.Target{NodeID: node.ID, Addr: node.Addr, Principal: u.Name, CAPub: s.ca.PublicKey()}
	code, err := sshexec.Run(r.Context(), s.tr, signer, cert, tgt,
		req.Isolation, req.Command,
		&sseWriter{w: w, f: flusher, stream: "stdout"},
		&sseWriter{w: w, f: flusher, stream: "stderr"})
	if err != nil {
		writeSSE(w, flusher, map[string]any{"stream": "error", "data": err.Error()})
		return
	}
	writeSSE(w, flusher, map[string]any{"stream": "exit", "code": code})
}

// sseWriter turns writes into Server-Sent Events tagged with a stream label.
type sseWriter struct {
	w      io.Writer
	f      http.Flusher
	stream string
}

func (s *sseWriter) Write(p []byte) (int, error) {
	if err := writeSSE(s.w, s.f, map[string]any{"stream": s.stream, "data": string(p)}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func writeSSE(w io.Writer, f http.Flusher, obj any) error {
	b, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
		return err
	}
	f.Flush()
	return nil
}

func (s *Server) handleEnrollToken(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var req api.EnrollTokenRequest
	if !decode(w, r, &req) {
		return
	}
	ttl := 15 * time.Minute
	if req.TTLSeconds > 0 {
		ttl = time.Duration(req.TTLSeconds) * time.Second
	}
	token := "enroll_" + randHex(16)
	if _, err := s.st.CreateEnrollToken(r.Context(), token, u.ID, req.Tags, ttl); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, api.EnrollTokenResponse{Token: token, Server: s.publicURL})
}

// EnsureBootstrapUser creates a default user if the database has none, returning the
// freshly minted token so the operator can log in. Returns ("", nil) if users exist.
func EnsureBootstrapUser(ctx context.Context, st *store.Store, name string) (string, error) {
	n, err := st.CountUsers(ctx)
	if err != nil {
		return "", err
	}
	if n > 0 {
		return "", nil
	}
	token := "obx_" + randHex(20)
	if _, err := st.CreateUser(ctx, "usr_"+randHex(6), name, token); err != nil {
		return "", err
	}
	return token, nil
}

// --- helpers ---

func toView(n *store.Node) api.NodeView {
	return api.NodeView{
		ID: n.ID, Name: n.Name, Addr: n.Addr, OS: n.OS, Arch: n.Arch,
		Tags: n.Tags, LastSeen: n.LastSeen.Unix(), CreatedAt: n.CreatedAt.Unix(),
		Online: time.Since(n.LastSeen) <= OnlineWindow,
	}
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "bad json: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, api.Error{Error: msg})
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func mergeTags(a, b []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range append(append([]string{}, a...), b...) {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}
