package core_test

// The manifest names middleware and hooks the same whatever the
// compiler inlined (P10b-inline-names): runtime.FuncForPC names a
// closure after the function it was compiled into, so a constructor
// inlined into its caller (go1.26.0 inlines mw.Allow; -gcflags=all=-l
// inlines nothing) would rename its closure and change
// weft.manifest.hash — a false "stale".

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/mw"
	"github.com/weftgo/weft/core/wefttest"
)

// redactor returns a closure: small, so the compiler may inline it
// into its caller and rename the closure it returns.
func redactor(word string) core.ToolMiddleware {
	return func(next core.ToolCaller) core.ToolCaller {
		return func(ctx context.Context, call core.ToolCallPart) (string, error) {
			out, err := next(ctx, call)
			return strings.ReplaceAll(out, word, "***"), err
		}
	}
}

// timing is a named middleware function.
func timing(next core.ToolCaller) core.ToolCaller { return next }

func tapper() func(context.Context, core.Event) { return func(context.Context, core.Event) {} }

// inlineAgent is a fixed agent with every naming case: weft's own
// middleware (named where made), a closure from an inlinable helper, a
// named function, a closure literal, and hooks of both kinds.
func inlineAgent() *core.Agent {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	return core.New(wefttest.Script(wefttest.Say("ok")),
		core.Name("inline"),
		core.WrapModel(mw.Log(quiet), mw.Retry(), mw.RepairJSON()),
		core.WrapTools(mw.Audit(quiet), mw.Allow(nil), mw.MapErrors(nil), redactor("secret"), timing,
			func(next core.ToolCaller) core.ToolCaller { return next }),
		core.Tap(tapper()), core.Tap(func(context.Context, core.Event) {}),
		core.Tool("lookup", "", func(context.Context, struct{}) (string, error) { return "", nil },
			core.WrapTools(mw.MapErrors(nil))))
}

func manifestHash(t *testing.T) (string, []byte) {
	t.Helper()
	b, err := core.Manifest(inlineAgent())
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), b
}

// The names themselves: weft's own by name alone, a named function by
// its import path, every closure by where it is written — never a
// runtime name.
func TestManifestNamesIgnoreInlining(t *testing.T) {
	a := manifestOf(t, inlineAgent()).Agents[0]
	got := layerNames(a.ToolMiddleware)
	want := []string{"mw.Audit", "mw.Allow", "mw.MapErrors", "func@core/inline_names_test.go:30", "core_test.timing", "func@core/inline_names_test.go:52"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("tool_middleware = %v, want %v", got, want)
	}
	if a.ToolMiddleware[4].Source != "github.com/weftgo/weft/core_test/inline_names_test.go:39" {
		t.Errorf("named function's source = %q", a.ToolMiddleware[4].Source)
	}
	if got := layerNames(a.Hooks.Tap); strings.Join(got, " ") != "func@core/inline_names_test.go:41 func@core/inline_names_test.go:53" {
		t.Errorf("taps = %v", got)
	}
	if m := a.Tools[0].Middleware; len(m) != 1 || m[0].Name != "mw.MapErrors" {
		t.Errorf("tool's own middleware = %v", m)
	}
}

// The helper the build test runs in a subprocess: it prints the fixed
// agent's manifest hash, as that build named it.
func TestManifestHashHelper(t *testing.T) {
	if os.Getenv("WEFT_MANIFEST_HASH_HELPER") != "1" {
		t.Skip("run by TestManifestHashAcrossBuilds")
	}
	h, b := manifestHash(t)
	fmt.Printf("MANIFEST %s\n%s\n", h, b)
}

// One agent, one hash: built with the compiler's inlining and with
// none (-gcflags=all=-l), the fixed agent's manifest is byte for byte
// the same. (The CI floor's toolchain, go1.26.0, is run by the gates.)
func TestManifestHashAcrossBuilds(t *testing.T) {
	if testing.Short() || os.Getenv("WEFT_MANIFEST_HASH_HELPER") == "1" {
		t.Skip("builds the package twice")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain on PATH")
	}
	run := func(flags ...string) string {
		t.Helper()
		args := append([]string{"test"}, flags...)
		args = append(args, "-run", "^TestManifestHashHelper$", "-count=1", "-v", ".")
		cmd := exec.Command(goBin, args...)
		cmd.Env = append(os.Environ(), "WEFT_MANIFEST_HASH_HELPER=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if h, ok := strings.CutPrefix(line, "MANIFEST "); ok {
				return h
			}
		}
		t.Fatalf("go %v printed no hash:\n%s", args, out)
		return ""
	}
	inlined, plain := run(), run("-gcflags=all=-l")
	here, b := manifestHash(t)
	if inlined != plain || inlined != here {
		t.Errorf("manifest hash: inlined build %s, -gcflags=all=-l %s, this test %s\n%s", inlined, plain, here, b)
	}
}
