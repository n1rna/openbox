#!/bin/sh
# openbox installer.
#
#   curl -fsSL https://openbox.n1rna.net/install.sh | sh
#
# Downloads the right prebuilt binary for your OS/arch from the latest GitHub
# release, verifies its SHA-256, and installs it onto your PATH. No build
# toolchain required.
#
# Environment overrides:
#   OPENBOX_VERSION   install a specific version (e.g. v0.1.0). Default: latest.
#   OPENBOX_BIN_DIR   install directory. Default: /usr/local/bin if writable,
#                     else $HOME/.local/bin.
#   OPENBOX_REPO      owner/repo to pull from. Default: n1rna/openbox.
set -eu

REPO="${OPENBOX_REPO:-n1rna/openbox}"
BIN="openbox"

info()  { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
warn()  { printf '\033[1;33mwarn:\033[0m %s\n' "$*" >&2; }
err()   { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

# --- detect platform -------------------------------------------------------
os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux)  os=linux ;;
  darwin) os=darwin ;;
  *) err "unsupported OS: $os (openbox ships linux and darwin binaries)" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64)   arch=amd64 ;;
  aarch64|arm64)  arch=arm64 ;;
  *) err "unsupported architecture: $arch" ;;
esac

target="${os}-${arch}"

# --- tooling ---------------------------------------------------------------
if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1"; }
  download() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO- "$1"; }
  download() { wget -qO "$2" "$1"; }
else
  err "need curl or wget"
fi

# --- resolve version -------------------------------------------------------
version="${OPENBOX_VERSION:-}"
if [ -z "$version" ]; then
  info "resolving latest release of $REPO"
  version=$(fetch "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | head -n1 | cut -d'"' -f4)
  [ -n "$version" ] || err "could not determine latest version (set OPENBOX_VERSION)"
fi

asset="${BIN}-${target}.tar.gz"
base="https://github.com/${REPO}/releases/download/${version}"
info "installing openbox $version ($target)"

# --- download + verify -----------------------------------------------------
tmp=$(mktemp -d 2>/dev/null || mktemp -d -t openbox)
trap 'rm -rf "$tmp"' EXIT

download "${base}/${asset}"        "${tmp}/${asset}"        || err "download failed: ${base}/${asset}"
download "${base}/${asset}.sha256" "${tmp}/${asset}.sha256" || warn "no checksum sidecar; skipping verification"

if [ -f "${tmp}/${asset}.sha256" ]; then
  expected=$(cut -d' ' -f1 < "${tmp}/${asset}.sha256")
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "${tmp}/${asset}" | cut -d' ' -f1)
  elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "${tmp}/${asset}" | cut -d' ' -f1)
  else
    actual=""; warn "no sha256 tool; skipping verification"
  fi
  if [ -n "$actual" ] && [ "$actual" != "$expected" ]; then
    err "checksum mismatch: expected $expected, got $actual"
  fi
  [ -n "$actual" ] && info "checksum ok"
fi

tar -C "$tmp" -xzf "${tmp}/${asset}" || err "extract failed"
[ -f "${tmp}/${BIN}" ] || err "archive did not contain '${BIN}'"
chmod +x "${tmp}/${BIN}"

# --- choose install dir ----------------------------------------------------
bindir="${OPENBOX_BIN_DIR:-}"
if [ -z "$bindir" ]; then
  if [ -w /usr/local/bin ] 2>/dev/null; then
    bindir=/usr/local/bin
  else
    bindir="${HOME}/.local/bin"
  fi
fi
mkdir -p "$bindir" 2>/dev/null || err "cannot create $bindir"

if mv "${tmp}/${BIN}" "${bindir}/${BIN}" 2>/dev/null; then
  :
elif command -v sudo >/dev/null 2>&1 && [ "$bindir" = /usr/local/bin ]; then
  info "elevating to install into $bindir"
  sudo mv "${tmp}/${BIN}" "${bindir}/${BIN}" || err "install failed"
else
  err "cannot write to $bindir (set OPENBOX_BIN_DIR to a writable dir)"
fi

info "installed ${bindir}/${BIN}"

# --- PATH guidance ---------------------------------------------------------
case ":${PATH}:" in
  *":${bindir}:"*) ;;
  *) warn "${bindir} is not on your PATH. Add it:"
     printf '       export PATH="%s:$PATH"\n' "$bindir" >&2 ;;
esac

"${bindir}/${BIN}" version 2>/dev/null || true
cat <<EOF

Next steps:
  openbox login --server <control-plane-url> --token <token>
  openbox nodes
  openbox -t <tag> <command>

Docs: https://openbox.n1rna.net
EOF
