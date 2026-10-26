package scope

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// U22 (user decision 2): a scope's flow is run metadata. Header stamps
// weft.flow on every run its handler starts — from the scope it sets,
// else from the request's own Weft-Scope header — and WithFlow does the
// same for any context; a run's own Metadata option wins.
func TestFlowIsStampedAsRunMetadata(t *testing.T) {
	var got []string
	agt := core.New(wefttest.Script(wefttest.Say("a"), wefttest.Say("b"), wefttest.Say("c"), wefttest.Say("d"), wefttest.Say("e")),
		core.Name("checkout"),
		core.Tap(func(ctx context.Context, ev core.Event) {
			if _, ok := ev.(core.RunStart); ok {
				got = append(got, core.MetadataFromContext(ctx)[FlowKey])
			}
		}))
	run := func(ctx context.Context, opts ...core.RunOption) {
		t.Helper()
		if _, err := agt.Generate(ctx, append([]core.RunOption{core.Prompt("hi")}, opts...)...); err != nil {
			t.Fatal(err)
		}
	}
	serve := func(s Scope, reqScope string, opts ...core.RunOption) {
		h := Header(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { run(r.Context(), opts...) }),
			func(*http.Request) Scope { return s })
		req := httptest.NewRequest(http.MethodPost, "/chat", nil)
		if reqScope != "" {
			req.Header.Set(HeaderName, reqScope)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	serve(Scope{PublicID: "pub_1", FlowID: "f_checkout"}, ";flow=f_other")                     // the handler's scope wins
	serve(Scope{PublicID: "pub_1"}, "pub_1;flow=f_forwarded")                                  // the request's own header
	serve(Scope{PublicID: "pub_1"}, "")                                                        // no flow: none stamped
	serve(Scope{FlowID: "f_checkout"}, "", core.Metadata(map[string]string{FlowKey: "f_own"})) // the run's own option wins
	run(WithFlow(context.Background(), "f_ctx"))
	want := []string{"f_checkout", "f_forwarded", "", "f_own", "f_ctx"}
	if len(got) != len(want) {
		t.Fatalf("%d runs, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("run %d: weft.flow = %q, want %q", i, got[i], want[i])
		}
	}
	if ctx := context.Background(); WithFlow(ctx, "") != ctx {
		t.Error("WithFlow with no flow should return ctx itself")
	}
}

// A tag or agent scope stands without a public id, and a page's
// data-scope may start with the key ("tag.arena.batch=…").
func TestTagScopeStandsAlone(t *testing.T) {
	s := Parse("tag.arena.batch=cli-1;agent=director")
	if s.PublicID != "" || s.Agent != "director" || s.Tags["arena.batch"] != "cli-1" || s.IsZero() {
		t.Fatalf("Parse = %#v", s)
	}
	if got := s.String(); got != ";agent=director;tag.arena.batch=cli-1" {
		t.Errorf("String = %q", got)
	}
	if !(Scope{Tags: map[string]string{}}).IsZero() || !(Scope{}).Equal(Scope{Tags: map[string]string{}}) {
		t.Error("an empty Tags map is the zero scope")
	}
}
