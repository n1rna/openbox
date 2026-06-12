// Package transport abstracts the network substrate that openbox processes use
// to reach each other. Phase 0 ships a plain TCP loopback implementation; Phase 1
// swaps in an embedded Tailscale (tsnet) implementation that satisfies the same
// interface, so the agent and client exec layers never change.
package transport

import (
	"context"
	"net"
)

// Transport is a bidirectional network substrate. An agent Listens on it; a
// client Dials it. The address semantics are implementation-defined (a host:port
// for TCP, a node name / tailnet IP for tsnet).
type Transport interface {
	// Listen returns a listener for inbound connections from peers.
	Listen(ctx context.Context, addr string) (net.Listener, error)

	// Dial opens a connection to a peer at addr.
	Dial(ctx context.Context, addr string) (net.Conn, error)

	// Close releases any resources held by the transport.
	Close() error
}

// Advertiser is an optional Transport capability: given the local listen address, it
// reports the address peers should use to reach this listener. For TCP that is the
// listen address itself; for an overlay (tsnet) it is the node's overlay address. The
// agent advertises this value to the control plane at registration.
type Advertiser interface {
	AdvertiseAddr(ctx context.Context, listenAddr string) (string, error)
}

// AdvertiseAddr returns tr's advertised address for listenAddr, falling back to
// listenAddr when the transport does not implement Advertiser.
func AdvertiseAddr(ctx context.Context, tr Transport, listenAddr string) (string, error) {
	if a, ok := tr.(Advertiser); ok {
		return a.AdvertiseAddr(ctx, listenAddr)
	}
	return listenAddr, nil
}
