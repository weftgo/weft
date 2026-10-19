package core

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"
)

// panicEmitLogger is a logger provider's logger whose Emit panics.
type panicEmitLogger struct{ embedded.Logger }

func (panicEmitLogger) Emit(context.Context, log.Record)                    { panic("emit") }
func (panicEmitLogger) Enabled(context.Context, log.EnabledParameters) bool { return true }

// A record-build panic is core's own and its Warn line says so; a panic
// out of the logger provider's Emit is the provider's (logger_provider).
// Both are contained and counted (P9-pre-go-fix).
func TestRecordPanicIsLabelledByItsSource(t *testing.T) {
	var buf bytes.Buffer
	obs := &observer{log: slog.New(slog.NewTextHandler(&buf, nil)), elog: panicEmitLogger{}}
	r := &recorder{elog: obs.elog, obs: obs, runID: "r1"}
	ctx := context.Background()
	func() {
		defer r.contain(ctx, 0, "event")
		panic("built wrong") // core's own building of a record
	}()
	func() {
		defer r.contain(ctx, 0, "event")
		r.emitLog(ctx, log.Record{})
	}()
	out := buf.String()
	if !strings.Contains(out, "weft could not build a record") || !strings.Contains(out, "record=event") {
		t.Errorf("the build panic's Warn line is not core's own:\n%s", out)
	}
	if !strings.Contains(out, "provider=logger_provider") {
		t.Errorf("the Emit panic is not the logger provider's:\n%s", out)
	}
	if strings.Count(out, "provider=logger_provider") != 1 {
		t.Errorf("the build panic was blamed on the logger provider:\n%s", out)
	}
	if n := r.runPanics.Load(); n != 2 {
		t.Errorf("contained panics counted = %d, want 2", n)
	}
}
