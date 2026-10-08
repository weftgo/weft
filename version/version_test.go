package version

import (
	"runtime/debug"
	"testing"
)

func TestRuntimeUnderDevelIsVersion(t *testing.T) {
	// A test binary's build info reads "(devel)" (or carries no weft
	// dependency at all): Runtime falls back to the source's tag.
	if got := Runtime(); got != Version {
		t.Errorf("Runtime() = %q in the workspace, want Version %q", got, Version)
	}
	devel := &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "(devel)"}}
	if got := fromBuildInfo(devel); got != Version {
		t.Errorf("main (devel): got %q, want %q", got, Version)
	}
}

func TestRuntimeReadsBuildInfo(t *testing.T) {
	for _, tc := range []struct {
		name string
		bi   debug.BuildInfo
		want string
	}{
		{"main module tagged", debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v1.2.3"}}, "v1.2.3"},
		{"consumer dependency", debug.BuildInfo{
			Main: debug.Module{Path: "example.com/app", Version: "(devel)"},
			Deps: []*debug.Module{{Path: "github.com/weftgo/weft/core", Version: "v9.9.9"}, {Path: modulePath, Version: "v1.2.4"}},
		}, "v1.2.4"},
		{"replaced by a version", debug.BuildInfo{
			Main: debug.Module{Path: "example.com/app"},
			Deps: []*debug.Module{{Path: modulePath, Version: "v1.2.4", Replace: &debug.Module{Path: "example.com/fork", Version: "v1.2.5"}}},
		}, "v1.2.5"},
		{"replaced by a directory", debug.BuildInfo{
			Main: debug.Module{Path: "example.com/app"},
			Deps: []*debug.Module{{Path: modulePath, Version: "v1.2.4", Replace: &debug.Module{Path: "../weft"}}},
		}, Version},
		{"no weft at all", debug.BuildInfo{Main: debug.Module{Path: "example.com/app"}}, Version},
	} {
		if got := fromBuildInfo(&tc.bi); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A build stamped from version control (Go 1.24 and later) names a
// commit, not a release: pseudo-versions and "+dirty" builds read as
// the source's tag; a tag, a pre-release tag and other build metadata
// stay as they are.
func TestOrTagMapsVCSStampsToVersion(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"v0.9.1-0.20261008120000-abcdef123456", Version},
		{"v0.9.1-0.20261008120000-abcdef123456+dirty", Version},
		{"v0.0.0-20261008120000-abcdef123456", Version},
		{"v1.0.0-rc.1.0.20261008120000-abcdef123456", Version},
		{"v2.3.4-0.20261008120000-abcdef123456+incompatible", Version},
		{"v0.9.0+dirty", Version},
		{"(devel)", Version},
		{"", Version},
		{"v0.9.0", "v0.9.0"},
		{"v1.0.0-rc.1", "v1.0.0-rc.1"},
		{"v1.2.3+incompatible", "v1.2.3+incompatible"},
		{"v0.9.1-0.2026-abc", "v0.9.1-0.2026-abc"}, // not fourteen digits: not a pseudo-version
	} {
		if got := orTag(tc.in); got != tc.want {
			t.Errorf("orTag(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	bi := &debug.BuildInfo{Main: debug.Module{Path: modulePath, Version: "v0.9.1-0.20261008120000-abcdef123456+dirty"}}
	if got := fromBuildInfo(bi); got != Version {
		t.Errorf("a local VCS-stamped build: got %q, want %q", got, Version)
	}
}
