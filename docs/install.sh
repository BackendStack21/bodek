#!/bin/sh
# bodek installer — downloads prebuilt release binaries from GitHub.
#
# Usage:
#   curl -fsSL https://bodek.21no.de/install.sh | sh
#   curl -fsSL https://bodek.21no.de/install.sh | sh -s -- --with-odek
#   sh install.sh [--with-odek]     # install odek without asking
#
# macOS and Linux. Windows users: grab a binary from the releases page.
#
# Installs bodek (and optionally odek) for the current platform into
# ~/.local/bin (or /usr/local/bin when writable). Verifies every download
# against the release checksums.txt when a sha256 tool is available.
set -eu

BODEK_REPO="BackendStack21/bodek"
ODEK_REPO="BackendStack21/odek"
GH_API="https://api.github.com/repos"
GH_DL="https://github.com"

say()  { printf '==> %s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------- platform
OS=$(uname -s)
ARCH=$(uname -m)
case "$OS" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  *) die "unsupported OS '$OS' — this installer covers macOS and Linux." ;;
esac
case "$ARCH" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported architecture '$ARCH'." ;;
esac

command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 \
  || die "need curl or wget to download."

fetch() { # fetch <url> -> stdout
  if command -v curl >/dev/null 2>&1; then curl -fsSL "$1"
  else wget -qO- "$1"; fi
}
download() { # download <url> <dest>
  if command -v curl >/dev/null 2>&1; then curl -fsSL -o "$2" "$1"
  else wget -qO "$2" "$1"; fi
}

checksum() { # checksum <file> <expected-hex>
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$1" | awk '{print $1}')
  elif command -v shasum >/dev/null 2>&1; then
    actual=$(shasum -a 256 "$1" | awk '{print $1}')
  else
    warn "no sha256 tool found — skipping checksum verification."
    return 0
  fi
  [ "$actual" = "$2" ] || die "checksum mismatch for $(basename "$1") — download corrupted or tampered. Aborting."
}

# ---------------------------------------------------------------- install dir
install_dir=""
pick_install_dir() {
  for d in /usr/local/bin "$HOME/.local/bin"; do
    if mkdir -p "$d" 2>/dev/null && [ -w "$d" ]; then install_dir=$d; return; fi
  done
  die "no writable install directory found (tried /usr/local/bin, ~/.local/bin)."
}

path_has_dir() { case ":$PATH:" in *":$1:"*) return 0 ;; *) return 1 ;; esac; }

# ---------------------------------------------------------------- releases
latest_tag() { # latest_tag <repo> -> "vX.Y.Z" (empty on failure)
  # Resolve via the redirect of /releases/latest — no API rate limits.
  url="$GH_DL/$1/releases/latest"
  if command -v curl >/dev/null 2>&1; then
    loc=$(curl -fsSI -o /dev/null -w '%{redirect_url}' "$url" 2>/dev/null || true)
  else
    loc=$(wget -qS --spider "$url" 2>&1 | sed -n 's/^ *Location: *//p' | tail -1)
  fi
  case "$loc" in
    */tag/*) printf '%s\n' "${loc##*/}" ;;
    *) printf '' ;;
  esac
}

checksum_line() { # checksum_line <repo> <tag> <asset> -> hex digest or ""
  fetch "$GH_DL/$1/releases/download/$2/checksums.txt" 2>/dev/null \
    | awk -v a="$3" '$2 == a {print $1; exit}'
}

TMP=$(mktemp -d) || die "cannot create temp dir."
trap 'rm -rf "$TMP"' EXIT INT TERM

