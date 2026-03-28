#!/bin/sh
# entrypoint.sh — secure container entrypoint for the openkiro sandbox image
#
# This script runs as PID 1 (via tini) inside the sandbox container. It:
#   1. Validates the execution environment.
#   2. Starts the requested command.
#   3. Re-starts the command on unexpected exit (auto-heal).
#
# Usage (set via Docker CMD or docker run arguments):
#   /entrypoint.sh openkiro server [port]
#   /entrypoint.sh <any-agent-command>
#
# Environment variables:
#   OPENKIRO_PORT     — proxy listen port (default: 1234)
#   SANDBOX_MAX_RESTARTS — max auto-heal restarts before giving up (default: 5)
#   SANDBOX_RESTART_DELAY_SECS — seconds between restarts (default: 3)

set -eu

OPENKIRO_PORT="${OPENKIRO_PORT:-1234}"
SANDBOX_MAX_RESTARTS="${SANDBOX_MAX_RESTARTS:-5}"
SANDBOX_RESTART_DELAY_SECS="${SANDBOX_RESTART_DELAY_SECS:-3}"

log() { printf '[entrypoint] %s\n' "$*" >&2; }

# ── Validate environment ─────────────────────────────────────────────────────

# Refuse to run as root.
if [ "$(id -u)" -eq 0 ]; then
  log "ERROR: running as root is not permitted in the sandbox"
  exit 1
fi

log "sandbox started (uid=$(id -u), gid=$(id -g))"
log "workspace: ${WORKSPACE_DIR:-/workspace}"
log "proxy port: ${OPENKIRO_PORT}"

# ── Run with auto-heal ───────────────────────────────────────────────────────

# Default to 'openkiro server' if no arguments are supplied.
if [ "$#" -eq 0 ]; then
  set -- openkiro server
fi

log "command: $*"

restarts=0
while true; do
  # Execute the requested command.
  "$@" &
  child_pid=$!

  # Forward SIGTERM/SIGINT to the child process.
  trap 'kill -TERM "${child_pid}" 2>/dev/null' TERM INT

  # Wait for the child to exit.
  wait "${child_pid}"
  exit_code=$?

  # Reset the trap so it doesn't fire again during the next iteration.
  trap - TERM INT

  # A clean exit (0) or SIGTERM (143) means intentional shutdown — do not restart.
  if [ "${exit_code}" -eq 0 ] || [ "${exit_code}" -eq 143 ]; then
    log "command exited cleanly (exit=${exit_code}), shutting down"
    exit 0
  fi

  restarts=$((restarts + 1))
  log "command exited with code ${exit_code} (restart ${restarts}/${SANDBOX_MAX_RESTARTS})"

  if [ "${restarts}" -ge "${SANDBOX_MAX_RESTARTS}" ]; then
    log "ERROR: max restarts (${SANDBOX_MAX_RESTARTS}) reached, giving up"
    exit "${exit_code}"
  fi

  log "restarting in ${SANDBOX_RESTART_DELAY_SECS}s..."
  sleep "${SANDBOX_RESTART_DELAY_SECS}"
done
