package main

import (
	"net"
	"testing"
)

// TestListenBesideAStudio (I1-fix): run bare beside a `weft studio`
// holding the default port, the demo moves to the fallback instead of
// failing "address in use"; under weft dev (WEFT_STUDIO_URL) it takes
// the fallback outright; an -addr the user gave is never moved.
func TestListenBesideAStudio(t *testing.T) {
	held, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Close() }()
	fb, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fallback := fb.Addr().String()
	_ = fb.Close()
	old := demoFallbackAddr
	demoFallbackAddr = fallback
	t.Cleanup(func() { demoFallbackAddr = old })

	t.Run("default port taken", func(t *testing.T) {
		t.Setenv("WEFT_STUDIO_URL", "")
		ln, addr, err := listen(held.Addr().String(), false)
		if err != nil {
			t.Fatal(err)
		}
		_ = ln.Close()
		if addr != fallback {
			t.Errorf("addr = %s, want the fallback %s", addr, fallback)
		}
	})
	t.Run("WEFT_STUDIO_URL", func(t *testing.T) {
		t.Setenv("WEFT_STUDIO_URL", "http://127.0.0.1:7331")
		ln, addr, err := listen("127.0.0.1:0", false)
		if err != nil {
			t.Fatal(err)
		}
		_ = ln.Close()
		if addr != fallback {
			t.Errorf("addr = %s, want the fallback %s", addr, fallback)
		}
	})
	t.Run("explicit -addr", func(t *testing.T) {
		t.Setenv("WEFT_STUDIO_URL", "http://127.0.0.1:7331")
		if ln, _, err := listen(held.Addr().String(), true); err == nil {
			_ = ln.Close()
			t.Error("an explicit, taken -addr was moved, want its error")
		}
	})
}