install_asset() { # install_asset <repo> <tag> <asset-file> <binary-name>
  repo=$1 tag=$2 asset=$3 bin=$4
  say "Downloading $asset ($tag)"
  download "$GH_DL/$repo/releases/download/$tag/$asset" "$TMP/$asset"
  digest=$(checksum_line "$repo" "$tag" "$asset")
  if [ -n "$digest" ]; then
    checksum "$TMP/$asset" "$digest"
    say "Checksum verified."
  else
    # Fail closed: a missing/unfetchable checksum line is suspicious, not a
    # reason to install unverified code.
    die "no checksum entry for $asset — refusing to install unverified."
  fi
  case "$asset" in
    *.tar.gz) tar -xzf "$TMP/$asset" -C "$TMP" "$bin" 2>/dev/null \
                || tar -xzf "$TMP/$asset" -C "$TMP" ;;
    *) cp "$TMP/$asset" "$TMP/$bin" && chmod +x "$TMP/$bin" ;;
  esac
  [ -f "$TMP/$bin" ] || die "binary '$bin' not found in archive."
  mv -f "$TMP/$bin" "$install_dir/$bin"
  chmod +x "$install_dir/$bin"
  say "Installed $install_dir/$bin"
}

# ---------------------------------------------------------------- odek
install_odek() {
  tag=$(latest_tag "$ODEK_REPO")
  [ -n "$tag" ] || die "cannot resolve the latest odek release (network or GitHub problem — install curl if it is missing)."
  install_asset "$ODEK_REPO" "$tag" "odek-$os-$arch" odek
  cat <<EOF

Odek is installed. Next, set up your provider:

  1. odek init                      # creates ~/.odek/config.json
  2. Add your provider API key, e.g.:
       "providers": { "openai": { "apiKey": "sk-..." } }
     Full guide: https://github.com/BackendStack21/odek/blob/main/GETTING_STARTED.md
EOF
}

maybe_install_odek() {
  if command -v odek >/dev/null 2>&1; then
    say "Odek found: $(command -v odek) — skipping."
    return
  fi
  printf 'Odek (the engine bodek drives) is not installed.\n'
  if [ "${1:-}" = "--with-odek" ]; then
    install_odek
    return
  fi
  printf 'Install it now from prebuilt binaries? [y/N] '
  # Never read the script's own stdin: under `curl | sh` that would swallow
  # the rest of the script. Use the terminal, else default to No.
  answer=""
  if [ -t 0 ]; then
    read -r answer
  elif [ -t 1 ] && [ -r /dev/tty ]; then
    read -r answer </dev/tty || answer=""
  fi
  case "$answer" in
    y|Y|yes|YES) install_odek ;;
    *) warn "skipping odek — bodek needs a running 'odek serve' to connect to.
To install it later, rerun with: sh install.sh --with-odek" ;;
  esac
}

# ---------------------------------------------------------------- main
WITH_ODEK=""
for arg in "$@"; do
  case "$arg" in
    --with-odek) WITH_ODEK=1 ;;
    -h|--help) if [ -f "$0" ]; then sed -n '2,8p' "$0"; else
                 echo 'usage: sh install.sh [--with-odek]'; fi
               exit 0 ;;
    *) die "unknown option '$arg' (supported: --with-odek)" ;;
  esac
done

pick_install_dir

tag=$(latest_tag "$BODEK_REPO")
[ -n "$tag" ] || die "cannot resolve the latest bodek release (network or GitHub problem — install curl if it is missing)."
ver=$(printf '%s\n' "$tag" | sed 's/^v//')
install_asset "$BODEK_REPO" "$tag" "bodek_${ver}_${os}_${arch}.tar.gz" bodek

if [ -n "$WITH_ODEK" ]; then
  maybe_install_odek --with-odek
else
  maybe_install_odek
fi

if ! path_has_dir "$install_dir"; then
  cat <<EOF

NOTE: $install_dir is not on your PATH. Add it:

  echo 'export PATH="$install_dir:\$PATH"' >> ~/.profile && source ~/.profile
EOF
fi

cat <<EOF

Done. Start bodek in any project directory:

  bodek

It will launch (or attach to) an 'odek serve' engine automatically.
Docs: https://github.com/BackendStack21/bodek
EOF
