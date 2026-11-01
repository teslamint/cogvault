// Package version reports the cogvault release version.
package version

import "runtime/debug"

// injected is set at link time: -ldflags "-X github.com/teslamint/cogvault/internal/version.injected=v1.2.3".
// Release builds (Makefile, container image) set it from the git tag.
var injected string

// String returns the injected version, else the module version recorded by
// `go install ...@vX.Y.Z`, else "dev".
func String() string {
	return resolve(injected, debug.ReadBuildInfo)
}

func resolve(injected string, read func() (*debug.BuildInfo, bool)) string {
	if injected != "" {
		return injected
	}
	if info, ok := read(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
