// Package version is weft's one version string: the framework
// module's release tag, stamped into Studio's api/meta, the devtools
// panel bundle, the otel resource, the runtime's registration and the
// binaries' version output. It imports nothing but the standard
// library, so any layer can carry it.
package version

import (
	"regexp"
	"runtime/debug"
	"strings"
)

// Version is the framework module's release tag. The release process
// bumps it with the tag (and rebuilds studio/dist, whose panel bundle
// is stamped from this line by studio/web/vite.panel.config.ts); it
// moves for nothing else. studio's tests fail when studio.Version or
// the embedded panel disagree with it. The second place to bump is
// core/observe.go's own version literal (the weft.version every run
// stamps; core imports nothing of this module), which
// otel/hardening_test.go pins against this one.
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
// carries. So does a build stamped from version control rather than a
// tag (Go 1.24 and later): a pseudo-version (v0.9.1-0.20260101120000-
// abcdef123456) or a "+dirty" one names a commit, not a release.
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

// pseudoVersion matches a Go pseudo-version, in all three of its
// forms (vX.0.0-T-H, vX.Y.Z-pre.0.T-H, vX.Y.Z-0.T-H), with or without
// build metadata — golang.org/x/mod/module's pattern, which this
// package does not import.
var pseudoVersion = regexp.MustCompile(`^v[0-9]+\.(0\.0-|[0-9]+\.[0-9]+-([^+]*\.)?0\.)[0-9]{14}-[A-Za-z0-9]+(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// orTag is v when it is a release version, Version when the build info
// had none or names a commit rather than a tag: "(devel)", a
// pseudo-version, or a version with "+dirty" build metadata.
func orTag(v string) string {
	if v == "" || v == "(devel)" || pseudoVersion.MatchString(v) || strings.HasSuffix(v, "+dirty") {
		return Version
	}
	return v
}
