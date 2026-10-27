package pool_test

import (
	"encoding/json"
	"testing"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
	"github.com/weftgo/weft/thread/pool"
)

// A pool tool's middleware reads "pool.Wrap" in the manifest, whatever
// the compiler inlined (P10b-inline-names: core.NameToolMiddleware).
func TestWrapNamedInManifest(t *testing.T) {
	p := pool.New(1)
	child := core.New(wefttest.Script(wefttest.Say("ok")), core.Name("child"))
	agt := core.New(wefttest.Script(), core.Name("lead"), p.MustWrap("delegate", "", child))
	b, err := core.Manifest(agt)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Agents []struct {
			Tools []struct {
				Middleware []core.MiddlewareInfo `json:"middleware"`
			} `json:"tools"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if m := doc.Agents[0].Tools[0].Middleware; len(m) != 1 || m[0].Name != "pool.Wrap" || m[0].Source != "" {
		t.Errorf("pool tool middleware = %+v, want pool.Wrap", m)
	}
}
