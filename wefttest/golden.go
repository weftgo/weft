package wefttest

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update is the golden-file switch: `go test -update` rewrites the
// files instead of comparing them. Registered at package init — this is
// the standard Go golden-file convention, and wefttest is only imported
// by test binaries.
var update = flag.Bool("update", false, "rewrite golden files instead of comparing")

// Golden compares got against the committed golden file at path. With
// -update it writes got (creating parent directories); otherwise a
// mismatch fails the test with a line diff, and a missing file fails
// with a hint to regenerate. Note the flag goes after the package list
// (`go test ./... -update`): placed before it, the go tool routes it to
// the wrong package's binary.
//
//	func TestManifest(t *testing.T) {
//		b, err := weft.Manifest(newAgent())
//		if err != nil { t.Fatal(err) }
//		wefttest.Golden(t, "weft.json", b)
//	}
func Golden(t testing.TB, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("golden %s: %v", path, err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			t.Fatalf("golden file %s does not exist; regenerate with `go test ./... -update` from the module root and commit the result", path)
		}
		t.Fatalf("golden %s: %v", path, err)
	}
	if !bytes.Equal(want, got) {
		t.Fatalf("golden file %s is stale; regenerate with `go test ./... -update` and commit the result.\ndiff (- committed, + generated):\n%s", path, lineDiff(string(want), string(got)))
	}
}

// lineDiff renders a minimal line diff between a and b — hand-rolled
// (no dependency), test output only.
func lineDiff(a, b string) string {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	// lcs[i][j] is the length of the longest common subsequence of
	// x[i:] and y[j:].
	n, m := len(x), len(y)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case x[i] == y[j]:
				lcs[i][j] = lcs[i+1][j+1] + 1
			case lcs[i+1][j] >= lcs[i][j+1]:
				lcs[i][j] = lcs[i+1][j]
			default:
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var sb strings.Builder
	var i, j int
	for i < n && j < m {
		switch {
		case x[i] == y[j]:
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			fmt.Fprintf(&sb, "- %s\n", x[i])
			i++
		default:
			fmt.Fprintf(&sb, "+ %s\n", y[j])
			j++
		}
	}
	for ; i < n; i++ {
		fmt.Fprintf(&sb, "- %s\n", x[i])
	}
	for ; j < m; j++ {
		fmt.Fprintf(&sb, "+ %s\n", y[j])
	}
	return sb.String()
}
