package main

import "testing"

// The example runs end to end offline: two servers, two prefixed
// imports, one scripted agent run, and the manifest printed.
func TestClientRun(t *testing.T) {
	if err := run(); err != nil {
		t.Fatal(err)
	}
}
