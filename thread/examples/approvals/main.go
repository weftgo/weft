// Command approvals walks one weft/thread session through the
// approval flow v0.2 exists for (ADR 0021): a gated call parks, the
// process "restarts" — the session is reopened from the JSONL file —
// a decision arrives signed over the challenge the session minted,
// and the conversation resumes under it. The session requires signed
// decisions, a rule its header keeps across the restart. Everything
// offline through a scripted model; every id deterministic.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/jsonl"
	"github.com/weftgo/weft/wefttest"
)

// secret is the demo key the "web UI" holds; a real deployment keeps
// it in that process only — the session file never sees key bytes.
var secret = []byte("example-keyring-secret-32-bytes-ok!")

func main() {
	dir := flag.String("dir", filepath.Join(os.TempDir(), "weft-approvals-example"), "directory for the session files")
	flag.Parse()
	if err := run(os.Stdout, *dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func p(w io.Writer, a ...any) error {
	_, err := fmt.Fprintln(w, a...)
	return err
}

func run(w io.Writer, dir string) error {
	ctx := context.Background()
	ring, err := thread.NewKeyring(thread.Key{ID: "k1", Secret: secret, Active: true})
	if err != nil {
		return err
	}

	st, err := jsonl.Open(dir)
	if err != nil {
		return err
	}

	// The gated tool: deploying needs a human.
	agent := weft.New(
		wefttest.Script(
			wefttest.ToolCalls(wefttest.Call{Name: "deploy", Args: `{"env":"prod"}`}),
			wefttest.Say("Deployed. The change is live."),
		),
		weft.Name("approvals-example"),
		weft.Tool("deploy", "Deploy the service.",
			func(ctx context.Context, in struct {
				Env string `json:"env"`
			}) (string, error) {
				call, _ := weft.CallFromContext(ctx)
				return "deployed to " + in.Env + " (approved: " + fmt.Sprint(call.Approved) + ")", nil
			},
			weft.RequireApproval()),
	)

	if err := p(w, "== a session parks a gated call"); err != nil {
		return err
	}
	s, err := thread.Create(ctx, st, agent, thread.WithKeyring(ring), thread.RequireSigned())
	if err != nil {
		return err
	}
	turn, err := s.Send(ctx, weft.User("Deploy to prod."))
	if err != nil {
		return err
	}
	res, err := turn.Wait()
	if err != nil {
		return err // a pending turn is a success
	}
	if err := p(w, "parked:", res.Pending[0].Name, "as", res.Pending[0].ID); err != nil {
		return err
	}

	if err := p(w, "== the process restarts; the pending request survived"); err != nil {
		return err
	}
	// RequireSigned is not passed again: the session's header carries
	// it, so the reopened session still refuses an unsigned Decide.
	reopened, err := thread.Open(ctx, st, s.ID(), agent, thread.WithKeyring(ring))
	if err != nil {
		return err
	}
	pending := reopened.Pending()
	if err := p(w, "pending after reopen:", len(pending)); err != nil {
		return err
	}
	if _, err := reopened.Decide(ctx, thread.Approve(pending[0].CallID)); err != nil {
		if err := p(w, "unsigned:", errors.Is(err, thread.ErrSignatureRequired)); err != nil {
			return err
		}
	}

	if err := p(w, "== the UI asks for a challenge and signs a decision"); err != nil {
		return err
	}
	challenge, err := reopened.Request(pending[0].CallID)
	if err != nil {
		return err
	}
	if err := p(w, "challenge: call", challenge.CallID, "key", challenge.KeyID); err != nil {
		return err
	}
	// The signing side holds the ring: Sign looks up the key the
	// challenge names. (A signer holding one key of its own uses
	// Key.Sign.)
	decision, err := ring.Sign(challenge, thread.Approve(pending[0].CallID))
	if err != nil {
		return err
	}
	resume, err := reopened.DecideSigned(ctx, decision)
	if err != nil {
		return err
	}
	resumed, err := resume.Wait()
	if err != nil {
		return err
	}
	if err := p(w, "resumed:", resumed.Text()); err != nil {
		return err
	}

	if err := p(w, "== a replayed signature fails closed"); err != nil {
		return err
	}
	if _, err := reopened.DecideSigned(ctx, decision); err != nil {
		if err := p(w, "replay:", err); err != nil {
			return err
		}
	}

	if err := p(w, "== the audit trail"); err != nil {
		return err
	}
	for _, e := range reopened.Audit() {
		switch e := e.(type) {
		case thread.ApprovalRequestEntry:
			if err := p(w, "request", e.CallID, "on", e.Tool); err != nil {
				return err
			}
		case thread.ApprovalDecisionEntry:
			if err := p(w, "decision", e.CallID, e.Outcome, "via", e.Via); err != nil {
				return err
			}
		case thread.ApprovalAuditEntry:
			line := []any{"audit", e.Step, e.Outcome}
			if e.Detail != "" {
				line = append(line, "("+e.Detail+")")
			}
			if err := p(w, line...); err != nil {
				return err
			}
		}
	}
	return nil
}
