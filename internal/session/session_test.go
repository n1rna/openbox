package session

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func runOK(t *testing.T, m *Manager, key, cmd string) (string, int) {
	t.Helper()
	var buf bytes.Buffer
	code, err := m.Run(context.Background(), key, nil, cmd, &buf)
	if err != nil {
		t.Fatalf("run %q: %v", cmd, err)
	}
	return buf.String(), code
}

func TestSessionPersistsState(t *testing.T) {
	m := NewManager()
	dir := t.TempDir()

	// Working directory persists across commands in the same session.
	runOK(t, m, "s1", "cd "+dir)
	runOK(t, m, "s1", "touch persisted.txt")
	if _, err := os.Stat(filepath.Join(dir, "persisted.txt")); err != nil {
		t.Fatalf("cwd did not persist: %v", err)
	}

	// Environment persists too.
	runOK(t, m, "s1", "export FOO=bar")
	if out, _ := runOK(t, m, "s1", "echo $FOO"); out != "bar\n" {
		t.Fatalf("env did not persist: %q", out)
	}
}

func TestSessionsAreIsolated(t *testing.T) {
	m := NewManager()
	runOK(t, m, "a", "export SECRET=in-a")
	if out, _ := runOK(t, m, "b", "echo [$SECRET]"); out != "[]\n" {
		t.Fatalf("session b saw session a's state: %q", out)
	}
	if m.Count() != 2 {
		t.Fatalf("want 2 live sessions, got %d", m.Count())
	}
}

func TestExitCodes(t *testing.T) {
	m := NewManager()
	if _, code := runOK(t, m, "s", "true"); code != 0 {
		t.Fatalf("true: want 0, got %d", code)
	}
	if _, code := runOK(t, m, "s", "false"); code != 1 {
		t.Fatalf("false: want 1, got %d", code)
	}
	if _, code := runOK(t, m, "s", "(exit 42)"); code != 42 {
		t.Fatalf("exit 42: want 42, got %d", code)
	}
}

func TestOutputFraming(t *testing.T) {
	m := NewManager()
	// Output without a trailing newline must be preserved exactly (marker stripped).
	if out, _ := runOK(t, m, "s", "printf abc"); out != "abc" {
		t.Fatalf("no-newline output mangled: %q", out)
	}
	// A marker-sized chunk of normal output should pass through untouched.
	big := "0123456789012345678901234567890123456789"
	if out, _ := runOK(t, m, "s", "printf "+big); out != big {
		t.Fatalf("multi-chunk output mangled: %q", out)
	}
}

func TestOrdering(t *testing.T) {
	m := NewManager()
	dir := t.TempDir()
	runOK(t, m, "s", "cd "+dir)
	// Sequential appends must land in submission order.
	runOK(t, m, "s", "echo 1 >> log")
	runOK(t, m, "s", "echo 2 >> log")
	runOK(t, m, "s", "echo 3 >> log")
	data, err := os.ReadFile(filepath.Join(dir, "log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "1\n2\n3\n" {
		t.Fatalf("ordering wrong: %q", data)
	}
}
