package pool

import "time"

// shortenNestedEnd sets p's endNested bound, for the tests that wait it
// out.
func shortenNestedEnd(p *Pool, d time.Duration) { p.nestedEnd = d }
