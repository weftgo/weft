// Command genfacade writes a facade package (internal/facadegen): the
// framework's github.com/weftgo/weft, weft/mw, weft/wefttest and
// weft/wefttest/conformance re-export the core module's packages of
// the same name. Run it through go generate ./... from the repository
// root; each facade records the exact invocation that produced it.
//
//	genfacade -src ./core -import github.com/weftgo/weft/core -as core -pkg weft -out facade.go
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/weftgo/weft/internal/facadegen"
)

type rewrites map[string]string

func (r rewrites) String() string { return fmt.Sprint(map[string]string(r)) }

func (r rewrites) Set(v string) error {
	from, to, ok := strings.Cut(v, "=")
	if !ok {
		return fmt.Errorf("want from=to, got %q", v)
	}
	r[from] = to
	return nil
}

func main() {
	var (
		src     = flag.String("src", "", "directory of the package to re-export")
		imp     = flag.String("import", "", "its import path")
		as      = flag.String("as", "", "local name the facade imports it under (default: the path's last element)")
		pkg     = flag.String("pkg", "", "the facade's package name")
		out     = flag.String("out", "facade.go", "file to write")
		rewrite = rewrites{}
	)
	flag.Var(rewrite, "rewrite", "from=to: name import path `to` where the source names `from` (repeatable)")
	flag.Parse()
	if *src == "" || *imp == "" || *pkg == "" {
		flag.Usage()
		os.Exit(2)
	}
	if *as == "" {
		*as = (*imp)[strings.LastIndex(*imp, "/")+1:]
	}
	code, err := facadegen.Generate(facadegen.Options{
		SrcDir:    *src,
		SrcImport: *imp,
		SrcName:   *as,
		DstPkg:    *pkg,
		Rewrite:   rewrite,
		Generate:  "go run github.com/weftgo/weft/internal/cmd/genfacade " + strings.Join(os.Args[1:], " "),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, code, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
