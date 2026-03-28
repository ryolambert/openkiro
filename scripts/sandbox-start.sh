#!/bin/bash
# sandbox-start.sh — openkiro proxy launcher for Docker Sandbox templates
#
# This script is the ENTRYPOINT for the openkiro Docker Sandbox templates
# (Dockerfile.sandbox-claude, Dockerfile.sandbox-kiro). It:
#
#   1. Starts the openkiro proxy in the background on OPENKIRO_PORT (default 1234).
#   2. Waits until the proxy /health endpoint responds (up to OPENKIRO_READY_TIMEOUT).
#   3. execs the agent command (default: claude --dangerously-skip-permissions).
#
# The proxy handles Kiro/AWS CodeWhisperer authentication automatically using
# the token at ~/.aws/sso/cache/kiro-auth-token.json (bind-mounted read-only
# from the host by the Docker Sandbox manager).
#
# Environment variables:
#   OPENKIRO_PORT            Proxy listen port (default: 1234)
#   OPENKIRO_READY_TIMEOUT   Seconds to wait for proxy readiness (default: 15)
#   ANTHROPIC_BASE_URL       Set by the template Dockerfile; must match the port above

set -eu

OPENKIRO_PORT="${OPENKIRO_PORT:-1234}"
OPENKIRO_READY_TIMEOUT="${OPENKIRO_READY_TIMEOUT:-15}"

log() { printf '[openkiro] %s\n' "$*" >&2; }

# ── Start the proxy ──────────────────────────────────────────────────────────
log "starting openkiro proxy on port ${OPENKIRO_PORT}..."
openkiro server "${OPENKIRO_PORT}" &
PROXY_PID=$!

# Reap the background process on exit so we don't leak it.
cleanup() { kill "${PROXY_PID}" 2>/dev/null || true; }
trap cleanup EXIT INT TERM

# ── Wait for the proxy to be ready ──────────────────────────────────────────
# Use curl if available; fall back to nc (netcat) for minimal images.
if command -v curl >/dev/null 2>&1; then
  health_check() { curl -sf "http://localhost:${OPENKIRO_PORT}/health" >/dev/null 2>&1; }
elif command -v nc >/dev/null 2>&1; then
  health_check() { nc -z 127.0.0.1 "${OPENKIRO_PORT}" >/dev/null 2>&1; }
else
  # No probe tool available — just wait a fixed time.
  log "WARNING: neither curl nor nc found; waiting 5s for proxy startup"
  sleep 5
  health_check() { true; }
fi

elapsed=0
until health_check; do
  if [ "${elapsed}" -ge "${OPENKIRO_READY_TIMEOUT}" ]; then
    log "ERROR: openkiro proxy did not become ready within ${OPENKIRO_READY_TIMEOUT}s"
    exit 1
  fi
  sleep 1
  elapsed=$((elapsed + 1))
done
log "openkiro proxy ready (port ${OPENKIRO_PORT})"

# ── Run the agent ────────────────────────────────────────────────────────────
# Default to 'claude --dangerously-skip-permissions' if no command is given.
# Callers (e.g. docker sandbox run ... -- kiro chat --trust-all-tools) can
# override by passing arguments after the entrypoint.
if [ "$#" -eq 0 ]; then
  log "no command specified; using default"
  set -- claude --dangerously-skip-permissions
fi

log "exec: $*"
exec "$@"
