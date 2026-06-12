// Package agent implements the openbox node daemon: it registers with the control
// plane, then accepts cert-authenticated connections over the transport and executes
// commands on the node.
//
// Phase 1 scope: registration, mutual SSH-certificate auth (only user certs signed
// by the openbox CA are accepted), heartbeats, and one-shot command exec. Still to
// come: persistent sessions keyed by session-id (Phase 2) and isolation tiers
// (Phase 4) — both slot into runCommand without touching the transport or auth.
package agent

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"runtime"
	"time"

	"openbox.io/openbox/internal/api"
	"openbox.io/openbox/internal/ca"
	"openbox.io/openbox/internal/certauth"
	"openbox.io/openbox/internal/config"
	"openbox.io/openbox/internal/cpclient"
	"openbox.io/openbox/internal/isolation"
	"openbox.io/openbox/internal/session"
	"openbox.io/openbox/internal/transport"

	"golang.org/x/crypto/ssh"
)

// isolationEnv is the channel env var the client uses to request an isolation mode.
const isolationEnv = "OPENBOX_ISOLATION"

// HeartbeatInterval is how often the agent reports liveness to the control plane.
const HeartbeatInterval = 30 * time.Second

// Options configures a daemon run.
type Options struct {
	Transport   transport.Transport
	Addr        string   // address to listen on AND advertise to the control plane
	Server      string   // control-plane base URL (required on first registration)
	EnrollToken string   // used only on first registration
	Name        string   // node display name
	Tags        []string // extra tags to request at registration
}

// Run loads (or completes) the node's registration and serves until ctx is done.
func Run(ctx context.Context, opts Options) error {
	cfg, err := config.LoadAgent()
	if err != nil {
		return err
	}

	hostSigner, err := cfg.HostSigner()
	if err != nil {
		return err
	}

	if !cfg.Registered() {
		if opts.EnrollToken == "" {
			return fmt.Errorf("node is not registered: provide an enrollment token (--token or $OPENBOX_ENROLL_TOKEN)")
		}
		if opts.Server == "" {
			return fmt.Errorf("node is not registered: provide the control-plane URL (--server or $OPENBOX_SERVER)")
		}
		if err := register(ctx, cfg, hostSigner, opts); err != nil {
			return fmt.Errorf("registration: %w", err)
		}
		log.Printf("registered as %s with control plane %s", cfg.NodeID, cfg.Server)
	}

	caPub, err := certauth.ParseAuthorizedKey(cfg.CAPubKey)
	if err != nil {
		return fmt.Errorf("parse CA pubkey: %w", err)
	}
	hostCertPub, err := certauth.ParseAuthorizedKey(cfg.HostCert)
	if err != nil {
		return fmt.Errorf("parse host cert: %w", err)
	}
	hostCert, ok := hostCertPub.(*ssh.Certificate)
	if !ok {
		return fmt.Errorf("stored host cert is not a certificate")
	}
	certSigner, err := ssh.NewCertSigner(hostCert, hostSigner)
	if err != nil {
		return fmt.Errorf("build host cert signer: %w", err)
	}

	a := &Agent{tr: opts.Transport, addr: opts.Addr, nodeID: cfg.NodeID, sessions: session.NewManager()}
	a.srvConf = &ssh.ServerConfig{PublicKeyCallback: certauth.UserAuthCallback(caPub)}
	a.srvConf.AddHostKey(certSigner)

	go a.heartbeatLoop(ctx, cfg.Server, cfg.NodeID)
	go a.sessions.GC(ctx)
	return a.serve(ctx)
}

func register(ctx context.Context, cfg *config.AgentConfig, hostSigner ssh.Signer, opts Options) error {
	cfg.Server = opts.Server
	cp := cpclient.New(opts.Server, "")
	name := opts.Name
	if name == "" {
		name = defaultName()
	}
	// Advertise the address peers should dial. On TCP that is the listen address; on
	// the mesh it is this node's overlay (tailnet) address.
	advertise, err := transport.AdvertiseAddr(ctx, opts.Transport, opts.Addr)
	if err != nil {
		return fmt.Errorf("determine advertise address: %w", err)
	}
	resp, err := cp.Register(ctx, api.RegisterRequest{
		EnrollToken: opts.EnrollToken,
		Name:        name,
		HostPubKey:  string(ssh.MarshalAuthorizedKey(hostSigner.PublicKey())),
		Addr:        advertise,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Tags:        opts.Tags,
	})
	if err != nil {
		return err
	}
	cfg.NodeID = resp.NodeID
	cfg.CAPubKey = resp.CAPubKey
	cfg.HostCert = resp.HostCert
	return cfg.Save()
}

// Agent is a running node daemon.
type Agent struct {
	tr       transport.Transport
	addr     string
	nodeID   string
	srvConf  *ssh.ServerConfig
	sessions *session.Manager
}

func (a *Agent) serve(ctx context.Context) error {
	ln, err := a.tr.Listen(ctx, a.addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer ln.Close()
	log.Printf("openbox agent %s listening on %s (%s/%s)", a.nodeID, ln.Addr(), runtime.GOOS, runtime.GOARCH)

	go func() { <-ctx.Done(); ln.Close() }()

	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Printf("accept error: %v", err)
			continue
		}
		go a.handleConn(ctx, conn)
	}
}

