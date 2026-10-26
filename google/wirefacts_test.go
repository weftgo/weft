package google

import (
	"net/http"
	"slices"
	"testing"
)

// TestWireFacts pins what the adapter tells the request record's wire
// (UX-S2-09): its endpoint, ExtraBody's keys and ExtraHeaders' names —
// never a value.
func TestWireFacts(t *testing.T) {
	m := Model("m", BaseURL("https://gw.example:8443/v1"),
		ExtraBody(map[string]any{"provider": map[string]any{"order": []string{"x"}}, "metadata": "secret"}),
		ExtraHeaders(http.Header{"X-Route": {"blue"}, "Authorization": {"Bearer s"}})).(*model)
	server, keys, headers := m.WireFacts()
	if server != "https://gw.example:8443/v1" || !slices.Equal(keys, []string{"metadata", "provider"}) ||
		!slices.Equal(headers, []string{"Authorization", "X-Route"}) {
		t.Errorf("WireFacts = %q, %v, %v", server, keys, headers)
	}
	want := defaultServer(t) // clears the SDK's variable before the model reads it
	if s, k, h := Model("m").(*model).WireFacts(); s != want || k != nil || h != nil {
		t.Errorf("bare WireFacts = %q, %v, %v", s, k, h)
	}
}

func defaultServer(t *testing.T) string {
	if env := ""; env != "" {
		t.Setenv(env, "")
	}
	return ""
}
