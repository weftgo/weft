package doctor_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/weftgo/weft/internal/doctor"
	"github.com/weftgo/weft/studio"
)

// ExampleRun checks a Studio with nothing connected to it: one line per
// check, each read from its /api/meta. With no runtime connected the
// runtimes line warns, and its continuation lines name the variables
// this shell lacks (WEFT_ENV, WEFT_STUDIO_URL).
func ExampleRun() {
	dir, _ := os.MkdirTemp("", "doctor")
	defer func() { _ = os.RemoveAll(dir) }()
	srv := studio.New(studio.Open(filepath.Join(dir, "weft.db")))
	defer func() { _ = srv.Close() }()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var out strings.Builder
	err := doctor.Run(context.Background(), &out, ts.URL, "", func(string) string { return "" })
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if f := strings.Fields(line); !strings.HasPrefix(line, " ") {
			fmt.Println(f[0], f[1])
		} else if strings.Contains(line, "WEFT_ENV is unset") {
			fmt.Println("  names WEFT_ENV")
		}
	}
	fmt.Println("err:", err)
	// Output:
	// ok studio
	// ok token
	// ok db
	// ok content
	// warn runtimes
	//   names WEFT_ENV
	// ok panel
	// ok weft.json
	// err: <nil>
}
