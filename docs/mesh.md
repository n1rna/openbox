---
title: Mesh networking
description: Put nodes and laptops on an embedded Tailscale/Headscale overlay so they reach each other peer-to-peer across NATs — no port forwarding.
---

By default openbox's transport is plain TCP, which is perfect on a LAN or the
same host. To reach machines across NATs — a GPU box at the office, a Mac mini at
home — add `--mesh` to put the agent **and** the CLI on an embedded
Tailscale-style overlay. Nodes and laptops then connect directly over WireGuard,
with no port forwarding.

The mesh lives behind the same transport interface as plain TCP, so it drops in
without changing the auth or exec layers: commands are still peer-to-peer and
still mutually cert-verified.

## With Tailscale or Headscale

It works with Tailscale's coordination service or a self-hosted **Headscale**
(verified end-to-end against Headscale).

### Agent

The agent joins the tailnet and advertises its overlay address (`100.x.y.z`) to
the control plane:

```sh
openbox agent --mesh \
  --mesh-control https://headscale.example.com \
  --mesh-authkey tskey-… \
  --tag mac
```

### CLI

The CLI joins too. Use environment variables so the mesh composes with the
`-t … <cmd>` shorthand:

```sh
export OPENBOX_MESH=1
export OPENBOX_MESH_CONTROL=https://headscale.example.com
export OPENBOX_MESH_AUTHKEY=tskey-…

openbox -t mac uname -a        # dialed peer-to-peer over WireGuard
```

| Variable | Flag equivalent | Purpose |
|---|---|---|
| `OPENBOX_MESH` | `--mesh` | enable the overlay transport |
| `OPENBOX_MESH_CONTROL` | `--mesh-control` | coordination server URL |
| `OPENBOX_MESH_AUTHKEY` | `--mesh-authkey` | pre-auth key (first join only) |
| `OPENBOX_MESH_HOSTNAME` | `--mesh-hostname` | node name on the tailnet |

## Control plane on the mesh

The [web console](/architecture/#the-dashboard) needs the control plane to be
able to reach the node it's proxying to. If your nodes are on the overlay, put
the control plane on it as well:

```sh
openbox control --mesh \
  --mesh-control https://headscale.example.com \
  --mesh-authkey tskey-…
```

## Keep the mesh warm with openboxd

Without a daemon, the CLI brings up its own tailnet node on every invocation,
which adds a few seconds on a cold start. Run **openboxd** to hold the mesh open
once and make every command instant:

```sh
openbox daemon --mesh \
  --mesh-control https://headscale.example.com \
  --mesh-authkey tskey-…
```

The daemon listens on a local Unix socket (`~/.openbox/openboxd.sock`). When it's
running, `openbox -t … <cmd>` automatically forwards the request to it instead of
building its own transport — so you keep the warm WireGuard connection across
invocations. If no daemon is running, the CLI falls back to the inline path
transparently (set `OPENBOX_NO_DAEMON=1` to force that). See the
[CLI reference](/cli/#openbox-daemon) for running it under systemd.

On a LAN, plain TCP (the default) has no join cost, but the daemon still helps if
you want a single long-lived process owning node connections.
