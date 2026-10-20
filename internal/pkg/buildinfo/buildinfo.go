// Package buildinfo reports the version the running binary was built from.
package buildinfo

import "runtime/debug"

// version is set at link time by release builds:
//
//	go build -ldflags "-X github.com/azverinus/flagnetic/internal/pkg/buildinfo.version=v1.2.3"
var version string

// Version returns the release version set at link time, else the module
// version recorded by `go install`, else "devel".
func Version() string {
	return resolve(version, debug.ReadBuildInfo)
}

func resolve(linked string, read func() (*debug.BuildInfo, bool)) string {
	if linked != "" {
		return linked
	}
	if info, ok := read(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "devel"
}
