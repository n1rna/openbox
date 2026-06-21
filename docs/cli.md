---
title: CLI reference
description: Every openbox subcommand, its flags, and how targeting and sessions work.
---

`openbox` is a single binary that plays three roles — CLI, agent, and control
plane — selected by subcommand.

## Command summary

| Command | What it does |
|---|---|
| `openbox login --server URL --token T` | authenticate this machine |
| `openbox whoami` | show the logged-in user |
| `openbox control [flags]` | run the self-hosted control plane |
| `openbox agent [flags]` | run the node daemon |
| `openbox node token [--tag …]` | mint a node enrollment token |
| `openbox node add --host user@ip …` | bootstrap a remote node over SSH |
| `openbox nodes [--tag t]` | list your nodes |
| `openbox -t TAG [-s SID] [--docker IMG] <cmd…>` | run a command on a node |
| `openbox version` | print the version |

## Running commands

The bare form is the one you'll use most. `openbox <flags> <command…>` is
shorthand for `openbox run <flags> <command…>`:

```sh
openbox -t mac uname -a
openbox -n node_abc123 'echo $HOME'
openbox -t gpu --docker pytorch/pytorch python train.py
```

Everything after the flags is the command, passed verbatim — quote shell
metacharacters as you normally would.

### Targeting

| Flag | Selects |
|---|---|
| `-t, --tag TAG` | any node carrying `TAG` (prefers online nodes) |
| `-n, --node ID` | one exact node id |
| `-s, --session SID` | bind to a persistent shell (see below) |

### Sessions

`-s SID` binds a named session. The first command with a given id picks a node
(honoring `-t`/`-n`) and opens a persistent shell there; every later command
with the same id lands on that same node, in that same shell, in order, with cwd
and environment preserved.

```sh
openbox -t mac -s build cd /tmp/work    # picks a mac node, opens shell "build"
openbox -t mac -s build make            # same node, same shell, sees the cd
openbox -t mac -s build ./ship.sh
```

Idle sessions are reaped automatically.

### Isolation

The default is **native** — a dedicated shell on the host, lightweight. Opt up
per command:

| Flag | Runs the command… |
|---|---|
| (default) | natively on the host |
| `--docker IMAGE` | inside a container (a session keeps one container alive across commands) |
| `--nspawn DIR` | inside a systemd-nspawn container (Linux) |
| `--isolate native\|docker:img\|nspawn:dir` | explicit form |

Isolation fails **closed**: an unknown mode is rejected, never silently
downgraded to running on the host.

## `openbox control`

Runs the control plane: user auth, node registry, the SSH CA, the session
directory, and the dashboard.

| Flag | Default | Purpose |
|---|---|---|
| `--addr` | `127.0.0.1:8080` | listen address (use `0.0.0.0:8080` for LAN) |
| `--url` | `http://127.0.0.1:8080` | public base URL advertised to nodes |
| `--db` | `~/.openbox/control/openbox.db` | sqlite database path |
| `--ca` | `~/.openbox/control/ca_key` | CA private key path |
| `--mesh`, `--mesh-control`, `--mesh-authkey` | — | join the [mesh](/mesh/) so the web console can reach mesh nodes |

## `openbox agent`

Runs the node daemon: registers with the control plane, serves cert-verified
exec, and heartbeats.

| Flag | Default | Purpose |
|---|---|---|
| `--addr` | `127.0.0.1:7600` | listen/advertise address (use `0.0.0.0:7600` for LAN) |
| `--server` | `$OPENBOX_SERVER` | control-plane URL (first registration only) |
| `--token` | `$OPENBOX_ENROLL_TOKEN` | enrollment token (first registration only) |
| `--name` | hostname | node display name |
| `--tag` | — | tag to request at registration (repeatable) |
| `--mesh`, `--mesh-control`, `--mesh-authkey`, `--mesh-hostname` | — | join the [mesh](/mesh/) |

After the first registration the agent saves its identity (node id, host cert,
CA pubkey, server URL) under `~/.openbox/agent/` and runs without `--server`/`--token`.

## `openbox node add`

Bootstraps a remote node over SSH (enrollment methods 1 & 2): connects, uploads
the binary, mints an enrollment token, and launches the agent.

| Flag | Purpose |
|---|---|
| `--host user@host[:port]` | target to bootstrap (required) |
| `--password` | SSH password (method 1) |
| `--key`, `--key-pass` | SSH private key + optional passphrase (method 2) |
| `--binary` | openbox binary to upload (default: this binary; must match remote OS/arch) |
| `--agent-addr` | address the agent listens on/advertises |
| `--name`, `--tag` | node name and tags |

:::note
`--binary` must match the **remote** OS/arch. From a Mac enrolling a Linux box,
cross-build first: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o openbox-linux ./cmd/openbox`,
then pass `--binary openbox-linux`.
:::
