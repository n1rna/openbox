---
title: Architecture
description: The trust model, the data path, and how the control plane brokers identity without sitting in the way of your commands.
---

openbox is one Go binary in three roles: the **control plane** (self-hosted),
the **agent** (on every node), and the **CLI** (anywhere). The defining choice
is that the control plane brokers *identity* and then steps out of the *data path*.

## The data path

```text
                        ┌──────────────────────────┐
   openbox login  ───►  │   control plane (thin)   │
                        │  · user auth / tokens    │
        dispatch        │  · node registry + tags  │
   (resolve tag, mint   │  · SSH certificate CA     │
    short-lived cert)   │  · session directory     │
            ┌───────────┴────────────┐
       ┌────▼─────┐  peer-to-peer  ┌──▼───────┐
       │ openbox  │  cert-verified │ openbox  │
       │  (CLI)   │ ──── SSH ────► │  agent   │
       └──────────┘                └──────────┘
```

When you run `openbox -t mac uname -a`:

1. The CLI asks the control plane to **dispatch**: resolve the tag to a node and
   mint a short-lived SSH **user certificate** (default 5-minute TTL).
2. The control plane returns the node's address and the cert material. It does
   not proxy anything.
3. The CLI dials the node's agent directly and runs the command over a mutually
   cert-verified SSH channel. Your command bytes never touch the control plane.

## Trust model

openbox uses an SSH **certificate authority**, the Teleport / Netflix-BLESS
model: one CA, ephemeral certificates, no static key distribution.

- **One CA** lives on the control plane. Nodes get a **host certificate** at
  registration; users get a **user certificate** per dispatch.
- A node accepts a connection **only** if the client presents a user cert signed
  by the CA and valid for the connecting username (the cert principal). Bare
  keys, expired certs, wrong-CA certs, and principal mismatches are all rejected.
- The client accepts a node **only** if it presents a host cert signed by the CA
  and valid for the expected node id.
- Certs are short-lived, so there's nothing long-lived to steal or revoke from
  the data path.

This logic is small and tested (`internal/certauth`): mutual verification in
both directions, with the failure cases covered.

## Enrollment

A node joins in one of three ways:

1. **user / password / ip** — the control-plane operator runs `openbox node add`,
   which SSHes in with a password, uploads the agent, and launches it.
2. **user / ip / ssh-key** — same, but with a key instead of a password.
3. **token on the node** — `openbox node token` mints a one-time enrollment
   token; you run `openbox agent --token …` directly on the box.

For methods 1 and 2 the bootstrap is one-time: afterward the node holds its own
identity (node id + host cert) and never needs the original credentials again.

## Sessions

A session is a persistent shell on a node, keyed by `<user, session-id>`. The
session id is bound into the user certificate as an extension, so the agent
knows which shell to route a command into. The shell reads commands from a pipe;
output is framed with a marker line carrying the exit code, so the agent can
stream stdout/stderr and still report the precise exit status. Idle sessions are
reaped on a timer.

## Isolation

Each command runs in one of three backends, chosen by the caller:

- **native** — a shell on the host (default; lightweight).
- **docker** — inside a container; a session keeps one container alive across
  its commands.
- **nspawn** — inside a systemd-nspawn container (Linux).

The requested mode is carried to the agent and fails **closed** — an unknown or
malformed spec is rejected rather than silently downgraded to host execution.

## The dashboard

The control plane serves a single-page dashboard: a live map of the fleet
(online/offline, tags, platform) with filtering and auto-refresh, an active
sessions table, and an add-node flow that mints enrollment tokens. Click a node
for a detail drawer where you can rename it, edit tags inline, run commands in a
**live web console** (output streams back over Server-Sent Events), and remove it.

The web console is the one deliberate, scoped exception to "control plane not in
the data path": for a browser-initiated command the control plane mints itself
an ephemeral user cert (acting as you) and proxies the connection to the node,
so the node's auth is unchanged. It needs the control plane to be able to reach
the node — run `openbox control --mesh …` if your nodes are on the
[overlay](/mesh/).

## Transport

The network substrate sits behind an interface. The default is plain TCP (LAN or
same-host). The [mesh](/mesh/) swaps in an embedded Tailscale/Headscale overlay
without touching the auth or exec layers — nodes and laptops reach each other
peer-to-peer across NATs.

## On-disk layout

```text
~/.openbox/
  config.json            CLI login state (server, token, key)
  control/
    openbox.db           sqlite registry: users, nodes, tokens, sessions
    ca_key               the CA private key
  agent/
    agent.json           node identity: id, host cert, CA pubkey, server
```
