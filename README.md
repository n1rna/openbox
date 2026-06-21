# openbox

Personal box manager. Control any Linux or Mac server you can reach, run commands
and containers on it, and let AI agents drive a whole fleet of machines from any
laptop — authenticate once, then `openbox -t mac make build` just works.

```
openbox -t mac  uname -a                 # run on any node tagged "mac"
openbox -t gpu  --docker pytorch/pytorch python train.py
openbox -t mac -s build cd /tmp/work     # persistent session: these three…
openbox -t mac -s build make             # …share cwd + env, run in order…
openbox -t mac -s build ./ship.sh        # …in the same shell on the same node
```

**Docs:** [openbox.n1rna.net](https://openbox.n1rna.net)

## Install

One static binary, no runtime dependencies:

```bash
curl -fsSL https://openbox.n1rna.net/install.sh | sh
```

Or grab a [release](https://github.com/n1rna/openbox/releases) tarball, or build
from source with `make build`. See the [install guide](https://openbox.n1rna.net/install/).

## How it works

openbox has three pieces, all from one Go binary:

```
                        ┌──────────────────────────┐
                        │   control plane (thin)   │   you self-host this
   openbox login  ───►  │  · user auth / tokens    │
                        │  · node registry + tags  │
                        │  · SSH certificate CA     │
                        │  · session directory     │
                        │  · web dashboard          │
                        └────────────┬─────────────┘
                                     │ brokers identity, then steps aside
        dispatch (resolve tag,       │
        mint short-lived user cert)  │
            ┌────────────────────────┴───────────┐
            │                                     │
       ┌────▼─────┐   peer-to-peer, mutually  ┌───▼──────┐
       │ openbox  │   cert-verified SSH       │ openbox  │
       │  (CLI)   │ ────────────────────────► │  agent   │  on each node
       │ any host │   (no control plane in    │ (node)   │
       └──────────┘    the data path)         └──────────┘
```

The control plane never sees your command traffic. It hands the CLI a short-lived
SSH **user certificate** and tells it which node to reach; the CLI then talks
straight to the node's agent. The node trusts the cert because the **openbox CA**
signed it, and the CLI verifies the node's **host certificate** the same way. This is
the Teleport / Netflix-BLESS model: one CA, ephemeral certs, no static key
distribution.

## Trust model

- **One CA** lives on the control plane. Nodes get a host cert at registration;
  users get a 5-minute user cert per dispatch.
- The node accepts a connection **only** if the client presents a user cert signed by
  the CA and valid for the connecting username (the cert principal). Bare keys,
  expired certs, wrong-CA certs, and principal mismatches are all rejected
  (`internal/certauth` + tests).
- The client accepts a node **only** if it presents a host cert signed by the CA and
  valid for the expected node id.
- Isolation requested by the caller fails **closed**: an unknown isolation mode is
  rejected, never silently downgraded to running on the host.

## Quickstart (self-hosted)

```bash
go build -o openbox ./cmd/openbox

# 1. run the control plane (prints a login command with a bootstrap token)
./openbox control

# 2. log in from any machine
openbox login --server http://127.0.0.1:8080 --token obx_…

# 3a. add THIS machine as a node
openbox node token --tag mac            # prints the `openbox agent …` command
openbox agent --server http://127.0.0.1:8080 --token enroll_… --tag mac

# 3b. …or bootstrap a REMOTE machine over SSH (installs + enrolls it for you)
openbox node add --host user@1.2.3.4 --password '…' --tag gpu
openbox node add --host user@1.2.3.4 --key ~/.ssh/id_ed25519 --tag lab

# 4. run things
openbox nodes
openbox -t mac uname -a
```

Open the control-plane URL in a browser for the **dashboard**: a live map of your
fleet (online/offline, tags, platform) with filter and auto-refresh, active sessions,
and an add-node flow. Click any node for a detail drawer where you can:

- rename it and edit its tags inline,
- run commands in a **live web console** (output streams back over SSE),
- remove it from the fleet.

The web console is the one deliberate, scoped exception to "control plane not in the
data path": for a browser-initiated command the control plane mints itself an
ephemeral user cert (acting as you) and proxies the connection to the node, so the
node's auth is unchanged. It needs the control plane to be able to reach the node —
run `openbox control --mesh …` if your nodes are on the overlay.

## CLI

| command | what |
|---|---|
| `openbox login --server URL --token T` | authenticate this machine |
| `openbox whoami` | show the logged-in user |
| `openbox control [--addr --url --db --ca]` | run the self-hosted control plane |
| `openbox agent [--server --token --addr --tag --name]` | run the node daemon |
| `openbox node token [--tag …]` | mint a node enrollment token |
| `openbox node add --host user@ip [--password\|--key] [--tag …]` | bootstrap a remote node over SSH |
| `openbox nodes [--tag t]` | list your nodes |
| `openbox -t TAG [-s SID] [--docker IMG] <cmd…>` | run a command on a node |

### Targeting & sessions

- `-t, --tag` — pick any node carrying a tag (prefers online nodes).
- `-n, --node` — pick an exact node id.
- `-s, --session` — bind to a persistent shell. The first use picks a node; every
  later command with the same id lands on that node, in that shell, in order, with
  cwd/env preserved.

### Isolation

The default is native (a dedicated shell on the host — lightweight). Opt up per call:

- `--docker IMAGE` — run inside a container (sessions keep one container alive across
  commands).
- `--nspawn DIR` — run inside a systemd-nspawn container (Linux).
- `--isolate native|docker:img|nspawn:dir` — explicit form.

### Mesh (peer-to-peer over NAT)

By default the transport is plain TCP (LAN / same-host). Add `--mesh` to put the
agent — and the CLI — on an embedded Tailscale overlay, so nodes and laptops reach
each other directly across NATs with no port forwarding:

```bash
# agent joins the tailnet and advertises its overlay address to the control plane
openbox agent --mesh --mesh-control https://headscale.example.com \
  --mesh-authkey tskey-… --tag mac

# the CLI joins too (env so it composes with the `-t … <cmd>` shorthand)
export OPENBOX_MESH=1 OPENBOX_MESH_CONTROL=https://headscale.example.com OPENBOX_MESH_AUTHKEY=tskey-…
openbox -t mac uname -a            # dialed peer-to-peer over WireGuard
```

Works with Tailscale or self-hosted **Headscale** (verified end-to-end against
Headscale). The agent advertises its `100.64.x.x` tailnet address; the CLI dials it
directly. Note: the CLI brings up a tailnet node per invocation (a few seconds cold);
a future local `openboxd` will hold the connection to make this instant.

## Layout

```
cmd/openbox            CLI + agent entry (one binary)
internal/transport     network substrate behind an interface (TCP today, tsnet next)
internal/ca            SSH certificate authority
internal/certauth      mutual cert verification (agent ⇄ client)  [tested]
internal/control       control-plane HTTP service + dashboard
internal/store         sqlite registry / sessions (pure-Go driver)
internal/agent         node daemon: register, serve, heartbeat, exec
internal/session       persistent shell sessions                  [tested]
internal/isolation     native / docker / nspawn backends          [tested]
internal/sshexec       shared SSH exec core (CLI + web console)
internal/bootstrap     remote SSH enrollment (methods 1 & 2)
internal/cpclient      control-plane HTTP client
internal/config        on-disk CLI + agent state
internal/api           shared wire types
```

## Status

Working and tested end-to-end: control plane, SSH CA, login, enrollment (token +
remote SSH bootstrap), node registry with heartbeats, tag/session resolution, mutual
cert auth, persistent sessions, isolation tiers (docker verified live), the embedded
**Tailscale mesh** (verified against Headscale), and the web dashboard.

**Hardening still open** (marked `TODO` in code): authenticate node heartbeats with a
per-node token, and pin the remote host key (`known_hosts`) during SSH bootstrap. A
future local `openboxd` daemon would hold the mesh connection so the CLI is instant
rather than joining the tailnet per invocation.
