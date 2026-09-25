// Package buildinfo carries the release version, set at link time with
// -ldflags "-X github.com/binbandit/yip/internal/buildinfo.Version=v0.1.0".
package buildinfo

// Version of this build.
var Version = "0.1.0-dev"
