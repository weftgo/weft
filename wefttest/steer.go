package wefttest

import (
	"context"
	"sync"

	"github.com/weftgo/weft"
)

// Steers is a deterministic steering source: the messages registered for
// a step are delivered at that step's drain points, and nothing drains
// anywhere else. Keyed by step — not by time or channel state — so a
// replayed run (Replay) cannot diverge from its recording: the
// delivered messages are part of the next model request's transcript,
// and a steer that differs from the recorded one misses its fixture
// loudly (ErrNoFixture) instead of drifting (ADR 0017, ADR 0019 §6).
// For the same reason a live test steers through this type too: the
// schedule the test ran is the schedule the file shows.
//
//	src := wefttest.NewSteers().At(0, weft.User("prefer the left door"))
//	res, err := agt.Generate(ctx, weft.Prompt("which door?"), src.Option())
type Steers struct {
	mu     sync.Mutex
	byStep map[int][]weft.Message
}

// NewSteers returns an empty Steers source; register deliveries with At.
func NewSteers() *Steers {
	return &Steers{byStep: map[int][]weft.Message{}}
}

// At registers the messages delivered at step's drain points and
// returns the source, so registrations chain. Registering no messages
// clears the step. The messages are copied; the caller's slice is not
// retained.
func (s *Steers) At(step int, msgs ...weft.Message) *Steers {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(msgs) == 0 {
		delete(s.byStep, step)
		return s
	}
	s.byStep[step] = append([]weft.Message(nil), msgs...)
	return s
}

// Steer is the SteerFunc the loop drains; see Option. The returned
// messages are a copy, so the run's transcript and the source's
// registration never alias.
func (s *Steers) Steer(_ context.Context, at weft.SteerPoint) []weft.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	msgs := s.byStep[at.Step]
	if len(msgs) == 0 {
		return nil
	}
	return append([]weft.Message(nil), msgs...)
}

// Option returns the run option installing this source:
// Steering(src.Steer) packaged, so tests pass src.Option() beside
// Prompt and the wiring cannot drift from the core's shape.
func (s *Steers) Option() weft.RunOption { return weft.Steering(s.Steer) }
