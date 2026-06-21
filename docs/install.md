---
title: Install
description: Install openbox with one curl command, a prebuilt release binary, or from source.
---

openbox is a single static Go binary — no runtime dependencies. The same binary
is the CLI, the agent, and the control plane.

## One-line install

```sh
curl -fsSL https://docs.opbx.net/install.sh | sh
```

The script detects your OS and architecture, downloads the matching binary from
the latest [GitHub release](https://github.com/n1rna/openbox/releases), verifies
its SHA-256, and installs it onto your `PATH` (`/usr/local/bin`, or
`~/.local/bin` if that isn't writable).

Override the defaults with environment variables:

```sh
# pin a version
OPENBOX_VERSION=v0.1.0 curl -fsSL https://docs.opbx.net/install.sh | sh

# install somewhere else
OPENBOX_BIN_DIR=~/bin   curl -fsSL https://docs.opbx.net/install.sh | sh
```

| Variable | Default | Purpose |
|---|---|---|
| `OPENBOX_VERSION` | latest release | install a specific tag (e.g. `v0.1.0`) |
| `OPENBOX_BIN_DIR` | `/usr/local/bin` or `~/.local/bin` | install directory |
| `OPENBOX_REPO` | `n1rna/openbox` | source repository |

## Prebuilt binaries

Every release attaches a tarball + `.sha256` per platform. Grab one directly:

```sh
curl -fsSL https://github.com/n1rna/openbox/releases/latest/download/openbox-linux-amd64.tar.gz \
  | tar -xz -C /usr/local/bin
openbox version
```

| Platform | Asset |
|---|---|
| Linux x86_64  | `openbox-linux-amd64.tar.gz` |
| Linux arm64   | `openbox-linux-arm64.tar.gz` |
| macOS Intel   | `openbox-darwin-amd64.tar.gz` |
| macOS Apple silicon | `openbox-darwin-arm64.tar.gz` |

## From source

Requires Go (see `go.mod` for the version).

```sh
git clone https://github.com/n1rna/openbox
cd openbox
make build          # produces ./openbox, version stamped from git describe
./openbox version
```

`make dist` cross-compiles the full set of release tarballs into `dist/`.

## macOS note

On macOS Sequoia/Tahoe and later, the system **Local Network Privacy** control
can block an unsigned CLI from connecting to nodes on your LAN (you'll see
`no route to host` even though `ssh`/`ping` work). If you hit this, grant your
terminal app access under *System Settings → Privacy & Security → Local Network*,
or drive nodes over the [mesh](/mesh/).
