#!/bin/sh
# Build the binary and produce .deb and .rpm packages via nfpm (in Docker).
#
# The version is taken from the git tag by default (git describe), so tag a
# release first:
#
#	git tag -a v0.2.0 -m "release 0.2.0"
#	deploy/build-packages.sh
#
# Override explicitly with VERSION=1.2.3 deploy/build-packages.sh
set -e

cd "$(dirname "$0")/.."

# Derive the version from git when not given. "git describe" yields e.g.
# v0.2.0 on a tag, or v0.2.0-3-gabc123 a few commits later. Strip the leading
# "v" so package versions are clean (0.2.0). Fall back to 0.0.0 with no tags.
if [ -z "$VERSION" ]; then
	VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo 0.0.0)"
fi
PKG_VERSION="$(printf '%s' "$VERSION" | sed 's/^v//')"

# Target package architecture (Debian naming: amd64, arm64, ...). Defaults to
# the host arch. GOARCH is derived from it so the binary matches the package.
ARCH="${ARCH:-$(dpkg --print-architecture 2>/dev/null || echo amd64)}"
case "$ARCH" in
amd64) GOARCH=amd64 ;;
arm64) GOARCH=arm64 ;;
armhf) GOARCH=arm ;;
*) GOARCH="$ARCH" ;;
esac

echo ">> building binary (version $VERSION, arch $ARCH/$GOARCH)"
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" GOPROXY=off GOSUMDB=off \
	go build -trimpath \
	-ldflags "-s -w -X proxy-socks5-balancer/internal/version.Version=$PKG_VERSION" \
	-o dist/proxy-socks5-balancer ./cmd/balancer

echo ">> packaging deb + rpm"
for pkg in deb rpm; do
	docker run --rm \
		-e VERSION="$PKG_VERSION" -e ARCH="$ARCH" \
		-w /work -v "$PWD":/work \
		goreleaser/nfpm package \
		--config nfpm.yaml --target dist --packager "$pkg"
done

echo ">> done"
ls -1 dist/*.deb dist/*.rpm
