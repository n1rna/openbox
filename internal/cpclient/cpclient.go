// Package cpclient is the HTTP client for the openbox control plane, used by both
// the CLI (dispatch, listing, login) and the node daemon (register, heartbeat).
package cpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"openbox.io/openbox/internal/api"
)

// Client talks to a control-plane base URL with an optional bearer token.
type Client struct {
	base  string
	token string
	hc    *http.Client
}

// New returns a client for base with the given user token (may be empty for
// node-facing endpoints).
func New(base, token string) *Client {
	return &Client{
		base:  strings.TrimRight(base, "/"),
		token: token,
		hc:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Register enrolls a node and returns its identity + trust material.
func (c *Client) Register(ctx context.Context, req api.RegisterRequest) (*api.RegisterResponse, error) {
	var resp api.RegisterResponse
	return &resp, c.do(ctx, "POST", "/v1/nodes/register", req, &resp)
}

// Heartbeat marks a node healthy.
func (c *Client) Heartbeat(ctx context.Context, nodeID string) error {
	return c.do(ctx, "POST", "/v1/nodes/heartbeat", api.HeartbeatRequest{NodeID: nodeID}, nil)
}

// Dispatch resolves a target node and obtains a user cert for the call.
func (c *Client) Dispatch(ctx context.Context, req api.DispatchRequest) (*api.DispatchResponse, error) {
	var resp api.DispatchResponse
	return &resp, c.do(ctx, "POST", "/v1/dispatch", req, &resp)
}

// ListNodes returns the user's nodes, optionally filtered by tag.
func (c *Client) ListNodes(ctx context.Context, tag string) (*api.ListNodesResponse, error) {
	path := "/v1/nodes"
	if tag != "" {
		path += "?tag=" + tag
	}
	var resp api.ListNodesResponse
	return &resp, c.do(ctx, "GET", path, nil, &resp)
}

// Whoami returns the authenticated user.
func (c *Client) Whoami(ctx context.Context) (*api.WhoamiResponse, error) {
	var resp api.WhoamiResponse
	return &resp, c.do(ctx, "GET", "/v1/whoami", nil, &resp)
}

// CreateEnrollToken mints a node enrollment token with the given tags.
func (c *Client) CreateEnrollToken(ctx context.Context, tags []string, ttl time.Duration) (*api.EnrollTokenResponse, error) {
	var resp api.EnrollTokenResponse
	req := api.EnrollTokenRequest{Tags: tags, TTLSeconds: int(ttl.Seconds())}
	return &resp, c.do(ctx, "POST", "/v1/enroll-tokens", req, &resp)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var e api.Error
		data, _ := io.ReadAll(resp.Body)
		_ = json.Unmarshal(data, &e)
		if e.Error == "" {
			e.Error = fmt.Sprintf("%s %s: %s", method, path, resp.Status)
		}
		return fmt.Errorf("%s", e.Error)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}
