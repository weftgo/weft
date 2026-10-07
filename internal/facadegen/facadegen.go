// Package facadegen writes a facade package: one that re-exports
// another package's whole exported API under a new import path, so
// github.com/weftgo/weft (the framework) offers the loop that
// github.com/weftgo/weft/core (the slim module) implements, under the
// same names. Types become aliases, constants and variables become
// re-declarations bound to the originals, and functions become
// one-line wrappers — a wrapper rather than a func variable so the
// signature, its generics and its doc comment survive on pkg.go.dev.
//
// The facade is generated from source (go/ast), never hand-written:
// a release regenerates it (go generate ./...) and TestFacadesAreComplete
// fails the build when the facade and its source disagree.
package facadegen

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Options describe one facade.
type Options struct {
	// SrcDir is the directory of the package being re-exported.
	SrcDir string
	// SrcImport is its import path; SrcName the local name the facade
	// imports it under (shown on pkg.go.dev as the alias target).
	SrcImport, SrcName string
	// DstPkg is the facade's package name.
	DstPkg string
	// Rewrite maps an import path the source's signatures use to the
	// path the facade should name instead — the layers' facades name
	// github.com/weftgo/weft where the source names its core, so the
	// documented signatures read weft.Model, not core.Model. The local
	// name is the rewritten path's last element.
	Rewrite map[string]string
	// Generate is the go:generate line to record, verbatim, without
	// the "//go:generate " prefix.
	Generate string
}

// Decl is one exported top-level declaration.
type Decl struct {
	Kind string // "type", "func", "const" or "var"
	Name string
}

var versionElem = regexp.MustCompile(`^v[0-9]+$`)

// localName is the package name an import path binds by default.
func localName(importPath string) string {
	base := path.Base(importPath)
	if versionElem.MatchString(base) {
		base = path.Base(path.Dir(importPath))
	}
	return strings.ReplaceAll(base, "-", "")
}

// parseDir parses the non-test, unconstrained Go files of dir.
func parseDir(fset *token.FileSet, dir string) ([]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if bytes.Contains(src, []byte("//go:build ")) {
			// A constrained file is not part of the default build; the
			// facade mirrors what every consumer compiles.
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), src, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("facadegen: no Go files in %s", dir)
	}
	sort.Slice(files, func(i, j int) bool {
		return fset.File(files[i].Pos()).Name() < fset.File(files[j].Pos()).Name()
	})
	return files, nil
}

// Exported lists the exported top-level declarations of the package in
// dir, in source order. Methods belong to their types and are not listed.
func Exported(dir string) ([]Decl, error) {
	fset := token.NewFileSet()
	files, err := parseDir(fset, dir)
	if err != nil {
		return nil, err
	}
	var out []Decl
	for _, f := range files {
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.IsExported() {
					out = append(out, Decl{"func", d.Name.Name})
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						if s.Name.IsExported() {
							out = append(out, Decl{"type", s.Name.Name})
						}
					case *ast.ValueSpec:
						kind := "var"
						if d.Tok == token.CONST {
							kind = "const"
						}
						for _, n := range s.Names {
							if n.IsExported() {
								out = append(out, Decl{kind, n.Name})
							}
						}
					}
				}
			}
		}
	}
	return out, nil
}

type gen struct {
	opts    Options
	fset    *token.FileSet
	imports map[string]string // local name → import path, for the facade
}

// Generate renders the facade source for opts.
func Generate(opts Options) ([]byte, error) {
	g := &gen{opts: opts, fset: token.NewFileSet(), imports: map[string]string{}}
	files, err := parseDir(g.fset, opts.SrcDir)
	if err != nil {
		return nil, err
	}
	g.imports[opts.SrcName] = opts.SrcImport
	var body bytes.Buffer
	for _, f := range files {
		fileImports := map[string]string{} // local name → path, in the source file
		for _, imp := range f.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			name := localName(p)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			fileImports[name] = p
		}
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv != nil || !d.Name.IsExported() {
					continue
				}
				if err := g.funcDecl(&body, d, fileImports); err != nil {
					return nil, err
				}
			case *ast.GenDecl:
				if err := g.genDecl(&body, d, fileImports); err != nil {
					return nil, err
				}
			}
		}
	}

	var out bytes.Buffer
	fmt.Fprintf(&out, "// Code generated by internal/cmd/genfacade; DO NOT EDIT.\n")
	fmt.Fprintf(&out, "// Source: %s\n\n", opts.SrcImport)
	fmt.Fprintf(&out, "package %s\n\n", opts.DstPkg)
	if opts.Generate != "" {
		fmt.Fprintf(&out, "//go:generate %s\n\n", opts.Generate)
	}
	names := make([]string, 0, len(g.imports))
	for n := range g.imports {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return g.imports[names[i]] < g.imports[names[j]] })
	out.WriteString("import (\n")
	isStd := func(p string) bool { return !strings.Contains(strings.SplitN(p, "/", 2)[0], ".") }
	for _, std := range []bool{true, false} {
		for _, n := range names {
			p := g.imports[n]
			if isStd(p) != std {
				continue
			}
			if localName(p) == n {
				fmt.Fprintf(&out, "\t%q\n", p)
			} else {
				fmt.Fprintf(&out, "\t%s %q\n", n, p)
			}
		}
		if std {
			out.WriteString("\n")
		}
	}
	out.WriteString(")\n\n")
	out.Write(body.Bytes())
	src, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("facadegen: generated source does not format: %w\n%s", err, out.Bytes())
	}
	return src, nil
}

