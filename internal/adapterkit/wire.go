package adapterkit

import (
	"net/http"
	"sort"
)

// WireFacts is what the first-party adapters answer to the core's
// optional WireFacts convention (the request record's wire, UX-S2-09):
// the endpoint they send to, ExtraBody's top-level keys and
// ExtraHeaders' names — names only, never a value. server is "" when
// the adapter cannot know it (an injected client). The core sanitises
// and bounds what it is handed.
func WireFacts(server string, body map[string]any, headers http.Header) (string, []string, []string) {
	var keys, names []string
	for k := range body {
		keys = append(keys, k)
	}
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(keys)
	sort.Strings(names)
	return server, keys, names
}

// Endpoint is the server an adapter sends to: its BaseURL, else the
// SDK's environment variable env, else the provider's default host
// ("" when it has none to name).
func Endpoint(baseURL, envValue, defaultHost string) string {
	switch {
	case baseURL != "":
		return baseURL
	case envValue != "":
		return envValue
	}
	return defaultHost
}
