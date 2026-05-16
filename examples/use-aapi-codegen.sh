#!/usr/bin/env bash
# Print the absolute path to a cached aapi-codegen binary, downloading
# it from the plheide/aapi-codegen GitHub release on first use.
#
# Modelled on plheide/go-jsonschema's `use-go-jsonschema.sh` pattern —
# same shape, same caching, same env-var pinning convention. Copy this
# file into the consuming repo's scripts/ directory unchanged (or adapt
# the defaults at the top if you want a different fork or version pin).
#
# Pin the version with the AAPI_VERSION env var; default tracks the
# fork tag the schema generators in the consuming repo were tested
# against. Bump it as a single-file diff to roll forward.
#
# Usage:
#   BIN=$(/path/to/scripts/use-aapi-codegen.sh)
#   "$BIN" SPEC.asyncapi.yaml
#
# Supported platforms (OS detected via `uname -s`):
#   - Linux  / WSL          (release archive: .tar.gz, binary: aapi-codegen)
#   - Darwin / macOS        (release archive: .tar.gz, binary: aapi-codegen)
#   - Windows / Git Bash    (release archive: .zip,    binary: aapi-codegen.exe)
# Git Bash on Windows reports `uname -s` as `MINGW64_NT-*` etc; we
# normalise back to "Windows" to match goreleaser's archive naming.
#
# The downloaded archive is cached at
# $HOME/.cache/aapi-codegen/<version>/ so subsequent invocations are
# offline.
set -euo pipefail

AAPI_VERSION="${AAPI_VERSION:-v0.1.0}"
AAPI_REPO="${AAPI_REPO:-plheide/aapi-codegen}"
AAPI_CACHE="${AAPI_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/aapi-codegen/${AAPI_VERSION}}"

# Map host platform to the release-asset naming (matches the
# .goreleaser.yaml archive name_template + format_overrides — keep in
# sync if either side changes).
case "$(uname -s)" in
    Linux)
        os="Linux"; ext="tar.gz"; bin_suffix=""
        ;;
    Darwin)
        os="Darwin"; ext="tar.gz"; bin_suffix=""
        ;;
    MINGW64_NT-*|MSYS_NT-*|CYGWIN_NT-*)
        os="Windows"; ext="zip"; bin_suffix=".exe"
        ;;
    *)
        echo "use-aapi-codegen: unsupported OS $(uname -s)" >&2
        exit 1
        ;;
esac

case "$(uname -m)" in
    x86_64|amd64)  arch="x86_64" ;;
    aarch64|arm64) arch="arm64" ;;
    *)             echo "use-aapi-codegen: unsupported arch $(uname -m) — release archives are amd64/arm64 only" >&2; exit 1 ;;
esac

BIN="${AAPI_CACHE}/aapi-codegen${bin_suffix}"
if [ -x "$BIN" ]; then
    echo "$BIN"
    exit 0
fi

asset="aapi-codegen_${os}_${arch}.${ext}"
url="https://github.com/${AAPI_REPO}/releases/download/${AAPI_VERSION}/${asset}"

# Atomic install via staging dir: download + extract into a sibling of
# the final cache dir, then rename into place. Two concurrent first-
# time invocations (common when `go generate ./...` parallelises
# schema dirs) both download into separate stagings; whichever finishes
# rename first wins, the other sees its rename fail because the target
# now exists and falls back to using the already-cached binary.
#
# Without this, both processes raced on `tar -xz -C $AAPI_CACHE` and
# one could observe the other's half-extracted bytes.
mkdir -p "$(dirname "$AAPI_CACHE")"
staging=$(mktemp -d "${AAPI_CACHE}.staging.XXXXXX")
trap 'rm -rf "$staging"' EXIT

echo "use-aapi-codegen: fetching ${asset} from ${AAPI_REPO}@${AAPI_VERSION}" >&2
if ! curl --silent --show-error --fail --location "$url" \
        --output "${staging}/${asset}"; then
    echo "use-aapi-codegen: download failed from $url" >&2
    echo "use-aapi-codegen:   - verify AAPI_VERSION ($AAPI_VERSION) names a published release on ${AAPI_REPO}" >&2
    echo "use-aapi-codegen:   - override with AAPI_VERSION=<existing-tag> if needed" >&2
    exit 1
fi
case "$ext" in
    tar.gz)
        tar -xzf "${staging}/${asset}" -C "$staging"
        ;;
    zip)
        # `unzip` is included in Git Bash by default and is the
        # conventional .zip extractor on POSIX-ish environments.
        if ! command -v unzip >/dev/null 2>&1; then
            echo "use-aapi-codegen: 'unzip' not found on PATH — required for Windows .zip archives" >&2
            exit 1
        fi
        unzip -q "${staging}/${asset}" -d "$staging"
        ;;
    *)
        echo "use-aapi-codegen: unhandled archive extension $ext" >&2
        exit 1
        ;;
esac
rm -f "${staging}/${asset}"

# Atomic rename. On the same filesystem this is a single inode swap,
# which is concurrency-safe. If another process won the race the
# directory is already in place and `mv` errors; in that case we trust
# the existing cache content.
if ! mv "$staging" "$AAPI_CACHE" 2>/dev/null; then
    rm -rf "$staging"
fi

if [ ! -x "$BIN" ]; then
    echo "use-aapi-codegen: cache install completed but $BIN is missing or not executable" >&2
    exit 1
fi

echo "$BIN"
