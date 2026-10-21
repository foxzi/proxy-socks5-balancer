#!/bin/sh
# Reload systemd after the unit file is removed.
set -e

systemctl daemon-reload >/dev/null 2>&1 || true
