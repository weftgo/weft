package pool

import (
	"time"

	"github.com/weftgo/weft/thread"
)

// shortenNestedEnd sets p's endNested bound, for the tests that wait it
// out.
func shortenNestedEnd(p *Pool, d time.Duration) { p.nestedEnd = d }

// OnStarting sets p's hook in run between the running mark and the
// start of the child's turn — the window a Forward must be refused in —
// handing it the pool, the delegation's parent session and its
// receipt id. For the external tests (TestNestedRandomChains).
func OnStarting(p *Pool, f func(p *Pool, parent *thread.Session, receipt string)) {
	p.testHookStarting = func(d *delegate) { f(p, d.parent, d.receipt) }
}
