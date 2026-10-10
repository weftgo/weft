package scope

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread"
)

// PageURL's sanitising rule (plan G3): Referer first, else Origin + "/";
// http and https only; userinfo, query and fragment dropped; a URL over
// 1024 bytes dropped whole.
func TestPageURLSanitises(t *testing.T) {
	long := "https://app.example/" + strings.Repeat("a", 1010)
	for _, c := range []struct {
		name, referer, origin, want string
	}{
		{"referer", "https://app.example/chat/42", "", "https://app.example/chat/42"},
		{"referer wins over origin", "https://app.example/chat", "https://other.example", "https://app.example/chat"},
		{"query and fragment dropped", "https://app.example/chat?token=s3cret&x=1#weft_scope=pub_x", "", "https://app.example/chat"},
		{"userinfo dropped", "https://user:pass@app.example:8443/p", "", "https://app.example:8443/p"},
		{"scheme case folded", "HTTPS://app.example/p", "", "https://app.example/p"},
		{"a bare origin as referer gains /", "http://localhost:5173", "", "http://localhost:5173/"},
		{"origin + /", "", "http://localhost:5173", "http://localhost:5173/"},
		{"bad referer falls back to origin", "javascript:alert(1)", "https://app.example", "https://app.example/"},
		{"opaque origin", "", "null", ""},
		{"origin with a path is no origin", "", "https://app.example/x", ""},
		{"file scheme", "file:///etc/passwd", "", ""},
		{"data scheme", "data:text/html,hi", "", ""},
		{"relative referer", "/chat", "", ""},
		{"no host", "https:///chat", "", ""},
		{"spaces", "https://app.example/a b", "", ""},
		{"control byte", "https://app.example/a\x01", "", ""},
		{"too long", long, "", ""},
		{"neither", "", "", ""},
	} {
		r := httptest.NewRequest(http.MethodPost, "/chat", nil)
		if c.referer != "" {
			r.Header.Set("Referer", c.referer)
		}
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := PageURL(r); got != c.want {
			t.Errorf("%s: PageURL = %q, want %q", c.name, got, c.want)
		}
	}
}

// The done line's Go half (plan G3): a run its handler starts under
// scope.Header carries weft.page_url — on its RunStart's metadata and
// its invoke_agent span — with no option passed by the handler; a
// thread Send under the request's context carries it too, beside the
// session's own keys; PageURLFromContext reads it in the handler.
func TestHeaderStampsPageURLOnEveryRun(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	var runMD []map[string]string
	agt := core.New(wefttest.Script(wefttest.Say("one"), wefttest.Say("two")),
		core.Name("support"), core.TracerProvider(tp),
		core.Tap(func(ctx context.Context, ev core.Event) {
			if _, ok := ev.(core.RunStart); ok {
				runMD = append(runMD, core.MetadataFromContext(ctx))
			}
		}))
	st := thread.Memory()
	var fromCtx string
	h := Header(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fromCtx, _ = PageURLFromContext(r.Context())
		if _, err := agt.Generate(r.Context(), core.Prompt("hi")); err != nil {
			t.Error(err)
		}
		s, err := thread.Create(r.Context(), st, agt, thread.PublicID("pub_x"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = s.Close(r.Context()) }()
		turn, err := s.Send(r.Context(), core.User("hi"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := turn.Wait(); err != nil {
			t.Error(err)
		}
	}), func(*http.Request) Scope { return Scope{PublicID: "pub_x"} })
	req := httptest.NewRequest(http.MethodPost, "/chat", nil)
	req.Header.Set("Referer", "https://app.example/support?q=secret#frag")
	h.ServeHTTP(httptest.NewRecorder(), req)

	const want = "https://app.example/support"
	if fromCtx != want {
		t.Errorf("PageURLFromContext = %q, want %q", fromCtx, want)
	}
	if len(runMD) != 2 {
		t.Fatalf("%d runs, want 2 (Generate and the session's turn)", len(runMD))
	}
	for i, md := range runMD {
		if md[PageURLKey] != want {
			t.Errorf("run %d metadata %s = %q, want %q", i, PageURLKey, md[PageURLKey], want)
		}
	}
	if runMD[1]["weft.public_id"] != "pub_x" || runMD[1]["weft.session.id"] == "" {
		t.Errorf("the session's keys beside page_url: %v", runMD[1])
	}
	spans := 0
	for _, sp := range rec.Ended() {
		if !strings.HasPrefix(sp.Name(), "invoke_agent") {
			continue
		}
		spans++
		got := ""
		for _, kv := range sp.Attributes() {
			if string(kv.Key) == PageURLKey {
				got = kv.Value.AsString()
			}
		}
		if got != want {
			t.Errorf("span %s: %s = %q, want %q", sp.Name(), PageURLKey, got, want)
		}
	}
	if spans != 2 {
		t.Errorf("%d invoke_agent spans, want 2", spans)
	}
}

// No page, no stamp: a request without Referer or Origin leaves the
// context as it was, and a run's own Metadata wins over the stamp.
func TestHeaderPageURLAbsentAndOverridden(t *testing.T) {
	var got []map[string]string
	agt := core.New(wefttest.Script(wefttest.Say("a"), wefttest.Say("b")),
		core.Tap(func(ctx context.Context, ev core.Event) {
			if _, ok := ev.(core.RunStart); ok {
				got = append(got, core.MetadataFromContext(ctx))
			}
		}))
	var opts [][]core.RunOption
	h := Header(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := PageURLFromContext(r.Context()); ok && len(opts) == 0 {
			t.Error("PageURLFromContext reported a page for a request without one")
		}
		_, _ = agt.Generate(r.Context(), append(opts[len(opts)-1], core.Prompt("x"))...)
	}), func(*http.Request) Scope { return Scope{} })
	opts = append(opts, nil)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/chat", nil))
	opts = append(opts, []core.RunOption{core.Metadata(map[string]string{PageURLKey: "https://mine.example/"})})
	req := httptest.NewRequest(http.MethodPost, "/chat", nil)
	req.Header.Set("Origin", "https://app.example")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(got) != 2 {
		t.Fatalf("%d runs", len(got))
	}
	if _, ok := got[0][PageURLKey]; ok {
		t.Errorf("no page: metadata = %v", got[0])
	}
	if got[1][PageURLKey] != "https://mine.example/" {
		t.Errorf("the run's own Metadata must win: %v", got[1])
	}
}
