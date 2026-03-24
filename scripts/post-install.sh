#!/bin/sh
# Post-install: auto-install/update shell aliases for the installing user.
# Runs as root on deb/rpm/apk — use SUDO_USER or DBUS_SESSION_BUS_ADDRESS owner to find real user.
set -e

REAL_USER="${SUDO_USER:-${USER:-}}"
if [ -z "$REAL_USER" ] || [ "$REAL_USER" = "root" ]; then
  exit 0
fi

su - "$REAL_USER" -c 'openkiro alias --install' 2>/dev/null || true
