package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"sync"
	"testing"
)

// fakeServer reads one request off conn and replays the given frames, asserting
// the request decoded as expected.
func fakeServer(t *testing.T, conn net.Conn, wantCmd string, frames func(write func(kind byte, p []byte))) {
	t.Helper()
	defer conn.Close()
	req, err := readRequest(bufio.NewReader(conn))
	if err != nil {
		t.Errorf("server readRequest: %v", err)
		return
	}
	if req.Command != wantCmd {
		t.Errorf("command = %q, want %q", req.Command, wantCmd)
	}
	var mu sync.Mutex
	frames(func(kind byte, p []byte) {
		if err := writeFrame(conn, &mu, kind, p); err != nil {
			t.Errorf("writeFrame: %v", err)
		}
	})
}

func TestDispatchStreamsAndExits(t *testing.T) {
	cli, srv := net.Pipe()
	go fakeServer(t, srv, "echo hi", func(w func(byte, []byte)) {
		w(kindStdout, []byte("hello "))
		w(kindStdout, []byte("world\n"))
		w(kindStderr, []byte("a warning\n"))
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(int32(7)))
		w(kindExit, b[:])
	})

	var out, errb bytes.Buffer
	code, err := Dispatch(context.Background(), cli, Request{Command: "echo hi"}, &out, &errb)
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
	if got := out.String(); got != "hello world\n" {
		t.Errorf("stdout = %q, want %q", got, "hello world\n")
	}
	if got := errb.String(); got != "a warning\n" {
		t.Errorf("stderr = %q, want %q", got, "a warning\n")
	}
}

func TestDispatchErrorFrame(t *testing.T) {
	cli, srv := net.Pipe()
	go fakeServer(t, srv, "boom", func(w func(byte, []byte)) {
		w(kindError, []byte("no node matches tag \"mac\""))
	})

	var out, errb bytes.Buffer
	_, err := Dispatch(context.Background(), cli, Request{Command: "boom"}, &out, &errb)
	if err == nil {
		t.Fatal("expected an error from an error frame")
	}
	if err.Error() != `no node matches tag "mac"` {
		t.Errorf("error = %q", err.Error())
	}
}

func TestDispatchCancel(t *testing.T) {
	cli, srv := net.Pipe()
	defer srv.Close()
	// Server reads the request but never replies; cancel should unblock Dispatch.
	go func() { _, _ = readRequest(bufio.NewReader(srv)) }()

	ctx, cancel := context.WithCancel(context.Background())
	go cancel()
	var out, errb bytes.Buffer
	if _, err := Dispatch(ctx, cli, Request{Command: "hang"}, &out, &errb); err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestDefaultSocketPathEnvOverride(t *testing.T) {
	t.Setenv("OPENBOX_DAEMON_SOCKET", "/tmp/custom-openboxd.sock")
	if got := DefaultSocketPath(); got != "/tmp/custom-openboxd.sock" {
		t.Errorf("DefaultSocketPath = %q", got)
	}
}

func TestConnectNoDaemon(t *testing.T) {
	if conn := Connect("/tmp/openbox-nonexistent-daemon.sock"); conn != nil {
		conn.Close()
		t.Fatal("expected nil conn when no daemon is listening")
	}
}
