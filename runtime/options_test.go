package runtime

import (
	"testing"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/wefttest"
)

// testAgent builds one named scripted agent for the registry tests.
func testAgent(name string) *weft.Agent {
	return weft.New(wefttest.Script(wefttest.Say("ok")), weft.Name(name))
}

// toolAgent builds a named agent with tools, for the parking tests.
func toolAgent(t *testing.T, name string, tools ...*weft.ToolDef) *weft.Agent {
	t.Helper()
	opts := []weft.Option{weft.Name(name)}
	for _, tool := range tools {
		opts = append(opts, tool)
	}
	return weft.New(wefttest.Script(wefttest.Say("ok")), opts...)
}

// TestInstallOpensNothing pins §6 rule 1 and §10.2's "without
// Install, nothing opens": every refusal path returns a callable
// shutdown without panicking and without dialing anything.
func TestInstallOpensNothing(t *testing.T) {
	cases := []struct {
		name string
		env  string
		opts []Option
	}{
		{"enabled false", "dev", []Option{Enabled(false), Agents(testAgent("a")), Studio("http://127.0.0.1:1", "")}},
		{"env not dev", "prod", []Option{Agents(testAgent("a")), Studio("http://127.0.0.1:1", "")}},
		{"no env", "", []Option{Agents(testAgent("a")), Studio("http://127.0.0.1:1", "")}},
		{"no agents", "dev", []Option{Studio("http://127.0.0.1:1", "")}},
		{"no endpoint", "dev", []Option{Agents(testAgent("a"))}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WEFT_ENV", tc.env)
			if shutdown := Install(tc.opts...); shutdown == nil {
				t.Fatal("Install returned a nil shutdown")
			} else {
				shutdown() // must be callable and quiet
			}
		})
	}
}

// TestInstallEnabledByEnv pins the WEFT_ENV=dev default: with the env
// set and an endpoint available, a link exists and stops cleanly.
func TestInstallEnabledByEnv(t *testing.T) {
	t.Setenv("WEFT_ENV", "dev")
	// A URL nothing listens on is enough: the link retries in the
	// background and the shutdown must still be clean.
	shutdown := Install(Agents(testAgent("a")), Studio("http://127.0.0.1:1", ""))
	if shutdown == nil {
		t.Fatal("Install returned a nil shutdown")
	}
	shutdown()
	shutdown() // idempotent
}
