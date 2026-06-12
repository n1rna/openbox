// Package session implements persistent shell sessions on a node. Commands sharing a
// session id run in the SAME long-lived shell, in order, so working directory,
// environment variables, and shell state persist across openbox invocations:
//
//	openbox -t mac -s build cd /tmp/work
//	openbox -t mac -s build make        # runs in /tmp/work
//
// Each session is a `/bin/sh` reading commands from a pipe (no PTY: no echo, no
// prompt, non-interactive — what automation wants). Output is captured per command
// by writing a unique marker carrying the exit code after each command, then
// streaming everything up to that marker back to the caller.
package session

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// IdleTTL is how long a session may sit unused before it is reaped.
const IdleTTL = 30 * time.Minute

// Manager owns all live sessions on a node, keyed by an opaque caller-supplied key
// (openbox uses "<user>\x00<session-id>").
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*session
}

// NewManager returns an empty session manager.
func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*session)}
}

// Run executes cmdline in the session identified by key, creating the session on
// first use with the given shell argv (the isolation backend's shell command), and
// streams merged stdout/stderr to out. It returns the command's exit code. Commands
// within a session are serialized in submission order. shellArgv is only consulted
// when the session is first created; a session's isolation is fixed for its life.
func (m *Manager) Run(ctx context.Context, key string, shellArgv []string, cmdline string, out io.Writer) (int, error) {
	s, err := m.get(key, shellArgv)
	if err != nil {
		return -1, err
	}
	return s.run(cmdline, out)
}

func (m *Manager) get(key string, shellArgv []string) (*session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[key]; ok && !s.dead {
		return s, nil
	}
	s, err := newSession(shellArgv)
	if err != nil {
		return nil, err
	}
	m.sessions[key] = s
	return s, nil
}

// GC reaps idle and dead sessions until ctx is cancelled, then closes everything.
func (m *Manager) GC(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.closeAll()
			return
		case <-t.C:
			m.reap()
		}
	}
}

func (m *Manager) reap() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, s := range m.sessions {
		if s.dead || time.Since(s.lastUsed()) > IdleTTL {
			s.close()
			delete(m.sessions, k)
		}
	}
}

func (m *Manager) closeAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, s := range m.sessions {
		s.close()
		delete(m.sessions, k)
	}
}

// Count returns the number of live sessions (used in tests/diagnostics).
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// session is one persistent shell.
type session struct {
	mu     sync.Mutex // serializes commands within the session
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Reader
	marker []byte
	dead   bool

	tmu  sync.Mutex
	used time.Time
}

func newSession(shellArgv []string) (*session, error) {
	if len(shellArgv) == 0 {
		shellArgv = []string{"/bin/sh"}
	}
	cmd := exec.Command(shellArgv[0], shellArgv[1:]...)
	// A predictable, quiet environment for the session shell.
	cmd.Env = append(os.Environ(), "PS1=", "PS2=")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// When the shell exits, unblock any pending reader.
	go func() { cmd.Wait(); pw.Close() }()

	nonce := make([]byte, 8)
	_, _ = rand.Read(nonce)
	s := &session{
		cmd:    cmd,
		stdin:  stdin,
		out:    bufio.NewReader(pr),
		marker: []byte("\x01__OBX_" + hex.EncodeToString(nonce) + "__\x01"),
		used:   time.Now(),
	}
	return s, nil
}

func (s *session) lastUsed() time.Time {
	s.tmu.Lock()
	defer s.tmu.Unlock()
	return s.used
}

func (s *session) touch() {
	s.tmu.Lock()
	s.used = time.Now()
	s.tmu.Unlock()
}

// run submits one command and streams its output up to the marker.
func (s *session) run(cmdline string, out io.Writer) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dead {
		return -1, fmt.Errorf("session is no longer alive")
	}
	s.touch()

	// Run the command, then print the marker followed by the exit code. The marker
	// has no leading newline so output without a trailing newline is preserved
	// exactly; we byte-scan for the marker rather than line-scan.
	script := cmdline + "\nprintf '%s%d\\n' '" + string(s.marker) + "' \"$?\"\n"
	if _, err := io.WriteString(s.stdin, script); err != nil {
		s.markDead()
		return -1, fmt.Errorf("write to session: %w", err)
	}

	code, err := streamUntilMarker(s.out, out, s.marker)
	if err != nil {
		s.markDead()
		return -1, fmt.Errorf("session shell ended: %w", err)
	}
	s.touch()
	return code, nil
}

func (s *session) markDead() { s.dead = true }

func (s *session) close() {
	s.dead = true
	if s.stdin != nil {
		s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
}

// streamUntilMarker copies bytes from r to w until marker is seen, then parses the
// trailing "<exit-code>\n" and returns it. It keeps a small tail unwritten so a
// marker split across reads is still detected, and never writes marker bytes to w.
func streamUntilMarker(r *bufio.Reader, w io.Writer, marker []byte) (int, error) {
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, rerr := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if idx := bytes.Index(buf, marker); idx >= 0 {
				if _, err := w.Write(buf[:idx]); err != nil {
					return -1, err
				}
				return readExitCode(r, buf[idx+len(marker):])
			}
			// Flush everything that cannot be the start of a marker.
			if keep := len(marker) - 1; len(buf) > keep {
				if _, err := w.Write(buf[:len(buf)-keep]); err != nil {
					return -1, err
				}
				buf = append(buf[:0], buf[len(buf)-keep:]...)
			}
		}
		if rerr != nil {
			return -1, rerr
		}
	}
}

// readExitCode reads "<digits>\n", which may already be partly in rest.
func readExitCode(r *bufio.Reader, rest []byte) (int, error) {
	for !bytes.ContainsRune(rest, '\n') {
		b, err := r.ReadByte()
		if err != nil {
			return -1, err
		}
		rest = append(rest, b)
	}
	line := rest[:bytes.IndexByte(rest, '\n')]
	return strconv.Atoi(strings.TrimSpace(string(line)))
}
