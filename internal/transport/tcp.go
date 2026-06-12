package transport

import (
	"context"
	"net"
)

// TCP is the Phase 0 transport: plain, unencrypted-at-this-layer TCP. Confidentiality
// and peer identity are provided one layer up by the SSH protocol. In Phase 1 this is
// replaced by a tsnet-backed Transport that provides the encrypted overlay mesh.
type TCP struct {
	lc net.ListenConfig
	d  net.Dialer
}

// NewTCP returns a TCP transport.
func NewTCP() *TCP { return &TCP{} }

func (t *TCP) Listen(ctx context.Context, addr string) (net.Listener, error) {
	return t.lc.Listen(ctx, "tcp", addr)
}

func (t *TCP) Dial(ctx context.Context, addr string) (net.Conn, error) {
	return t.d.DialContext(ctx, "tcp", addr)
}

func (t *TCP) Close() error { return nil }
