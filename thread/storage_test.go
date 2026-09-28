package thread_test

import (
	"testing"

	"github.com/weftgo/weft/thread"
	"github.com/weftgo/weft/thread/threadtest"
)

// Memory runs the shared conformance table — the durable backends run
// the same one, and agreement with Memory is the reference behaviour.
func TestMemoryConformance(t *testing.T) {
	threadtest.Run(t, func(t *testing.T) thread.Storage { return thread.Memory() })
}
