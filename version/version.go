// Package version is weft's one version string: the framework
// module's release tag, stamped into Studio's api/meta, the devtools
// panel bundle, the otel resource, the runtime's registration and the
// binaries' version output. It imports nothing but the standard
// library, so any layer can carry it.
package version

import "runtime/debug"

// Version is the framework module's release tag. The release process
// bumps it with the tag (and rebuilds studio/dist, whose panel bundle
// is stamped from this line by studio/web/vite.panel.config.ts); it
// moves for nothing else. studio's tests fail when studio.Version or
// the embedded panel disagree with it.
const Version = "v0.9.0"

// modulePath is the framework module, as build info names it.
const modulePath = "github.com/weftgo/weft"

// Runtime reports the version of the framework module this binary was
// built from, read from [debug.ReadBuildInfo]: the main module's
// version when the binary is weft's own, else the version of the
// github.com/weftgo/weft dependency (its replacement's, when replaced
// by another version). Where the build info has no version — inside
// the workspace or a test, where it reads "(devel)", or a replacement
// by a local directory — it returns [Version], the tag the source
// carries.
func Runtime() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return Version
	}
	return fromBuildInfo(bi)
}

// fromBuildInfo is Runtime over a given build info.
func fromBuildInfo(bi *debug.BuildInfo) string {
	if bi.Main.Path == modulePath {
		return orTag(bi.Main.Version)
	}
	for _, dep := range bi.Deps {
		if dep.Path != modulePath {
			continue
		}
		if dep.Replace != nil {
			return orTag(dep.Replace.Version)
		}
		return orTag(dep.Version)
	}
	return Version
}

// orTag is v when it is a version, Version when the build info had none.
func orTag(v string) string {
	if v == "" || v == "(devel)" {
		return Version
	}
	return v
}
