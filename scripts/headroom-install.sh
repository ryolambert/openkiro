#!/usr/bin/env bash
# headroom-install.sh — install the headroom-ai Python compression proxy.
#
# Usage:
#   scripts/headroom-install.sh              Install headroom-ai[proxy]
#   scripts/headroom-install.sh --full       Install headroom-ai[all]
#   scripts/headroom-install.sh --check      Check if headroom is installed
set -euo pipefail

PACKAGE="headroom-ai[proxy]"

# ── Argument parsing ──────────────────────────────────────────────────────────
for arg in "$@"; do
    case "$arg" in
        --full)  PACKAGE="headroom-ai[all]" ;;
        --check)
            if command -v headroom &>/dev/null; then
                echo "headroom is installed: $(headroom --version 2>/dev/null || echo 'unknown version')"
                exit 0
            else
                echo "headroom is NOT installed"
                exit 1
            fi
            ;;
        --help|-h)
            echo "Usage: $0 [--full] [--check]"
            echo "  --full   Install headroom-ai[all] (includes evals, ML compression)"
            echo "  --check  Check if headroom is already installed"
            exit 0
            ;;
        *)
            echo "Unknown option: $arg" >&2
            exit 1
            ;;
    esac
done

# ── Python detection ──────────────────────────────────────────────────────────
PYTHON=""
for candidate in python3 python; do
    if command -v "$candidate" &>/dev/null; then
        PYTHON="$candidate"
        break
    fi
done

if [ -z "$PYTHON" ]; then
    echo "Error: Python 3.10+ is required but not found." >&2
    echo "Install Python from https://www.python.org/downloads/" >&2
    exit 1
fi

# Verify Python version ≥ 3.10
PY_VER=$("$PYTHON" -c 'import sys; print(f"{sys.version_info.major}.{sys.version_info.minor}")')
PY_MAJOR=${PY_VER%%.*}
PY_MINOR=${PY_VER##*.}

if [ "$PY_MAJOR" -lt 3 ] || { [ "$PY_MAJOR" -eq 3 ] && [ "$PY_MINOR" -lt 10 ]; }; then
    echo "Error: Python >= 3.10 required (found $PY_VER)" >&2
    exit 1
fi

echo "Using Python $PY_VER ($PYTHON)"

# ── pip detection ─────────────────────────────────────────────────────────────
PIP=""
for candidate in pip3 pip; do
    if command -v "$candidate" &>/dev/null; then
        PIP="$candidate"
        break
    fi
done

if [ -z "$PIP" ]; then
    echo "pip not found, trying python -m pip..."
    PIP="$PYTHON -m pip"
fi

# ── Install ───────────────────────────────────────────────────────────────────
echo "Installing ${PACKAGE}..."
$PIP install "$PACKAGE"

# ── Verify ────────────────────────────────────────────────────────────────────
if command -v headroom &>/dev/null; then
    echo ""
    echo "✓ headroom installed successfully"
    echo "  Version: $(headroom --version 2>/dev/null || echo 'unknown')"
    echo ""
    echo "Quick start:"
    echo "  headroom proxy                    # Start compression proxy on :8787"
    echo "  headroom proxy --port 9000        # Custom port"
    echo ""
    echo "Use with openkiro:"
    echo "  openkiro server                   # Starts proxy on :1234 (uses headroom if available)"
else
    echo ""
    echo "⚠ headroom was installed but is not on PATH."
    echo "  You may need to add your Python scripts directory to PATH."
    echo "  Try: $PYTHON -m headroom proxy"
fi