// qualify rewrites the package qualifiers an expression uses — the
// source file's local names — into the facade's, recording each import
// the facade needs. It mutates the (throwaway) AST in place.
func (g *gen) qualify(expr ast.Node, fileImports map[string]string) error {
	var err error
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		p, ok := fileImports[x.Name]
		if !ok {
			return true // a field or method selector, not a package
		}
		if to, ok := g.opts.Rewrite[p]; ok {
			p = to
		}
		name := localName(p)
		if prev, ok := g.imports[name]; ok && prev != p {
			err = fmt.Errorf("facadegen: import name %q is claimed by both %s and %s", name, prev, p)
			return false
		}
		g.imports[name] = p
		x.Name = name
		return false
	})
	return err
}

func (g *gen) print(node any) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, g.fset, node); err != nil {
		panic(err)
	}
	return b.String()
}

// doc writes a declaration's comment group, if any, with no trailing
// position information so the facade's godoc matches the source's.
func (g *gen) doc(w *bytes.Buffer, cg *ast.CommentGroup) {
	if cg == nil {
		return
	}
	for _, c := range cg.List {
		w.WriteString(c.Text)
		w.WriteString("\n")
	}
}

func (g *gen) genDecl(w *bytes.Buffer, d *ast.GenDecl, fileImports map[string]string) error {
	switch d.Tok {
	case token.TYPE:
		for _, spec := range d.Specs {
			s := spec.(*ast.TypeSpec)
			if !s.Name.IsExported() {
				continue
			}
			doc := s.Doc
			if doc == nil && len(d.Specs) == 1 {
				doc = d.Doc
			}
			g.doc(w, doc)
			if s.TypeParams != nil {
				if err := g.qualify(s.TypeParams, fileImports); err != nil {
					return err
				}
				fmt.Fprintf(w, "type %s[%s] = %s.%s[%s]\n\n", s.Name.Name, g.typeParams(s.TypeParams), g.opts.SrcName, s.Name.Name, typeParamNames(s.TypeParams))
			} else {
				fmt.Fprintf(w, "type %s = %s.%s\n\n", s.Name.Name, g.opts.SrcName, s.Name.Name)
			}
		}
	case token.CONST, token.VAR:
		var lines []string
		for _, spec := range d.Specs {
			s := spec.(*ast.ValueSpec)
			for _, n := range s.Names {
				if !n.IsExported() {
					continue
				}
				var b bytes.Buffer
				g.doc(&b, s.Doc)
				fmt.Fprintf(&b, "%s = %s.%s", n.Name, g.opts.SrcName, n.Name)
				if s.Comment != nil {
					b.WriteString(" " + s.Comment.List[0].Text)
				}
				lines = append(lines, b.String())
			}
		}
		if len(lines) == 0 {
			return nil
		}
		g.doc(w, d.Doc)
		if len(lines) == 1 && len(d.Specs) == 1 {
			fmt.Fprintf(w, "%s %s\n\n", d.Tok, lines[0])
			return nil
		}
		fmt.Fprintf(w, "%s (\n", d.Tok)
		for i, l := range lines {
			if i > 0 && strings.HasPrefix(l, "//") {
				w.WriteString("\n")
			}
			w.WriteString(l + "\n")
		}
		w.WriteString(")\n\n")
	}
	return nil
}

// typeParams renders a type parameter list's contents: "In, Out any".
func (g *gen) typeParams(tp *ast.FieldList) string {
	var parts []string
	for _, f := range tp.List {
		var names []string
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
		parts = append(parts, strings.Join(names, ", ")+" "+g.print(f.Type))
	}
	return strings.Join(parts, ", ")
}

func typeParamNames(tp *ast.FieldList) string {
	var names []string
	for _, f := range tp.List {
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
	}
	return strings.Join(names, ", ")
}

func (g *gen) funcDecl(w *bytes.Buffer, d *ast.FuncDecl, fileImports map[string]string) error {
	ft := d.Type
	if err := g.qualify(ft, fileImports); err != nil {
		return err
	}
	// Package names the signature now uses must not be shadowed by a
	// parameter; a parameter sharing one is renamed.
	used := map[string]bool{}
	ast.Inspect(ft, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok {
				if _, ok := g.imports[x.Name]; ok {
					used[x.Name] = true
				}
			}
		}
		return true
	})
	var args []string
	for i, f := range ft.Params.List {
		if len(f.Names) == 0 {
			f.Names = []*ast.Ident{ast.NewIdent(fmt.Sprintf("p%d", i))}
		}
		for j, n := range f.Names {
			if n.Name == "_" || used[n.Name] {
				n.Name = fmt.Sprintf("p%d_%d", i, j)
			}
			if _, variadic := f.Type.(*ast.Ellipsis); variadic {
				args = append(args, n.Name+"...")
			} else {
				args = append(args, n.Name)
			}
		}
	}
	// Named results would make a bare return legal; the wrapper never
	// uses one, so drop the names to keep the godoc signature honest.
	if ft.Results != nil {
		for _, f := range ft.Results.List {
			f.Names = nil
		}
	}
	call := fmt.Sprintf("%s.%s", g.opts.SrcName, d.Name.Name)
	if ft.TypeParams != nil {
		call += "[" + typeParamNames(ft.TypeParams) + "]"
	}
	call += "(" + strings.Join(args, ", ") + ")"
	g.doc(w, d.Doc)
	sig := g.print(ft) // "func[TP](params) results"
	sig = strings.TrimPrefix(sig, "func")
	if ft.Results != nil {
		fmt.Fprintf(w, "func %s%s { return %s }\n\n", d.Name.Name, sig, call)
	} else {
		fmt.Fprintf(w, "func %s%s { %s }\n\n", d.Name.Name, sig, call)
	}
	return nil
}
