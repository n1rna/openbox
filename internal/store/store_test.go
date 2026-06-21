package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// openTestStore returns a store backed by Postgres when OPENBOX_TEST_PG is set
// (a postgres:// DSN), otherwise a throwaway SQLite file. The same test body
// therefore exercises both dialects — run with OPENBOX_TEST_PG to validate the
// Neon/Postgres path.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	ctx := context.Background()
	if dsn := os.Getenv("OPENBOX_TEST_PG"); dsn != "" {
		st, err := Open(ctx, dsn)
		if err != nil {
			t.Fatalf("open postgres: %v", err)
		}
		// Start from a clean slate so reruns don't collide on primary keys.
		for _, tbl := range []string{"sessions", "enroll_tokens", "nodes", "users"} {
			if _, err := st.db.ExecContext(ctx, "DELETE FROM "+tbl); err != nil {
				t.Fatalf("truncate %s: %v", tbl, err)
			}
		}
		t.Cleanup(func() { st.Close() })
		return st
	}
	st, err := Open(ctx, filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestUsersAndTokens(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	if n, err := st.CountUsers(ctx); err != nil || n != 0 {
		t.Fatalf("CountUsers = %d, %v; want 0", n, err)
	}
	u, err := st.CreateUser(ctx, "usr_1", "me", "secret-token")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if n, _ := st.CountUsers(ctx); n != 1 {
		t.Fatalf("CountUsers = %d; want 1", n)
	}
	got, err := st.UserByToken(ctx, "secret-token")
	if err != nil || got.ID != u.ID {
		t.Fatalf("UserByToken = %+v, %v", got, err)
	}
	if _, err := st.UserByToken(ctx, "wrong"); err != ErrNotFound {
		t.Fatalf("UserByToken(wrong) err = %v; want ErrNotFound", err)
	}
}

func TestEnrollTokenLifecycle(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	if _, err := st.CreateEnrollToken(ctx, "enr_1", "usr_1", []string{"mac", "lab"}, time.Hour); err != nil {
		t.Fatalf("CreateEnrollToken: %v", err)
	}
	et, err := st.ConsumeEnrollToken(ctx, "enr_1")
	if err != nil {
		t.Fatalf("ConsumeEnrollToken: %v", err)
	}
	if len(et.Tags) != 2 || et.Tags[0] != "mac" {
		t.Fatalf("tags = %v; want [mac lab]", et.Tags)
	}
	// Second consume must fail (already used).
	if _, err := st.ConsumeEnrollToken(ctx, "enr_1"); err == nil {
		t.Fatal("expected error consuming an already-used token")
	}
	// Expired token.
	if _, err := st.CreateEnrollToken(ctx, "enr_2", "usr_1", nil, -time.Minute); err != nil {
		t.Fatalf("CreateEnrollToken(expired): %v", err)
	}
	if _, err := st.ConsumeEnrollToken(ctx, "enr_2"); err == nil {
		t.Fatal("expected error consuming an expired token")
	}
}

func TestNodeUpsertAndList(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	n := &Node{ID: "node_1", Name: "box", Owner: "usr_1", HostPubKey: "k", Addr: "1.2.3.4:7600",
		OS: "linux", Arch: "amd64", Tags: []string{"gpu"}}
	if err := st.UpsertNode(ctx, n); err != nil {
		t.Fatalf("UpsertNode: %v", err)
	}
	// Upsert again with a new addr — must update, not duplicate (exercises ON CONFLICT).
	n.Addr = "5.6.7.8:7600"
	n.Tags = []string{"gpu", "fast"}
	if err := st.UpsertNode(ctx, n); err != nil {
		t.Fatalf("UpsertNode(update): %v", err)
	}
	got, err := st.NodeByID(ctx, "usr_1", "node_1")
	if err != nil || got.Addr != "5.6.7.8:7600" || len(got.Tags) != 2 {
		t.Fatalf("NodeByID = %+v, %v", got, err)
	}
	all, err := st.ListNodes(ctx, "usr_1", "")
	if err != nil || len(all) != 1 {
		t.Fatalf("ListNodes = %d, %v; want 1", len(all), err)
	}
	if tagged, _ := st.ListNodes(ctx, "usr_1", "fast"); len(tagged) != 1 {
		t.Fatalf("ListNodes(tag=fast) = %d; want 1", len(tagged))
	}
	if none, _ := st.ListNodes(ctx, "usr_1", "nope"); len(none) != 0 {
		t.Fatalf("ListNodes(tag=nope) = %d; want 0", len(none))
	}
}

func TestSessionBinding(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	if _, err := st.SessionNode(ctx, "usr_1", "build"); err != ErrNotFound {
		t.Fatalf("SessionNode(unbound) err = %v; want ErrNotFound", err)
	}
	node, created, err := st.BindSession(ctx, "usr_1", "build", "node_1")
	if err != nil || !created || node != "node_1" {
		t.Fatalf("BindSession = %q, %v, %v", node, created, err)
	}
	// Re-binding the same session returns the original node, created=false.
	node, created, err = st.BindSession(ctx, "usr_1", "build", "node_2")
	if err != nil || created || node != "node_1" {
		t.Fatalf("BindSession(again) = %q, %v, %v; want node_1,false", node, created, err)
	}
	if got, err := st.SessionNode(ctx, "usr_1", "build"); err != nil || got != "node_1" {
		t.Fatalf("SessionNode = %q, %v", got, err)
	}
	if err := st.DeleteSession(ctx, "usr_1", "build"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := st.SessionNode(ctx, "usr_1", "build"); err != ErrNotFound {
		t.Fatalf("after delete err = %v; want ErrNotFound", err)
	}
}
