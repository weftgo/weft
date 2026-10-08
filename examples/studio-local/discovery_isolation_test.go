package main

import (
	"os"
	"testing"
)

// TestMain turns the discovery file off for this package's tests: no
// test run joins (and pours its runs into) a Studio the developer
// happens to be running. Discovery is plan B3's otel.Install rung.
func TestMain(m *testing.M) {
	_ = os.Setenv("WEFT_DISCOVERY", "off")
	os.Exit(m.Run())
}
