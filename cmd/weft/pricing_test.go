package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --pricing / WEFT_PRICING become studio.Pricing and the banner's line:
// the flag wins over the variable; neither is the shipped table (no
// option); a file that does not load is an error naming where it came
// from, the path and the field.
func TestPricingOptions(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	envFile := write("env.json", `{"dated":"2026-09-01","models":[]}`)
	flagFile := write("flag.json", `{"dated":"2026-09-02","models":[{"provider":"acme","model_glob":"*","input":1,"output":2}]}`)
	bad := write("bad.json", `{"dated":"2026-09-02","models":[{"provider":"acme","model_glob":"*","output":2}]}`)

	t.Setenv("WEFT_PRICING", "")
	if opts, note, err := pricingOptions(""); err != nil || len(opts) != 0 || note != "" {
		t.Errorf("no pricing file = %d opts, %q, %v; want the shipped table", len(opts), note, err)
	}
	t.Setenv("WEFT_PRICING", envFile)
	if opts, note, err := pricingOptions(""); err != nil || len(opts) != 1 || !strings.Contains(note, envFile+" (WEFT_PRICING), dated 2026-09-01, 0 models") {
		t.Errorf("WEFT_PRICING = %d opts, %q, %v", len(opts), note, err)
	}
	if opts, note, err := pricingOptions(flagFile); err != nil || len(opts) != 1 || !strings.Contains(note, flagFile+" (--pricing), dated 2026-09-02, 1 models") {
		t.Errorf("--pricing over WEFT_PRICING = %d opts, %q, %v; want the flag's file", len(opts), note, err)
	}
	if _, _, err := pricingOptions(bad); err == nil || !strings.Contains(err.Error(), "--pricing") ||
		!strings.Contains(err.Error(), bad) || !strings.Contains(err.Error(), "models[0].input") {
		t.Errorf("--pricing bad.json = %v, want an error naming --pricing, the path and models[0].input", err)
	}
	t.Setenv("WEFT_PRICING", bad)
	if _, _, err := pricingOptions(""); err == nil || !strings.Contains(err.Error(), "WEFT_PRICING") || !strings.Contains(err.Error(), bad) {
		t.Errorf("WEFT_PRICING=bad.json = %v", err)
	}
	// weft studio refuses to start on it: exit 1, the error on stderr.
	var out, errb bytes.Buffer
	code := run([]string{"studio", "--db", "sqlite://" + filepath.Join(dir, "w.db"), "--addr", "127.0.0.1:0", "--open=false"}, &out, &errb)
	if code != 1 || !strings.Contains(errb.String(), bad) || !strings.Contains(errb.String(), "models[0].input") {
		t.Errorf("weft studio under a malformed WEFT_PRICING = %d %q, want exit 1 naming the path and the field", code, errb.String())
	}
}

// weft studio --pricing reaches api/meta as pricing {source file, path
// (the server token's), dated, models}.
func TestStudioPricingInMeta(t *testing.T) {
	t.Setenv("WEFT_PRICING", "")
	file := filepath.Join(t.TempDir(), "prices.json")
	if err := os.WriteFile(file, []byte(`{"dated":"2026-09-03","models":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	opts, _, err := pricingOptions(file)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := newServer("sqlite://"+filepath.Join(t.TempDir(), "weft.db"), "tok", opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/meta", nil)
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	want := `"pricing":{"source":"file","path":"` + file + `","dated":"2026-09-03","models":0}`
	if !strings.Contains(string(b), want) || !strings.Contains(string(b), `"pricing"`) {
		t.Errorf("meta = %s, want %s", b, want)
	}
}
