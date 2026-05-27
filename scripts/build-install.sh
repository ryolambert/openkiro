#!/usr/bin/env bash
# build-install.sh — build openkiro from source and install to /usr/local/bin
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BINARY="openkiro"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# ── Build ─────────────────────────────────────────────────────────────────────
echo "Building ${BINARY}..."
cd "${REPO_ROOT}"
make build

# ── Install ───────────────────────────────────────────────────────────────────
echo "Installing to ${INSTALL_DIR}/${BINARY} (requires sudo)..."
sudo cp -f "bin/${BINARY}" "${INSTALL_DIR}/${BINARY}"

# ── macOS: ad-hoc codesign ────────────────────────────────────────────────────
# macOS AppleSystemPolicy kills unsigned binaries copied to system paths.
# Ad-hoc signing satisfies the policy for locally-built binaries.
if [[ "$(uname)" == "Darwin" ]]; then
    echo "Signing binary (macOS)..."
    sudo codesign --force --sign - "${INSTALL_DIR}/${BINARY}"
fi

# ── Verify ────────────────────────────────────────────────────────────────────
echo ""
"${INSTALL_DIR}/${BINARY}" version
echo "✅ ${BINARY} installed successfully."
