// Package store is the control plane's persistence layer: users, the node
// registry, enrollment tokens, and the session directory.
//
// It speaks two SQL dialects behind one API, selected by the DSN: a pure-Go
// SQLite driver (a file path) for local/dev/self-hosted use, and Postgres (a
// postgres:// URL, e.g. Neon) for the hosted Cloudflare-Containers deployment
// where the container disk is ephemeral. Both drivers are pure Go, so the
// binary still cross-compiles without cgo. The queries are written with `?`
// placeholders and rebound to `$N` for Postgres; the schema uses portable types
// and a portable ON CONFLICT upsert.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // postgres (Neon) driver, pure Go
	_ "modernc.org/sqlite"             // sqlite driver, pure Go
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("not found")

type dialect int

const (
	dialectSQLite dialect = iota
	dialectPostgres
)

// Store wraps the database and remembers which dialect it speaks.
type Store struct {
	db      *sql.DB
	dialect dialect
}

// User is an openbox account. The raw token is never stored — only its hash.
type User struct {
	ID        string
	Name      string
	CreatedAt time.Time
}

// Node is a registered machine in the user's network.
type Node struct {
	ID         string
	Name       string
	Owner      string
	HostPubKey string // authorized_keys form of the node host key
	Addr       string // reachable address on the transport (host:port today, tailnet name later)
	OS         string
	Arch       string
	Tags       []string
	LastSeen   time.Time
	CreatedAt  time.Time
}

// EnrollToken is a one-time secret a node presents to register itself.
type EnrollToken struct {
	Token     string
	Owner     string
	Tags      []string
	Used      bool
	ExpiresAt time.Time
}

// Session maps a user-chosen session id to the node it was first bound to, so
// repeated `-s` calls with the same id reach the same node.
type Session struct {
	UserID    string
	SessionID string
	NodeID    string
	CreatedAt time.Time
	LastUsed  time.Time
}

