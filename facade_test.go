package weft

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weftgo/weft/internal/facadegen"
)

const module = "github.com/weftgo/weft"

// facades lists every generated facade in this module with the exact
// options its go:generate line records (paths relative to the facade's
// directory, as go generate runs them).
var facades = []struct {
	dir  string
	opts facadegen.Options
}{
	{".", facadegen.Options{
		SrcDir: "./core", SrcImport: module + "/core", SrcName: "core", DstPkg: "weft",
		Generate: "go run " + module + "/internal/cmd/genfacade -src ./core -import " + module + "/core -as core -pkg weft -out facade.go",
	}},
	{"mw", facadegen.Options{
		SrcDir: "../core/mw", SrcImport: module + "/core/mw", SrcName: "coremw", DstPkg: "mw",
		Rewrite:  map[string]string{module + "/core": module},
		Generate: "go run " + module + "/internal/cmd/genfacade -src ../core/mw -import " + module + "/core/mw -as coremw -pkg mw -out facade.go -rewrite " + module + "/core=" + module,
	}},
	{"wefttest", facadegen.Options{
		SrcDir: "../core/wefttest", SrcImport: module + "/core/wefttest", SrcName: "corewefttest", DstPkg: "wefttest",
		Rewrite:  map[string]string{module + "/core": module},
		Generate: "go run " + module + "/internal/cmd/genfacade -src ../core/wefttest -import " + module + "/core/wefttest -as corewefttest -pkg wefttest -out facade.go -rewrite " + module + "/core=" + module,
	}},
	{"wefttest/conformance", facadegen.Options{
		SrcDir: "../../core/wefttest/conformance", SrcImport: module + "/core/wefttest/conformance", SrcName: "coreconformance", DstPkg: "conformance",
		Rewrite:  map[string]string{module + "/core": module, module + "/core/wefttest": module + "/wefttest"},
		Generate: "go run " + module + "/internal/cmd/genfacade -src ../../core/wefttest/conformance -import " + module + "/core/wefttest/conformance -as coreconformance -pkg conformance -out facade.go -rewrite " + module + "/core=" + module + " -rewrite " + module + "/core/wefttest=" + module + "/wefttest",
	}},
}

// The facades (facade.go here, mw, wefttest, wefttest/conformance) are
// generated from the core module's packages and must re-export every
// exported declaration of their source: a name added to core without
// go generate ./... is a name the framework import silently lacks. The
// committed file must also be what the generator writes today. The
// test reads the core sources beside this module, so it skips in a
// module zip (which carries no nested module) and runs in the repo.
func TestFacadesAreComplete(t *testing.T) {
	if _, err := os.Stat(filepath.Join("core", "go.mod")); err != nil {
		t.Skip("core/ is not beside this module (module cache?); the facade gate runs from the repository")
	}
	for _, f := range facades {
		t.Run(f.dir, func(t *testing.T) {
			src := filepath.Join(f.dir, f.opts.SrcDir)
			want := names(t, src)
			got := names(t, f.dir)
			for name, kind := range want {
				if _, ok := got[name]; !ok {
					t.Errorf("%s exports %s %s; %s does not re-export it (run go generate ./...)", src, kind, name, f.dir)
				}
			}
			for name, kind := range got {
				if _, ok := want[name]; !ok {
					t.Errorf("%s declares %s %s, which %s does not export; a facade only re-exports", f.dir, kind, name, src)
				}
			}
			opts := f.opts
			opts.SrcDir = src
			code, err := facadegen.Generate(opts)
			if err != nil {
				t.Fatal(err)
			}
			committed, err := os.ReadFile(filepath.Join(f.dir, "facade.go"))
			if err != nil {
				t.Fatal(err)
			}
			if string(code) != string(committed) {
				t.Errorf("%s/facade.go is stale: run go generate ./...", f.dir)
			}
		})
	}
}

// names maps each exported top-level declaration of the package in dir
// to its kind.
func names(t *testing.T, dir string) map[string]string {
	t.Helper()
	decls, err := facadegen.Exported(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, d := range decls {
		out[d.Name] = d.Kind
	}
	return out
}
