package linkreg

import (
	"fmt"
	"sync"
	"testing"
)

// reset empties the registry between tests (package state).
func reset(t *testing.T) {
	t.Helper()
	reg.mu.Lock()
	reg.links = nil
	reg.mu.Unlock()
	t.Cleanup(func() {
		reg.mu.Lock()
		reg.links = nil
		reg.mu.Unlock()
	})
}

func TestLinkregAddRemoveAll(t *testing.T) {
	reset(t)
	if got := All(); len(got) != 0 {
		t.Fatalf("empty registry = %v", got)
	}
	local := &Link{Local: "srv"}
	b := &Link{StudioURL: "http://b.test", Instance: "ib"}
	a := &Link{StudioURL: "http://a.test", Instance: "ia"}
	rmLocal := Add(local)
	rmB := Add(b)
	rmA := Add(a)
	got := All()
	if len(got) != 3 || got[0].StudioURL != "" || got[1].StudioURL != "http://a.test" || got[2].StudioURL != "http://b.test" {
		t.Fatalf("All = %+v, want the in-process link, then a, then b", got)
	}
	rmB()
	rmB() // idempotent
	if got := All(); len(got) != 2 || got[1].StudioURL != "http://a.test" {
		t.Errorf("after removing b (twice) = %+v", got)
	}
	rmA()
	rmLocal()
	if got := All(); len(got) != 0 {
		t.Errorf("after removing all = %+v", got)
	}
}

// TestLinkregAllCopies: the list is a copy — changing it changes no
// registered link.
func TestLinkregAllCopies(t *testing.T) {
	reset(t)
	l := &Link{StudioURL: "http://a.test", Instance: "i1"}
	defer Add(l)()
	got := All()
	got[0].StudioURL = "http://changed.test"
	got[0].Instance = "x"
	if l.StudioURL != "http://a.test" || All()[0].Instance != "i1" {
		t.Errorf("All handed out the registered link: %+v", *l)
	}
}

// TestLinkregConcurrent runs Add, Remove and All together (-race).
func TestLinkregConcurrent(t *testing.T) {
	reset(t)
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rm := Add(&Link{StudioURL: fmt.Sprintf("http://%d.test", i)})
			_ = All()
			rm()
			rm()
		}()
	}
	wg.Wait()
	if got := All(); len(got) != 0 {
		t.Errorf("after every add and remove = %+v", got)
	}
}
