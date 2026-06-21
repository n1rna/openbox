package daemon

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"

	"time"

	"openbox.io/openbox/internal/client"
	"openbox.io/openbox/internal/config"
	"openbox.io/openbox/internal/transport"
)

// dialTimeout bounds how long the CLI waits when probing for a running daemon.
const dialTimeout = 300 * time.Millisecond

// Options configures the daemon.
type Options struct {
	Transport  transport.Transport
	Config     *config.ClientConfig
	SocketPath string
	Log        io.Writer // optional progress log (defaults to discard)
}

// Serve listens on the Unix socket and handles run requests until ctx is
// cancelled. The held transport is reused across every request.
func Serve(ctx context.Context, opts Options) error {
	if opts.Config == nil || !opts.Config.LoggedIn() {
		return fmt.Errorf("not logged in — run `openbox login` before starting the daemon")
	}
	logw := opts.Log
	if logw == nil {
		logw = io.Discard
	}
	// Materialize the signer once up front so concurrent requests never race to
	// generate + persist a key.
	if _, err := opts.Config.Signer(); err != nil {
		return fmt.Errorf("client signer: %w", err)
	}
	if opts.Config.Dirty() {
		if err := opts.Config.Save(); err != nil {
			return fmt.Errorf("persist client key: %w", err)
		}
	}

	if err := os.MkdirAll(filepath.Dir(opts.SocketPath), 0o700); err != nil {
		return err
	}
	// Clear a stale socket from a previous run, but refuse if one is live.
	if isLive(opts.SocketPath) {
		return fmt.Errorf("a daemon is already listening on %s", opts.SocketPath)
	}
	_ = os.Remove(opts.SocketPath)

	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "unix", opts.SocketPath)
	if err != nil {
		return fmt.Errorf("listen %s: %w", opts.SocketPath, err)
	}
	defer func() {
		ln.Close()
		_ = os.Remove(opts.SocketPath)
	}()
	fmt.Fprintf(logw, "openboxd listening on %s\n", opts.SocketPath)

	// Unblock Accept when the context is cancelled.
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break // shutting down
			}
			if errors.Is(err, net.ErrClosed) {
				break
			}
			fmt.Fprintf(logw, "accept: %v\n", err)
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			handle(ctx, conn, opts, logw)
		}()
	}
	wg.Wait()
	return nil
}

// handle services one connection: read the request, run it via the shared
// client path, and stream framed output back.
func handle(ctx context.Context, conn net.Conn, opts Options, logw io.Writer) {
	defer conn.Close()
	var mu sync.Mutex

	req, err := readRequest(bufio.NewReader(conn))
	if err != nil {
		_ = writeFrame(conn, &mu, kindError, []byte("read request: "+err.Error()))
		return
	}

	// Tie the request lifetime to both the daemon ctx and the client hanging up.
	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go watchClose(conn, cancel)

	target := client.Target{
		Tag:       req.Tag,
		NodeID:    req.NodeID,
		SessionID: req.SessionID,
		Isolation: req.Isolation,
	}
	stdout := frameWriter{w: conn, mu: &mu, kind: kindStdout}
	stderr := frameWriter{w: conn, mu: &mu, kind: kindStderr}

	code, err := client.Run(reqCtx, opts.Config, opts.Transport, target, req.Command, stdout, stderr)
	if err != nil {
		_ = writeFrame(conn, &mu, kindError, []byte(err.Error()))
		return
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(int32(code)))
	_ = writeFrame(conn, &mu, kindExit, b[:])
}

// watchClose cancels the request when the client closes its end. After sending
// the request the client sends nothing more, so any read result (EOF or error)
// means it has gone away.
func watchClose(conn net.Conn, cancel context.CancelFunc) {
	buf := make([]byte, 1)
	for {
		if _, err := conn.Read(buf); err != nil {
			cancel()
			return
		}
	}
}

// isLive reports whether a daemon is already accepting on the socket.
func isLive(socket string) bool {
	c, err := net.DialTimeout("unix", socket, dialTimeout)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// DefaultSocketPath is the socket the CLI looks for and the daemon binds by
// default. OPENBOX_DAEMON_SOCKET overrides it.
func DefaultSocketPath() string {
	if s := os.Getenv("OPENBOX_DAEMON_SOCKET"); s != "" {
		return s
	}
	return filepath.Join(config.Home(), "openboxd.sock")
}

// Connect dials a running daemon at socket. It returns (nil, nil) when no daemon
// is listening (any dial error against a local socket), so callers can
// transparently fall back to the inline path.
func Connect(socket string) net.Conn {
	conn, err := net.DialTimeout("unix", socket, dialTimeout)
	if err != nil {
		return nil
	}
	return conn
}
