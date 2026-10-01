package weft

// weft.version gets a source (ADR 0024 S1.2): the const must equal the
// newest v* tag reachable from HEAD — a released tree — or, ahead of a
// release, the target named by the CHANGELOG's unreleased heading. It
// sat stale at v0.3.6 through the v0.5.0 release once; this test makes
// that a failure, not a silent drift. Skips cleanly when no tag is
// reachable and no unreleased heading names a target (a fresh clone
// before the first tag).

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestVersionHasASource(t *testing.T) {
	tag, err := exec.Command("git", "describe", "--tags", "--abbrev=0", "--match", "v*", "HEAD").Output()
	newest := strings.TrimSpace(string(tag))
	if err != nil && len(newest) > 0 {
		// git wrote something unexpected; treat as no answer rather than
		// trusting a partial value.
		newest = ""
	}

	target, hasTarget := changelogUnreleasedTarget(t)
	// Either source may vouch for the const: the tag on a released tree,
	// the heading on one heading for its next release. Matching neither
	// is the drift this test exists to catch.
	if newest != "" && version == newest {
		return
	}
	if hasTarget && version == "v"+target {
		return
	}
	switch {
	case newest != "" && hasTarget:
		t.Errorf("version = %q matches neither the newest tag %q nor the CHANGELOG's unreleased target %q", version, newest, target)
	case newest != "":
		t.Errorf("version = %q, newest reachable tag = %q; bump the const when cutting a release", version, newest)
	case hasTarget:
		t.Errorf("version = %q, CHANGELOG's unreleased heading targets %q; they must agree until the tag exists", version, target)
	default:
		t.Skip("no v* tag reachable from HEAD and no unreleased heading naming a target — nothing to pin the version against")
	}
}

// changelogUnreleasedTarget reads the CHANGELOG's unreleased heading —
// "## 0.6.0 (unreleased)" — and returns the version it targets, without
// the v prefix the headings omit.
func changelogUnreleasedTarget(t *testing.T) (string, bool) {
	t.Helper()
	b, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		return "", false
	}
	m := regexp.MustCompile(`(?m)^## (\S+) \(unreleased\)`).FindSubmatch(b)
	if m == nil {
		return "", false
	}
	return string(m[1]), true
}
