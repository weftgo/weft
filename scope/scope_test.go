package scope

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
)

// golden is studio/testdata/scope.golden.json: the one fixture the web
// client's scope.test.ts reads too, so the two serialisers cannot drift.
type golden struct {
	Roundtrip, Serialize, Parse []struct {
		Scope  Scope  `json:"scope"`
		String string `json:"string"`
	}
}

func readGolden(t *testing.T) golden {
	t.Helper()
	raw, err := os.ReadFile("../studio/testdata/scope.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Roundtrip) == 0 || len(g.Serialize) == 0 || len(g.Parse) == 0 {
		t.Fatal("golden has an empty section")
	}
	return g
}

func TestGoldenRoundTrip(t *testing.T) {
	g := readGolden(t)
	flow := false
	for _, c := range g.Roundtrip {
		if got := c.Scope.String(); got != c.String {
			t.Errorf("%#v.String() = %q, want %q", c.Scope, got, c.String)
		}
		if got := Parse(c.String); got != c.Scope {
			t.Errorf("Parse(%q) = %#v, want %#v", c.String, got, c.Scope)
		}
		flow = flow || c.Scope.FlowID != ""
	}
	if !flow {
		t.Error("the golden has no round-trip case with a flow field (C3's Done line)")
	}
}

func TestGoldenSerializeOnly(t *testing.T) {
	for _, c := range readGolden(t).Serialize {
		if got := c.Scope.String(); got != c.String {
			t.Errorf("%#v.String() = %q, want %q", c.Scope, got, c.String)
		}
	}
}

func TestGoldenParseIsLenient(t *testing.T) {
	for _, c := range readGolden(t).Parse {
		if got := Parse(c.String); got != c.Scope {
			t.Errorf("Parse(%q) = %#v, want %#v", c.String, got, c.Scope)
		}
	}
}

// A string that is not valid UTF-8 (Go's lone surrogate: what
// encodeURIComponent refuses) has only %, ; and = encoded, and still
// round-trips — the web side's fallback.
func TestInvalidUTF8EncodesOnlyTheDelimiters(t *testing.T) {
	s := Scope{PublicID: "a;\xff", RunID: "=%"}
	if got, want := s.String(), "a%3B\xff;run=%3D%25"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if got := Parse(s.String()); got != s {
		t.Errorf("round trip = %#v, want %#v", got, s)
	}
}

func TestIsZero(t *testing.T) {
	if !(Scope{}).IsZero() {
		t.Error("the zero Scope is not zero")
	}
	for _, s := range []Scope{{PublicID: "p"}, {SessionID: "s"}, {FlowID: "f"}, {RunID: "r"}} {
		if s.IsZero() {
			t.Errorf("%#v is zero", s)
		}
	}
}

func TestHeaderSetsTheScopeBeforeTheBody(t *testing.T) {
	h := Header(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("data: 1\n\n"))
		w.(http.Flusher).Flush() // a streaming handler: the header leaves with the first flush
		w.Header().Set(HeaderName, "too late")
	}), func(*http.Request) Scope { return Scope{PublicID: "pub_1", RunID: "r_2"} })
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := resp.Header.Get(HeaderName); got != "pub_1;run=r_2" {
		t.Errorf("%s = %q, want pub_1;run=r_2", HeaderName, got)
	}
	if got := resp.Header.Values("Access-Control-Expose-Headers"); !slices.Equal(got, []string{HeaderName}) {
		t.Errorf("Access-Control-Expose-Headers = %q, want [%s]", got, HeaderName)
	}
}

func TestHeaderSkipsAZeroScope(t *testing.T) {
	h := Header(http.NotFoundHandler(), func(*http.Request) Scope { return Scope{} })
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if _, ok := w.Header()[HeaderName]; ok {
		t.Errorf("a zero scope set %s = %q", HeaderName, w.Header().Get(HeaderName))
	}
	if got := w.Header().Values("Access-Control-Expose-Headers"); len(got) != 0 {
		t.Errorf("a zero scope exposed %q", got)
	}
}

// The expose list is appended to, never replaced, and never repeats
// Weft-Scope (or adds it beside a "*").
func TestExposeHeadersAppends(t *testing.T) {
	for _, tc := range []struct{ before, want []string }{
		{nil, []string{HeaderName}},
		{[]string{"X-Request-Id"}, []string{"X-Request-Id", HeaderName}},
		{[]string{"X-Request-Id, weft-scope"}, []string{"X-Request-Id, weft-scope"}},
		{[]string{"*"}, []string{"*"}},
	} {
		w := httptest.NewRecorder()
		for _, v := range tc.before {
			w.Header().Add("Access-Control-Expose-Headers", v)
		}
		Set(w, Scope{PublicID: "pub_1"})
		Set(w, Scope{PublicID: "pub_1", RunID: "r_1"})
		if got := w.Header().Values("Access-Control-Expose-Headers"); !slices.Equal(got, tc.want) {
			t.Errorf("before %q: expose = %q, want %q", tc.before, got, tc.want)
		}
	}
}

// Set inside a Header-wrapped handler replaces the middleware's scope
// (the dynamic case: the run id is known once the turn starts); a zero
// Set removes it.
func TestSetReplacesAndZeroRemoves(t *testing.T) {
	run := func(s Scope) http.Header {
		h := Header(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			Set(w, s)
			_, _ = w.Write([]byte("ok"))
		}), func(*http.Request) Scope { return Scope{PublicID: "pub_1"} })
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/run", nil))
		return w.Result().Header
	}
	if got := run(Scope{PublicID: "pub_1", RunID: "r_7"}).Values(HeaderName); !slices.Equal(got, []string{"pub_1;run=r_7"}) {
		t.Errorf("after Set: %s = %q, want [pub_1;run=r_7]", HeaderName, got)
	}
	if got := run(Scope{}).Values(HeaderName); len(got) != 0 {
		t.Errorf("after a zero Set: %s = %q, want none", HeaderName, got)
	}
}
