#!/bin/sh
# Create the service user and reload systemd after install/upgrade.
set -e

if ! getent group proxy-balancer >/dev/null 2>&1; then
	groupadd --system proxy-balancer
fi

if ! getent passwd proxy-balancer >/dev/null 2>&1; then
	useradd --system --gid proxy-balancer --no-create-home \
		--home-dir /nonexistent --shell /usr/sbin/nologin proxy-balancer
fi

systemctl daemon-reload >/dev/null 2>&1 || true

echo "proxy-socks5-balancer installed."
echo "Edit /etc/proxy-socks5-balancer/config.yaml, then:"
echo "  systemctl enable --now proxy-socks5-balancer"
