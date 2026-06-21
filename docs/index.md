---
title: openbox
description: Personal box manager — control any Linux or Mac server you can reach, run commands and containers on it, and let AI agents drive a whole fleet from any laptop.
template: splash
hero:
  tagline: Authenticate once, then <code>openbox -t mac make build</code> just works — on any machine in your fleet, from any laptop.
  actions:
    - text: Quick start
      link: /quick-start/
      icon: right-arrow
      variant: primary
    - text: View on GitHub
      link: https://github.com/n1rna/openbox
      icon: external
---

## What is openbox

openbox turns the machines you already have — a GPU box, a couple of Mac minis
for builds, a Raspberry Pi wired to some hardware — into a single fleet you can
drive from anywhere. Tag a node, then target it by tag:

```sh
openbox -t mac  uname -a                 # run on any node tagged "mac"
openbox -t gpu  --docker pytorch/pytorch python train.py
openbox -t mac -s build cd /tmp/work     # persistent session: these three…
openbox -t mac -s build make             # …share cwd + env, run in order…
openbox -t mac -s build ./ship.sh        # …in the same shell on the same node
```

It's built for AI agents as much as humans: a coding agent can fan a build out
across a tagged pool, hold a working directory open across steps, and isolate
risky work in a container — all through one small CLI.

## How it works

openbox is one Go binary that plays three roles: a thin **control plane** you
self-host, an **agent** on every node, and the **CLI** you run anywhere.

```text
                        ┌──────────────────────────┐
   openbox login  ───►  │   control plane (thin)   │   you self-host this
                        │  · user auth / tokens    │
                        │  · node registry + tags  │
                        │  · SSH certificate CA     │
                        │  · session directory     │
                        │  · web dashboard          │
                        └────────────┬─────────────┘
                                     │ brokers identity, then steps aside
            ┌────────────────────────┴───────────┐
       ┌────▼─────┐   peer-to-peer, mutually  ┌───▼──────┐
       │ openbox  │   cert-verified SSH       │ openbox  │
       │  (CLI)   │ ────────────────────────► │  agent   │  on each node
       └──────────┘   (no control plane in    └──────────┘
                       the data path)
```

The control plane never sees your command traffic. It hands the CLI a
short-lived SSH **user certificate** and tells it which node to reach; the CLI
then talks straight to the node's agent. The node trusts the cert because the
**openbox CA** signed it, and the CLI verifies the node's **host certificate**
the same way — the Teleport / Netflix-BLESS model: one CA, ephemeral certs, no
static key distribution.

## Why you might want it

- **Simpler than Kubernetes.** No cluster, no YAML, no control-plane-in-the-data-path.
  Native execution by default; opt up to Docker or systemd-nspawn per command.
- **Reach across NATs.** An embedded Tailscale/Headscale mesh lets laptops and
  nodes find each other directly, with no port forwarding. See [Mesh networking](/mesh/).
- **Tags + sessions.** Target a pool by tag; bind a session id to keep a shell
  (cwd + env) alive across commands on one node.
- **A real dashboard.** A live map of your fleet — online/offline, tags, platform —
  with a web console that streams command output back over SSE.

## Next steps

- [Install](/install/) — one curl command, or a prebuilt binary.
- [Quick start](/quick-start/) — stand up a control plane and enroll your first node.
- [CLI reference](/cli/) — every command and flag.
- [Architecture](/architecture/) — the trust model and data path in depth.
