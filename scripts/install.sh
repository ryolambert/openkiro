#!/usr/bin/env bash
# install.sh — install openkiro on macOS/Linux
set -euo pipefail

MIN_MAJOR=1
MIN_MINOR=23
PKG="github.com/ryolambert/openkiro@latest"

# ── Go detection / install ────────────────────────────────────────────────────
if ! command -v go &>/dev/null; then
    if command -v brew &>/dev/null; then
        echo "Go not found — installing via Homebrew..."
        brew install go
    else
        echo "Error: Go not found. Install from https://go.dev/dl/" >&2
        exit 1
    fi
fi

# ── Version check ─────────────────────────────────────────────────────────────
go_ver=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | head -1 | tr -d 'go')
go_major=${go_ver%%.*}
go_minor=${go_ver##*.}
if [ "${go_major}" -lt "${MIN_MAJOR}" ] || \
   { [ "${go_major}" -eq "${MIN_MAJOR}" ] && [ "${go_minor}" -lt "${MIN_MINOR}" ]; }; then
    echo "Error: Go >= ${MIN_MAJOR}.${MIN_MINOR} required (found ${go_ver})" >&2
    exit 1
fi

# ── Install ───────────────────────────────────────────────────────────────────
echo "Installing ${PKG}..."
go install "${PKG}"

# ── PATH setup ────────────────────────────────────────────────────────────────
GOBIN="$(go env GOPATH)/bin"
SHELL_NAME="$(basename "${SHELL:-bash}")"
case "${SHELL_NAME}" in
    zsh)  RC="${HOME}/.zshrc" ;;
    bash) RC="${HOME}/.bashrc" ;;
    *)    RC="${HOME}/.profile" ;;
esac

if [[ ":${PATH}:" != *":${GOBIN}:"* ]]; then
    if ! grep -qF "${GOBIN}" "${RC}" 2>/dev/null; then
        echo "" >> "${RC}"
        echo "export PATH=\"${GOBIN}:\$PATH\"" >> "${RC}"
        echo "Added ${GOBIN} to PATH in ${RC}"
    fi
    export PATH="${GOBIN}:${PATH}"
fi

echo "openkiro installed successfully."
echo "Reload your shell or run: source ${RC}"
