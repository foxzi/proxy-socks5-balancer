#!/bin/sh
# Stop the service before it is removed.
set -e

if systemctl is-active --quiet proxy-socks5-balancer 2>/dev/null; then
	systemctl stop proxy-socks5-balancer || true
fi
