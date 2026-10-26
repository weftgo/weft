// Package linkreg is the process's open runtime links as an embedded
// Studio sees them (U20.2, UX-D-01): weft/runtime adds a link when
// Install opens it and removes it when the link stops, and weft/studio
// reads the list for /api/meta's runtime_link — whether this process's
// link talks to this very server in-process, or which Studio it dialled
// instead. An app that embeds Studio (setup A) and whose link dialled
// another Studio (weft dev's) can then say where its runtime went,
// instead of reading as an app whose link is closed.
//
// It exists because weft/runtime imports weft/studio (runtime.Local
// takes a *studio.Server), so studio cannot ask runtime directly.
package linkreg

import (
	"sort"
	"sync"
)

// Link is one open runtime link.
type Link struct {
	// Local is the *studio.Server an in-process link talks to
	// (runtime.Local); nil for a link that dials over HTTP.
	Local any
	// StudioURL is the Studio an HTTP link dialled, without userinfo,
	// query or fragment (the registration's studio_url); "" in-process.
	StudioURL string
}

var reg struct {
	mu    sync.Mutex
	links map[*Link]struct{}
}

// Add records l as open; the returned function removes it (idempotent).
func Add(l *Link) (remove func()) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if reg.links == nil {
		reg.links = map[*Link]struct{}{}
	}
	reg.links[l] = struct{}{}
	return func() {
		reg.mu.Lock()
		defer reg.mu.Unlock()
		delete(reg.links, l)
	}
}

// All lists the open links, HTTP links ordered by their Studio URL.
func All() []Link {
	reg.mu.Lock()
	out := make([]Link, 0, len(reg.links))
	for l := range reg.links {
		out = append(out, *l)
	}
	reg.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].StudioURL < out[j].StudioURL })
	return out
}
