// Package daemon implements openboxd: a long-lived local process that holds the
// transport (notably the embedded mesh node) open and runs commands on its
// behalf. The CLI forwards a run request over a Unix socket instead of building
// its own transport per invocation — so mesh-targeted commands are instant
// rather than paying a tailnet join every time.
//
// The daemon reuses the exact same client.Run path the inline CLI uses; it only
// supplies the held transport and a framing writer. The wire behavior on the
// node is therefore identical whether or not a daemon is in the loop.
package daemon

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
)

// Request is the run request the CLI sends to the daemon (one JSON line).
type Request struct {
	Tag       string `json:"tag,omitempty"`
	NodeID    string `json:"node,omitempty"`
	SessionID string `json:"session,omitempty"`
	Isolation string `json:"isolation,omitempty"`
	Command   string `json:"command"`
}

// Frame kinds in the response stream.
const (
	kindStdout byte = 1
	kindStderr byte = 2
	kindExit   byte = 3 // payload: 4-byte big-endian int32 exit code
	kindError  byte = 4 // payload: UTF-8 error message
)

const maxFrame = 1 << 20 // 1 MiB; output is chunked well below this

// writeFrame writes one length-prefixed frame. The mutex serializes writes so
// stdout and stderr frames (written from separate goroutines by the ssh client)
// never interleave on the wire.
func writeFrame(w io.Writer, mu *sync.Mutex, kind byte, payload []byte) error {
	mu.Lock()
	defer mu.Unlock()
	var hdr [5]byte
	hdr[0] = kind
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	return nil
}

// frameWriter adapts a frame kind to an io.Writer, so it can be handed to
// sshexec as a stdout/stderr sink.
type frameWriter struct {
	w    io.Writer
	mu   *sync.Mutex
	kind byte
}

func (fw frameWriter) Write(p []byte) (int, error) {
	if err := writeFrame(fw.w, fw.mu, fw.kind, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// writeRequest encodes a request as a single JSON line.
func writeRequest(w io.Writer, req Request) error {
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = w.Write(b)
	return err
}

// readRequest reads and decodes one JSON-line request.
func readRequest(r *bufio.Reader) (Request, error) {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return Request{}, err
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		return Request{}, fmt.Errorf("bad request: %w", err)
	}
	return req, nil
}

// Dispatch runs req against an already-connected daemon, streaming stdout/stderr
// to the given writers and returning the remote exit code. The caller owns conn
// and should close it. ctx cancellation closes the read side promptly.
func Dispatch(ctx context.Context, conn net.Conn, req Request, stdout, stderr io.Writer) (int, error) {
	if err := writeRequest(conn, req); err != nil {
		return -1, fmt.Errorf("send request: %w", err)
	}

	// Closing the conn on cancel unblocks the read loop below.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	r := bufio.NewReader(conn)
	var hdr [5]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if ctx.Err() != nil {
				return -1, ctx.Err()
			}
			return -1, fmt.Errorf("daemon closed stream: %w", err)
		}
		kind := hdr[0]
		n := binary.BigEndian.Uint32(hdr[1:])
		if n > maxFrame {
			return -1, fmt.Errorf("oversized frame (%d bytes)", n)
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			return -1, fmt.Errorf("short frame: %w", err)
		}
		switch kind {
		case kindStdout:
			if _, err := stdout.Write(payload); err != nil {
				return -1, err
			}
		case kindStderr:
			if _, err := stderr.Write(payload); err != nil {
				return -1, err
			}
		case kindExit:
			if len(payload) != 4 {
				return -1, fmt.Errorf("malformed exit frame")
			}
			return int(int32(binary.BigEndian.Uint32(payload))), nil
		case kindError:
			return -1, fmt.Errorf("%s", payload)
		default:
			return -1, fmt.Errorf("unknown frame kind %d", kind)
		}
	}
}
