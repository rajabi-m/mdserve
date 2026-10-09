#!/bin/sh
# Install mdserve from GitHub Releases.
#
# Download and inspect a pinned installer before running it; see README.md.
#
# Options (environment):
#   MDSERVE_VERSION   tag to install (default: latest release)
#   MDSERVE_BINDIR    install dir     (default: ~/.local/bin)
set -eu

REPO="mostafa-re/mdserve"
BINDIR="${MDSERVE_BINDIR:-$HOME/.local/bin}"
fail() { echo "mdserve: $*" >&2; exit 1; }
case "$BINDIR" in /*) ;; *) fail "install directory must be absolute" ;; esac
case "$BINDIR" in *:*) fail "install directory must not contain a PATH separator" ;; esac
# Quote shell configuration as data; no custom path is evaluated as shell code.
posix_quote() { printf "'"; printf '%s' "$1" | sed "s/'/'\\\\''/g"; printf "'"; }
fish_quote() { printf "'"; printf '%s' "$1" | sed "s/\\\\/\\\\\\\\/g; s/'/\\\\'/g"; printf "'"; }
fetch() { curl --proto '=https' --proto-redir '=https' --connect-timeout 15 --max-time 120 --max-filesize 67108864 -fSL "$1" -o "$2"; }

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$os" in
  linux)  os=linux ;;
  darwin) os=darwin ;;
  *) echo "mdserve: unsupported OS '$os' — see https://github.com/$REPO/releases" >&2; exit 1 ;;
esac
case "$arch" in
  x86_64|amd64)  arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "mdserve: unsupported architecture '$arch'" >&2; exit 1 ;;
esac

ver="${MDSERVE_VERSION:-}"
tmp="$(mktemp -d)"
staged=
trap 'rm -rf "$tmp"; [ -z "$staged" ] || rm -f "$staged"' 0
trap 'exit 1' HUP INT TERM
if [ -z "$ver" ]; then
  fetch "https://api.github.com/repos/$REPO/releases/latest" "$tmp/latest.json"
  ver="$(sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' "$tmp/latest.json" | head -n1)"
fi
[ "${#ver}" -le 128 ] || fail "invalid release version"
[ "$(printf '%s' "$ver" | tr -d '\n\r')" = "$ver" ] || fail "invalid release version"
printf '%s\n' "$ver" | LC_ALL=C grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9]+([.-][A-Za-z0-9]+)*)?$' || fail "invalid release version"

asset="mdserve_${ver}_${os}_${arch}.tar.gz"
url="https://github.com/$REPO/releases/download/$ver/$asset"

echo "mdserve: downloading $ver ($os/$arch)"
fetch "$url.sha256" "$tmp/checksum"
fetch "$url" "$tmp/archive"
# Exactly one sha256sum record for this asset, using text or binary mode.
[ "$(wc -c < "$tmp/checksum" | tr -d ' ')" -le 4096 ] || fail "oversized checksum"
expected=$(LC_ALL=C awk -v asset="$asset" '
 NR == 1 && length($1) == 64 && $1 !~ /[^0-9a-fA-F]/ &&
 (substr($0,65,2) == "  " || substr($0,65,2) == " *") &&
 substr($0,67) == asset { digest=tolower($1); next }
 { bad=1 }
 END { if (NR != 1 || bad || digest == "") exit 1; print digest }
' "$tmp/checksum") || fail "invalid release checksum record"
if command -v sha256sum >/dev/null 2>&1; then
 actual=$(sha256sum "$tmp/archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
 actual=$(shasum -a 256 "$tmp/archive" | awk '{print $1}')
else
 fail "SHA-256 tool required (sha256sum or shasum)"
fi
[ "$actual" = "$expected" ] || fail "release checksum mismatch"
# Inspect first; never extract paths into the filesystem. Copy only the expected
# regular entry to a staged file, so archive links cannot affect the destination.
tar -tzf "$tmp/archive" > "$tmp/names" || fail "invalid release archive"
[ "$(cat "$tmp/names")" = mdserve ] && [ "$(wc -l < "$tmp/names" | tr -d ' ')" = 1 ] || fail "unexpected archive entries"
tar -tvzf "$tmp/archive" > "$tmp/types" || fail "invalid release archive"
[ "$(wc -l < "$tmp/types" | tr -d ' ')" = 1 ] || fail "unexpected archive entries"
case "$(cat "$tmp/types")" in -*) ;; *) fail "release binary must be a regular file" ;; esac
mkdir -p "$BINDIR"
staged=$(mktemp "$BINDIR/.mdserve-install.XXXXXX")
(ulimit -f 262144; tar -xOzf "$tmp/archive" mdserve > "$staged") || fail "could not extract release binary"
[ -s "$staged" ] || fail "empty release binary"
chmod 755 "$staged"
# The staged file is on the destination filesystem, making replacement atomic.
[ ! -d "$BINDIR/mdserve" ] || fail "existing executable is a directory"
mv -f "$staged" "$BINDIR/mdserve"
staged=

echo "mdserve: installed $ver to $BINDIR/mdserve"

# Put BINDIR on PATH. ~/.local/bin is a user directory, so this only edits your
# shell rc file — it never needs root. Opt out with MDSERVE_NO_MODIFY_PATH=1.
case ":$PATH:" in
  *":$BINDIR:"*) exit 0 ;;        # already on PATH
esac

if [ -n "${MDSERVE_NO_MODIFY_PATH:-}" ]; then
  printf 'mdserve: add to PATH: export PATH=%s:"$PATH"\n' "$(posix_quote "$BINDIR")"
  exit 0
fi

case "$(basename "${SHELL:-sh}")" in
  zsh)  rc="$HOME/.zshrc";                   line="export PATH=$(posix_quote "$BINDIR"):\"\$PATH\"" ;;
  # macOS bash login shells read .bash_profile, not .bashrc
  bash) [ "$os" = darwin ] && rc="$HOME/.bash_profile" || rc="$HOME/.bashrc"
        line="export PATH=$(posix_quote "$BINDIR"):\"\$PATH\"" ;;
  fish) rc="$HOME/.config/fish/config.fish"; line="fish_add_path $(fish_quote "$BINDIR")" ;;
  *)    rc="$HOME/.profile";                 line="export PATH=$(posix_quote "$BINDIR"):\"\$PATH\"" ;;
esac

# Editing the rc must never fail the install — the binary is already in place.
manual="mdserve: add to PATH manually: $line"
if ! { mkdir -p "$(dirname "$rc")" && touch "$rc"; } 2>/dev/null; then
  echo "$manual"
elif grep -qF "$BINDIR" "$rc" 2>/dev/null; then
  echo "mdserve: $BINDIR already configured in $rc"
elif printf '\n# added by mdserve installer\n%s\n' "$line" >> "$rc" 2>/dev/null; then
  echo "mdserve: added $BINDIR to PATH in $rc"
  echo "mdserve: restart your shell or run:  source $rc"
else
  echo "$manual"
fi
