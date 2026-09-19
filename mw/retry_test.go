package mw

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// cappedErr carries a status and the response the retry-after header
// rides on — the vendor shape RetryAfter extracts structurally
// (mirrors mw_test's apiError, which the external test package keeps).
type cappedErr struct {
	StatusCode int
	Response   *http.Response
}

func (e *cappedErr) Error() string { return fmt.Sprintf("status %d", e.StatusCode) }

// MaxWait(0) means "no cap": a provider's retry-after ask is honoured
// however long it is, instead of failing fast wrapping
// ErrRetryAfterTooLong. delay is tested directly — sleeping the ask
// would be the only end-to-end form.
func TestMaxWaitZeroRemovesTheCap(t *testing.T) {
	err := &cappedErr{StatusCode: 429, Response: &http.Response{
		Header: http.Header{"Retry-After": []string{"120"}},
	}}
	capped := &retryConfig{maxWait: defaultMaxWait}
	if _, err := capped.delay(0, err, time.Now()); !errors.Is(err, ErrRetryAfterTooLong) {
		t.Errorf("default cap: err = %v, want ErrRetryAfterTooLong", err)
	}
	uncapped := &retryConfig{maxWait: 0} // what MaxWait(0) sets
	d, derr := uncapped.delay(0, err, time.Now())
	if derr != nil || d != 120*time.Second {
		t.Errorf("MaxWait(0): delay = %v, err = %v; want the ask honoured, no error", d, derr)
	}
}
