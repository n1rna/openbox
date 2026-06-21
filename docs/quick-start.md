---
title: Quick start
description: Stand up a control plane, enroll your first node, and run a command on it.
---

This walks you from nothing to running a command on a remote node. You'll need
[openbox installed](/install/) on at least the machine running the control plane.

## 1. Run the control plane

The control plane is self-hosted. Start it; on first run it prints a login
command with a bootstrap token.

```sh
openbox control
```

For a control plane that nodes on your LAN can reach, bind it to all interfaces
and advertise your LAN address (the `--url` is what nodes phone home to):

```sh
openbox control --addr 0.0.0.0:8080 --url http://192.168.1.10:8080
```

Open the `--url` in a browser for the [dashboard](/architecture/#the-dashboard).

## 2. Log in

From any machine, using the token the control plane printed:

```sh
openbox login --server http://192.168.1.10:8080 --token obx_…
openbox whoami
```

## 3. Enroll a node

There are three ways to add a node.

### A — this machine, with a token

Mint an enrollment token, then run the agent on the box you want to add:

```sh
openbox node token --tag mac
# prints:  openbox agent --server … --token enroll_… --tag mac
openbox agent --server http://192.168.1.10:8080 --token enroll_… --tag mac
```

### B — a remote machine, over SSH

openbox SSHes in, uploads the agent binary, and launches it for you. Use a
password or a key:

```sh
openbox node add --host user@1.2.3.4 --password '…'                --tag gpu
openbox node add --host user@1.2.3.4 --key ~/.ssh/id_ed25519       --tag lab
```

After this one-time bootstrap the node manages its own identity — the
password/key is never needed again.

### Keep the agent running

For a node that should survive reboots, run the agent under your init system.
A systemd **user** service (no root required) works well:

```ini
# ~/.config/systemd/user/openbox-agent.service
[Unit]
Description=openbox agent
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%h/.openbox/bin/openbox agent --addr 0.0.0.0:7600 --tag mac
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
```

```sh
systemctl --user enable --now openbox-agent.service
loginctl enable-linger "$USER"   # start at boot without an interactive login
```

## 4. Run things

```sh
openbox nodes                          # list the fleet
openbox -t mac uname -a                # run on any node tagged "mac"
openbox -t gpu --docker pytorch/pytorch python train.py
```

### Persistent sessions

Bind a session id to keep one shell alive across commands — cwd and environment
persist, and every command with the same id lands on the same node:

```sh
openbox -t mac -s build cd /tmp/work
openbox -t mac -s build make
openbox -t mac -s build ./ship.sh
```

That's the whole loop. From here, see the [CLI reference](/cli/) for every flag,
or [Architecture](/architecture/) for how the trust model works.
