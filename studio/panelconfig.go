package studio

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/weftgo/weft/version"
)

// GET <base>/panel-config.json (plan B3): what the devtools panel needs
// to talk to this Studio — its endpoint, the version and the
// capabilities — so the panel never infers its endpoint from its own
// script src (C2).
//
// It is unauthenticated and carries nothing secret (no token, no
// paths), and it answers only a loopback Host (or an AllowOrigins
// host: setup A's DNS-rebinding guard) and a same-origin, loopback or
// AllowOrigins Origin when the request carries one. Anything else is a
// 404 — not a 403 — so a page served from elsewhere sees what it sees
// without a Studio, and the panel removes itself silently.

// panelConfig is the document.
type panelConfig struct {
	// Endpoint is the Studio's base URL as this request reached it:
	// scheme, Host and the handler's Base.
	Endpoint string `json:"endpoint"`
	// Version is the weft version serving it (version.Runtime).
	Version string `json:"version"`
	// Capabilities is api/meta's list.
	Capabilities []string `json:"capabilities"`
}

// panelConfigGroup is the route group: always on, capability
// "panel-config".
func panelConfigGroup() routeGroup {
	return routeGroup{
		name:       "panel-config",
		capability: "panel-config",
		register: func(mux *http.ServeMux, s *Server) {
			mux.HandleFunc("GET /panel-config.json", s.servePanelConfig)
		},
	}
}

func (s *Server) servePanelConfig(w http.ResponseWriter, r *http.Request) {
	if !panelConfigAllowed(s.origins, r) {
		writeError(w, r, http.StatusNotFound, "not_found", "no such route "+r.URL.Path)
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	caps := s.capabilityList()
	if caps == nil {
		caps = []string{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r, http.StatusOK, panelConfig{
		Endpoint:     scheme + "://" + r.Host + s.base,
		Version:      version.Runtime(),
		Capabilities: caps,
	})
}

// panelConfigAllowed is the route's guard: a loopback (or
// AllowOrigins) Host, and an Origin, when present, that is the same
// origin, a loopback one or an AllowOrigins entry.
func panelConfigAllowed(origins []string, r *http.Request) bool {
	if !hostAllowed(origins, r) {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if u, err := url.Parse(origin); err == nil && strings.EqualFold(u.Host, r.Host) {
		return true
	}
	return originAllowed(origins, true, origin)
}
