package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weftgo/weft"
)

func TestParseFixtures(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"single response", "data: a\n\ndata: b\n", []string{"data: a\n\ndata: b\n"}},
		{
			"two responses",
			"data: a\n\ndata: [DONE]\n\n=== response 2 ===\ndata: b\n",
			[]string{"data: a\n\ndata: [DONE]\n\n", "data: b\n"},
		},
		{
			"leading separator",
			"=== response 1 ===\ndata: a\n",
			[]string{"data: a\n"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseFixtures(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("responses = %d, want %d: %q", len(got), len(tc.want), got)
			}
			for i := range got {
				if strings.TrimSpace(got[i]) != strings.TrimSpace(tc.want[i]) {
					t.Errorf("response %d:\n got  %q\n want %q", i+1, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestFixtureServerServesInOrderThenFails(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "case.sse")
	body := "data: one\n\n=== response 2 ===\ndata: two\n"
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := FixtureServer(t, file)

	get := func() (string, int) {
		resp, err := http.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return string(b), resp.StatusCode
	}
	if b, code := get(); code != 200 || strings.TrimSpace(b) != "data: one" {
		t.Errorf("request 1: code=%d body=%q", code, b)
	}
	if b, code := get(); code != 200 || strings.TrimSpace(b) != "data: two" {
		t.Errorf("request 2: code=%d body=%q", code, b)
	}
	if b, code := get(); code != 500 || !strings.Contains(b, "exhausted") {
		t.Errorf("request 3: code=%d body=%q, want 500 exhausted", code, b)
	}
}

func TestContractDetectorFires(t *testing.T) {
	var d contractDetector
	d.check(nil)
	d.check(context.Canceled)
	if d.violated {
		t.Fatal("false positive on a non-contract error")
	}
	d.check(fmt.Errorf("model stream: %w: stream ended without ModelFinish", weft.ErrModelContract))
	if !d.violated {
		t.Fatal("detector missed a wrapped ErrModelContract")
	}
	if !errors.Is(weft.ErrModelRequestsDenied, weft.ErrModelRequestsDenied) { // keep errors import honest
		t.Fatal("unreachable")
	}
}
