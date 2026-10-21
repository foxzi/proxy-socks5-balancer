#!/bin/sh
# Build the binary and produce .deb and .rpm packages via nfpm (in Docker).
# Usage: VERSION=0.1.0 deploy/build-packages.sh
set -e

VERSION="${VERSION:-0.1.0}"
cd "$(dirname "$0")/.."

echo ">> building binary (version $VERSION)"
mkdir -p dist
CGO_ENABLED=0 GOPROXY=off GOSUMDB=off \
	go build -trimpath -ldflags "-s -w" \
	-o dist/proxy-socks5-balancer ./cmd/balancer

echo ">> packaging deb + rpm"
for pkg in deb rpm; do
	docker run --rm \
		-e VERSION="$VERSION" \
		-w /work -v "$PWD":/work \
		goreleaser/nfpm package \
		--config nfpm.yaml --target dist --packager "$pkg"
done

echo ">> done"
ls -1 dist/*.deb dist/*.rpm
