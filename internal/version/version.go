// Package version exposes the build version of the balancer.
//
// Version is overridden at build time with the git tag via:
//
//	-ldflags "-X proxy-socks5-balancer/internal/version.Version=v1.2.3"
//
// When built without that flag (go build, go test) it stays "dev".
package version

// Version is the build version. Set via -ldflags at build time.
var Version = "dev"
