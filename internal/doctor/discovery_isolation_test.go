package doctor_test

import (
	"os"
	"testing"
)

// TestMain turns the discovery file off for this package's tests: no
// test run joins (and pours its runs into) a Studio the developer
// happens to be running — otel.Install/Start read the file when
// WEFT_STUDIO_URL is unset (plan B3).
func TestMain(m *testing.M) {
	_ = os.Setenv("WEFT_DISCOVERY", "off")
	os.Exit(m.Run())
}
