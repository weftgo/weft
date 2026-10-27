package studio

import (
	"encoding/json"
	"testing"
)

// TestCostParityGolden pins one cost rule for a set (P10b-pre-web-fix):
// the session row's figure (tally, cost_partial) beside the turn rows it
// sums, for the three probe cases — testdata/api/cost-parity.golden.json,
// which the web's parity.test feeds to Studio's partialView and to the
// panel's atLeastCost, so both surfaces say the same figure as Go.
func TestCostParityGolden(t *testing.T) {
	running := "a subagent run's cost is not known yet: r/0/c1 (running: usage arrives at run end)"
	known := func(usd float64) runCost {
		return runCost{own: cost{known: true, usd: usd, cache: cacheExact}, delegated: cost{known: true}}
	}
	ownOnly := func(usd float64) runCost {
		return runCost{own: cost{known: true, usd: usd, cache: cacheExact}, delegated: cost{badge: costUnknown, reason: running, why: running, cause: running}}
	}
	scripted := runCost{own: cost{known: true, badge: costScripted, reason: scriptedReason, cache: cacheExact}, delegated: cost{known: true}}
	unpriced := runCost{own: cost{badge: costUnpriced, reason: "acme/x has no price", models: []string{"acme/x"}, cache: cacheExact}, delegated: cost{known: true}}
	type row struct {
		costFields
		delegatedFields
	}
	type parityCase struct {
		Name    string     `json:"name"`
		Session costFields `json:"session"`
		Turns   []row      `json:"turns"`
	}
	var out struct {
		Cases []parityCase `json:"cases"`
	}
	for _, c := range []struct {
		name  string
		parts []runCost
	}{
		{"own known, delegated unknown, beside a known turn", []runCost{known(0.004), ownOnly(0.002)}},
		{"two $0 turns and one unknown", []runCost{scripted, scripted, unpriced}},
		{"every turn own-known, delegated unknown", []runCost{ownOnly(0.004), ownOnly(0.002)}},
	} {
		pc := parityCase{Name: c.name, Session: tally(c.parts, "turn", false).fields()}
		for _, p := range c.parts {
			pc.Turns = append(pc.Turns, row{p.own.fields(), p.delegated.delegatedFields()})
		}
		out.Cases = append(out.Cases, pc)
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "cost-parity.golden.json", string(b))
}