func (a *Agent) heartbeatLoop(ctx context.Context, server, nodeID string) {
	cp := cpclient.New(server, "")
	t := time.NewTicker(HeartbeatInterval)
	defer t.Stop()
	// Beat immediately so the node shows online without waiting a full interval.
	_ = cp.Heartbeat(ctx, nodeID)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := cp.Heartbeat(ctx, nodeID); err != nil {
				log.Printf("heartbeat: %v", err)
			}
		}
	}
}

func (a *Agent) handleConn(ctx context.Context, nConn net.Conn) {
	sshConn, chans, reqs, err := ssh.NewServerConn(nConn, a.srvConf)
	if err != nil {
		log.Printf("ssh handshake failed: %v", err)
		return
	}
	defer sshConn.Close()
	go ssh.DiscardRequests(reqs)

	user := sshConn.User()
	// The control plane bound any session id into the user cert; it arrives here as
	// a cert extension, having already been verified during auth.
	var sessionID string
	if sshConn.Permissions != nil {
		sessionID = sshConn.Permissions.Extensions[ca.SessionExtension]
	}

	for newChan := range chans {
		if newChan.ChannelType() != "session" {
			newChan.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		ch, chReqs, err := newChan.Accept()
		if err != nil {
			log.Printf("channel accept: %v", err)
			continue
		}
		go a.handleSession(ctx, ch, chReqs, user, sessionID)
	}
}

// handleSession processes one SSH session channel: optional "env" requests (used to
// select isolation) followed by an "exec" request carrying the command. If a session
// id is bound, the command runs in the persistent shell for (user, session);
// otherwise it runs one-shot.
func (a *Agent) handleSession(ctx context.Context, ch ssh.Channel, reqs <-chan *ssh.Request, user, sessionID string) {
	defer ch.Close()
	iso := isolation.Default()
	var isoErr error // a requested-but-invalid isolation must fail closed, never downgrade
	for req := range reqs {
		switch req.Type {
		case "env":
			if k, v, ok := parseEnvPayload(req.Payload); ok && k == isolationEnv {
				if parsed, err := isolation.Parse(v); err == nil {
					iso = parsed
				} else {
					isoErr = fmt.Errorf("invalid isolation %q: %w", v, err)
				}
			}
			req.Reply(true, nil)
		case "exec":
			cmdline, err := parseExecPayload(req.Payload)
			if err != nil {
				req.Reply(false, nil)
				return
			}
			req.Reply(true, nil)
			if isoErr != nil {
				fmt.Fprintf(ch.Stderr(), "openbox: %v\n", isoErr)
				sendExitStatus(ch, 1)
				return
			}
			var code int
			if sessionID != "" {
				log.Printf("exec by %s [session %s, %s]: %s", user, sessionID, iso, cmdline)
				code = a.runInSession(ctx, ch, user, sessionID, iso, cmdline)
			} else {
				log.Printf("exec by %s [%s]: %s", user, iso, cmdline)
				code = a.runCommand(ctx, ch, iso, cmdline)
			}
			sendExitStatus(ch, code)
			return
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}

// runInSession routes the command into the persistent shell for (user, sessionID),
// started under the requested isolation. Output is merged (stdout+stderr).
func (a *Agent) runInSession(ctx context.Context, ch ssh.Channel, user, sessionID string, iso isolation.Isolation, cmdline string) int {
	key := user + "\x00" + sessionID
	code, err := a.sessions.Run(ctx, key, iso.ShellArgv(), cmdline, ch)
	if err != nil {
		fmt.Fprintf(ch.Stderr(), "openbox: session error: %v\n", err)
		return 1
	}
	return code
}

// runCommand executes cmdline one-shot under the requested isolation, streaming
// stdout/stderr separately to ch, and returns the exit code.
func (a *Agent) runCommand(ctx context.Context, ch ssh.Channel, iso isolation.Isolation, cmdline string) int {
	argv := iso.OneShotArgv(cmdline)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = ch
	cmd.Stderr = ch.Stderr()
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(ch.Stderr(), "openbox: failed to start: %v\n", err)
		return 127
	}
	if err := cmd.Wait(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		fmt.Fprintf(ch.Stderr(), "openbox: %v\n", err)
		return 1
	}
	return 0
}

func parseExecPayload(p []byte) (string, error) {
	if len(p) < 4 {
		return "", fmt.Errorf("short exec payload")
	}
	n := binary.BigEndian.Uint32(p)
	if int(n) > len(p)-4 {
		return "", fmt.Errorf("bad exec length")
	}
	return string(p[4 : 4+n]), nil
}

// parseEnvPayload decodes an SSH "env" request payload: two length-prefixed strings
// (name, value).
func parseEnvPayload(p []byte) (key, val string, ok bool) {
	name, rest, ok := sshString(p)
	if !ok {
		return "", "", false
	}
	value, _, ok := sshString(rest)
	if !ok {
		return "", "", false
	}
	return name, value, true
}

func sshString(p []byte) (string, []byte, bool) {
	if len(p) < 4 {
		return "", nil, false
	}
	n := binary.BigEndian.Uint32(p)
	if int(n) > len(p)-4 {
		return "", nil, false
	}
	return string(p[4 : 4+n]), p[4+n:], true
}

func sendExitStatus(ch ssh.Channel, code int) {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(code))
	ch.SendRequest("exit-status", false, payload)
}

func defaultName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "node"
}
