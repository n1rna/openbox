package transport

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"

	"tailscale.com/tsnet"
)

// TSConfig configures the embedded-Tailscale transport.
type TSConfig struct {
	Hostname   string // node name on the tailnet
	AuthKey    string // pre-auth key (Tailscale or Headscale); empty = interactive login
	ControlURL string // coordination server; empty = Tailscale's default
	Dir        string // persistent state directory (node key, etc.)
	Verbose    bool   // surface tsnet logs
}

// TSNet is a Transport backed by an embedded Tailscale node (tsnet). Every openbox
// process that uses it becomes a node on the tailnet, giving the agent and CLI
// NAT-traversing, end-to-end-encrypted overlay addresses. It satisfies Advertiser so
// the agent can register its tailnet IP.
type TSNet struct {
	cfg TSConfig

	mu  sync.Mutex
	srv *tsnet.Server
	ip  netip.Addr // cached tailnet IPv4 once up
}

// NewTSNet returns a tsnet-backed transport. The underlying node is brought up lazily
// on the first Listen/Dial/AdvertiseAddr call.
func NewTSNet(cfg TSConfig) *TSNet { return &TSNet{cfg: cfg} }

// server lazily constructs the tsnet.Server.
func (t *TSNet) server() *tsnet.Server {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.srv == nil {
		t.srv = &tsnet.Server{
			Hostname:   t.cfg.Hostname,
			AuthKey:    t.cfg.AuthKey,
			ControlURL: t.cfg.ControlURL,
			Dir:        t.cfg.Dir,
		}
		if !t.cfg.Verbose {
			t.srv.Logf = func(string, ...any) {}
			t.srv.UserLogf = func(string, ...any) {}
		}
	}
	return t.srv
}

// up brings the node online and caches its tailnet IPv4.
func (t *TSNet) up(ctx context.Context) error {
	s := t.server()
	status, err := s.Up(ctx)
	if err != nil {
		return fmt.Errorf("tailscale up: %w", err)
	}
	for _, ip := range status.TailscaleIPs {
		if ip.Is4() {
			t.mu.Lock()
			t.ip = ip
			t.mu.Unlock()
			break
		}
	}
	return nil
}

// Listen brings the node up and listens on the tailnet. addr is normally ":port".
func (t *TSNet) Listen(ctx context.Context, addr string) (net.Listener, error) {
	if err := t.up(ctx); err != nil {
		return nil, err
	}
	return t.server().Listen("tcp", addr)
}

// Dial dials addr (a tailnet IP:port or MagicDNS name) over the overlay.
func (t *TSNet) Dial(ctx context.Context, addr string) (net.Conn, error) {
	if err := t.up(ctx); err != nil {
		return nil, err
	}
	return t.server().Dial(ctx, "tcp", addr)
}

// AdvertiseAddr reports the tailnet address peers should dial to reach a listener
// bound to listenAddr (whose host part is ignored; only the port is used).
func (t *TSNet) AdvertiseAddr(ctx context.Context, listenAddr string) (string, error) {
	if err := t.up(ctx); err != nil {
		return "", err
	}
	t.mu.Lock()
	ip := t.ip
	t.mu.Unlock()
	if !ip.IsValid() {
		return "", fmt.Errorf("tailnet address not assigned yet")
	}
	port := portOf(listenAddr)
	if port == "" {
		return "", fmt.Errorf("listen address %q has no port", listenAddr)
	}
	return net.JoinHostPort(ip.String(), port), nil
}

// Close shuts the node down.
func (t *TSNet) Close() error {
	t.mu.Lock()
	srv := t.srv
	t.mu.Unlock()
	if srv != nil {
		return srv.Close()
	}
	return nil
}

func portOf(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil {
		return port
	}
	// Bare ":7600" or "7600".
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		return addr[i+1:]
	}
	return addr
}