// Open opens (and migrates) the database. dsn is either a sqlite file path or a
// postgres:// (Neon) URL; the dialect is chosen accordingly.
func Open(ctx context.Context, dsn string) (*Store, error) {
	driver, dia := "sqlite", dialectSQLite
	if isPostgresDSN(dsn) {
		driver, dia = "pgx", dialectPostgres
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	if dia == dialectSQLite {
		db.SetMaxOpenConns(1) // sqlite: serialize writers, avoids "database is locked"
	} else {
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(2)
		db.SetConnMaxIdleTime(5 * time.Minute)
	}
	s := &Store{db: db, dialect: dia}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func isPostgresDSN(dsn string) bool {
	return strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://")
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// rebind converts `?` placeholders to Postgres `$N`; SQLite keeps `?`.
func (s *Store) rebind(q string) string {
	if s.dialect != dialectPostgres {
		return q
	}
	var b strings.Builder
	b.Grow(len(q) + 8)
	n := 0
	for i := 0; i < len(q); i++ {
		if q[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		} else {
			b.WriteByte(q[i])
		}
	}
	return b.String()
}

// exec / queryRow / query wrap the database/sql calls so every query is rebound
// for the active dialect in one place.
func (s *Store) exec(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return s.db.ExecContext(ctx, s.rebind(q), args...)
}

func (s *Store) queryRow(ctx context.Context, q string, args ...any) *sql.Row {
	return s.db.QueryRowContext(ctx, s.rebind(q), args...)
}

func (s *Store) query(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return s.db.QueryContext(ctx, s.rebind(q), args...)
}

func (s *Store) migrate(ctx context.Context) error {
	// BIGINT for unix timestamps (Postgres INTEGER would overflow after 2038);
	// SQLite treats BIGINT as INTEGER affinity. ON CONFLICT upserts are portable
	// across both. Statements are executed one-by-one so the Postgres extended
	// protocol (one statement per Exec) is satisfied.
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  token_hash TEXT NOT NULL UNIQUE,
  created_at BIGINT NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS nodes (
  id           TEXT PRIMARY KEY,
  name         TEXT NOT NULL,
  owner        TEXT NOT NULL,
  host_pubkey  TEXT NOT NULL,
  addr         TEXT NOT NULL,
  os           TEXT NOT NULL,
  arch         TEXT NOT NULL,
  tags         TEXT NOT NULL,
  last_seen    BIGINT NOT NULL,
  created_at   BIGINT NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS enroll_tokens (
  token      TEXT PRIMARY KEY,
  owner      TEXT NOT NULL,
  tags       TEXT NOT NULL,
  used       INTEGER NOT NULL DEFAULT 0,
  expires_at BIGINT NOT NULL
)`,
		`CREATE TABLE IF NOT EXISTS sessions (
  user_id    TEXT NOT NULL,
  session_id TEXT NOT NULL,
  node_id    TEXT NOT NULL,
  created_at BIGINT NOT NULL,
  last_used  BIGINT NOT NULL,
  PRIMARY KEY (user_id, session_id)
)`,
	}
	for _, stmt := range stmts {
		if _, err := s.exec(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// HashToken returns the hex sha256 of a token, used as the stored credential.
func HashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

// --- users ---

// CreateUser inserts a user with the given id, name and token (token stored hashed).
func (s *Store) CreateUser(ctx context.Context, id, name, token string) (*User, error) {
	now := time.Now()
	_, err := s.exec(ctx,
		`INSERT INTO users (id, name, token_hash, created_at) VALUES (?, ?, ?, ?)`,
		id, name, HashToken(token), now.Unix())
	if err != nil {
		return nil, err
	}
	return &User{ID: id, Name: name, CreatedAt: now}, nil
}

// CountUsers returns the number of users, used to decide bootstrap.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.queryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// UserByToken resolves a raw token to its user, or ErrNotFound.
func (s *Store) UserByToken(ctx context.Context, token string) (*User, error) {
	row := s.queryRow(ctx,
		`SELECT id, name, created_at FROM users WHERE token_hash = ?`, HashToken(token))
	var u User
	var ts int64
	switch err := row.Scan(&u.ID, &u.Name, &ts); {
	case err == sql.ErrNoRows:
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	u.CreatedAt = time.Unix(ts, 0)
	return &u, nil
}

// --- enrollment tokens ---

// CreateEnrollToken stores a one-time enrollment token for owner with preset tags.
func (s *Store) CreateEnrollToken(ctx context.Context, token, owner string, tags []string, ttl time.Duration) (*EnrollToken, error) {
	exp := time.Now().Add(ttl)
	tagsJSON, _ := json.Marshal(tags)
	_, err := s.exec(ctx,
		`INSERT INTO enroll_tokens (token, owner, tags, used, expires_at) VALUES (?, ?, ?, 0, ?)`,
		token, owner, string(tagsJSON), exp.Unix())
	if err != nil {
		return nil, err
	}
	return &EnrollToken{Token: token, Owner: owner, Tags: tags, ExpiresAt: exp}, nil
}

// ConsumeEnrollToken validates and marks a token used, returning it. It fails if the
// token is unknown, already used, or expired.
func (s *Store) ConsumeEnrollToken(ctx context.Context, token string) (*EnrollToken, error) {
	row := s.queryRow(ctx,
		`SELECT token, owner, tags, used, expires_at FROM enroll_tokens WHERE token = ?`, token)
	var et EnrollToken
	var tagsJSON string
	var used int
	var exp int64
	switch err := row.Scan(&et.Token, &et.Owner, &tagsJSON, &used, &exp); {
	case err == sql.ErrNoRows:
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	if used != 0 {
		return nil, fmt.Errorf("enrollment token already used")
	}
	if time.Now().After(time.Unix(exp, 0)) {
		return nil, fmt.Errorf("enrollment token expired")
	}
	_ = json.Unmarshal([]byte(tagsJSON), &et.Tags)
	if _, err := s.exec(ctx, `UPDATE enroll_tokens SET used = 1 WHERE token = ?`, token); err != nil {
		return nil, err
	}
	et.Used = true
	return &et, nil
}

// --- nodes ---

// UpsertNode inserts or updates a node by id.
func (s *Store) UpsertNode(ctx context.Context, n *Node) error {
	tagsJSON, _ := json.Marshal(n.Tags)
	now := time.Now()
	if n.CreatedAt.IsZero() {
		n.CreatedAt = now
	}
	n.LastSeen = now
	_, err := s.exec(ctx, `
INSERT INTO nodes (id, name, owner, host_pubkey, addr, os, arch, tags, last_seen, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
  name=excluded.name, host_pubkey=excluded.host_pubkey, addr=excluded.addr,
  os=excluded.os, arch=excluded.arch, tags=excluded.tags, last_seen=excluded.last_seen`,
		n.ID, n.Name, n.Owner, n.HostPubKey, n.Addr, n.OS, n.Arch, string(tagsJSON),
		n.LastSeen.Unix(), n.CreatedAt.Unix())
	return err
}

// Touch updates a node's last_seen timestamp.
func (s *Store) Touch(ctx context.Context, nodeID string) error {
	_, err := s.exec(ctx, `UPDATE nodes SET last_seen = ? WHERE id = ?`, time.Now().Unix(), nodeID)
	return err
}

// NodeByID returns a single node owned by owner.
func (s *Store) NodeByID(ctx context.Context, owner, id string) (*Node, error) {
	row := s.queryRow(ctx, nodeSelect+` WHERE owner = ? AND id = ?`, owner, id)
	return scanNode(row.Scan)
}

// ListNodes returns all of owner's nodes. If tag is non-empty, only nodes carrying
// that tag are returned.
func (s *Store) ListNodes(ctx context.Context, owner, tag string) ([]*Node, error) {
	rows, err := s.query(ctx, nodeSelect+` WHERE owner = ? ORDER BY name`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Node
	for rows.Next() {
		n, err := scanNode(rows.Scan)
		if err != nil {
			return nil, err
		}
		if tag == "" || hasTag(n.Tags, tag) {
			out = append(out, n)
		}
	}
	return out, rows.Err()
}

// SetNodeMeta updates a node's display name and tags (owner-scoped). Returns
// ErrNotFound if no such node belongs to owner.
func (s *Store) SetNodeMeta(ctx context.Context, owner, id, name string, tags []string) error {
	tagsJSON, _ := json.Marshal(tags)
	res, err := s.exec(ctx,
		`UPDATE nodes SET name = ?, tags = ? WHERE owner = ? AND id = ?`,
		name, string(tagsJSON), owner, id)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

// DeleteNode removes a node and any sessions bound to it (owner-scoped).
func (s *Store) DeleteNode(ctx context.Context, owner, id string) error {
	res, err := s.exec(ctx, `DELETE FROM nodes WHERE owner = ? AND id = ?`, owner, id)
	if err != nil {
		return err
	}
	if err := mustAffect(res); err != nil {
		return err
	}
	_, _ = s.exec(ctx, `DELETE FROM sessions WHERE node_id = ?`, id)
	return nil
}

// DeleteSession removes a session binding (owner-scoped).
func (s *Store) DeleteSession(ctx context.Context, userID, sessionID string) error {
	res, err := s.exec(ctx,
		`DELETE FROM sessions WHERE user_id = ? AND session_id = ?`, userID, sessionID)
	if err != nil {
		return err
	}
	return mustAffect(res)
}

func mustAffect(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

const nodeSelect = `SELECT id, name, owner, host_pubkey, addr, os, arch, tags, last_seen, created_at FROM nodes`

func scanNode(scan func(...any) error) (*Node, error) {
	var n Node
	var tagsJSON string
	var lastSeen, createdAt int64
	switch err := scan(&n.ID, &n.Name, &n.Owner, &n.HostPubKey, &n.Addr, &n.OS, &n.Arch, &tagsJSON, &lastSeen, &createdAt); {
	case err == sql.ErrNoRows:
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	_ = json.Unmarshal([]byte(tagsJSON), &n.Tags)
	n.LastSeen = time.Unix(lastSeen, 0)
	n.CreatedAt = time.Unix(createdAt, 0)
	return &n, nil
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}

// --- sessions ---

// SessionNode returns the node id bound to (user, sessionID), or ErrNotFound if the
// session has no binding yet. It is a pure read and never creates a binding.
func (s *Store) SessionNode(ctx context.Context, userID, sessionID string) (string, error) {
	row := s.queryRow(ctx,
		`SELECT node_id FROM sessions WHERE user_id = ? AND session_id = ?`, userID, sessionID)
	var nodeID string
	switch err := row.Scan(&nodeID); {
	case err == sql.ErrNoRows:
		return "", ErrNotFound
	case err != nil:
		return "", err
	}
	_, _ = s.exec(ctx,
		`UPDATE sessions SET last_used = ? WHERE user_id = ? AND session_id = ?`,
		time.Now().Unix(), userID, sessionID)
	return nodeID, nil
}

// ListSessions returns a user's session bindings, most-recently-used first.
func (s *Store) ListSessions(ctx context.Context, userID string) ([]*Session, error) {
	rows, err := s.query(ctx,
		`SELECT user_id, session_id, node_id, created_at, last_used FROM sessions
		 WHERE user_id = ? ORDER BY last_used DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Session
	for rows.Next() {
		var s Session
		var created, last int64
		if err := rows.Scan(&s.UserID, &s.SessionID, &s.NodeID, &created, &last); err != nil {
			return nil, err
		}
		s.CreatedAt = time.Unix(created, 0)
		s.LastUsed = time.Unix(last, 0)
		out = append(out, &s)
	}
	return out, rows.Err()
}

// BindSession returns the node bound to (user, sessionID), creating the binding to
// preferredNode if none exists yet. The returned bool reports whether a new binding
// was created.
func (s *Store) BindSession(ctx context.Context, userID, sessionID, preferredNode string) (nodeID string, created bool, err error) {
	row := s.queryRow(ctx,
		`SELECT node_id FROM sessions WHERE user_id = ? AND session_id = ?`, userID, sessionID)
	switch err := row.Scan(&nodeID); {
	case err == nil:
		_, _ = s.exec(ctx,
			`UPDATE sessions SET last_used = ? WHERE user_id = ? AND session_id = ?`,
			time.Now().Unix(), userID, sessionID)
		return nodeID, false, nil
	case err != sql.ErrNoRows:
		return "", false, err
	}
	now := time.Now().Unix()
	_, err = s.exec(ctx,
		`INSERT INTO sessions (user_id, session_id, node_id, created_at, last_used) VALUES (?, ?, ?, ?, ?)`,
		userID, sessionID, preferredNode, now, now)
	if err != nil {
		return "", false, err
	}
	return preferredNode, true, nil
}
