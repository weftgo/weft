package otel_test

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/otel"
	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/wefttest"
)

// ContentThread is the kind Redact sees for a weft/thread session
// record's content fields — who decided, a deny reason, a steer's text
// (record contract §4, kind thread). Here a content-on Local sink
// redacts the decider's name; a content-off destination would receive
// the decision without either field, its ids and enums intact.
func ExampleContentThread() {
	dir, err := os.MkdirTemp("", "weft-otel-thread-")
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	var mu sync.Mutex
	var saw []string
	redact := func(kind weft.ContentKind, s string) string {
		if kind == otel.ContentThread {
			mu.Lock()
			saw = append(saw, s)
			mu.Unlock()
			if s == "agent-7" {
				return "[who]"
			}
		}
		return s
	}
	ctx := context.Background()
	p, err := otel.Start(ctx, otel.NoGlobal(), otel.NoEnv(), otel.Heartbeat(0),
		otel.Local(filepath.Join(dir, "weft.db")), otel.Content(otel.ContentConfig{Redact: redact}))
	if err != nil {
		log.Fatal(err)
	}
	refund := weft.Tool("refund", "Refund an order.", func(context.Context, struct{}) (string, error) {
		return "refunded", nil
	}, weft.RequireApproval())
	agt := weft.New(wefttest.Script(
		wefttest.ToolCalls(wefttest.Call{Name: "refund", ID: "r1"}),
		wefttest.Say("I need a photo first."),
	), refund, weft.LoggerProvider(p.LoggerProvider()), weft.TracerProvider(p.TracerProvider()))
	s, err := thread.Create(ctx, thread.Memory(), agt)
	if err != nil {
		log.Fatal(err)
	}
	turn, _ := s.Send(ctx, weft.User("refund it"))
	_, _ = turn.Wait()
	d := thread.Deny("r1", "need a photo")
	d.Who = "agent-7"
	_, _ = s.Decide(ctx, d)
	_ = s.WaitIdle(ctx)
	_ = s.Close(ctx)
	_ = p.Shutdown(ctx)
	slices.Sort(saw)
	fmt.Println(saw)
	// Output: [agent-7 need a photo]
}
