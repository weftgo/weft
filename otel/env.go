package otel

import (
	"net/url"
	"strings"
)

// The environment destinations (S2.3). The core reads no environment
// variable; this module does, here only, and NoEnv() turns all of them
// off. Explicit options and environment destinations combine; two
// destinations pointing at the same URL are de-duplicated (the explicit
// one wins).
//
//	WEFT_STUDIO_URL, WEFT_STUDIO_TOKEN                     → Studio (content on)
//	OTEL_EXPORTER_OTLP_ENDPOINT, OTEL_EXPORTER_OTLP_HEADERS → OTLP
//	OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT      → content for the env OTLP destination
//	OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES            → resource (buildResource)
//	WEFT_DB                                                  → the default Local path
func envDestinations(getenv func(string) string) []dest {
	var out []dest
	if u := getenv("WEFT_STUDIO_URL"); u != "" {
		d := newDest(destStudio, "studio(env)")
		d.url = u
		d.token = getenv("WEFT_STUDIO_TOKEN")
		on := true
		d.content = &on
		out = append(out, d)
	}
	if u := getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); u != "" {
		d := newDest(destOTLP, "otlp(env)")
		d.url = normalizeEnvEndpoint(u)
		if h := getenv("OTEL_EXPORTER_OTLP_HEADERS"); h != "" {
			d.headers = parseHeaderList(h)
		}
		if strings.EqualFold(getenv("OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT"), "true") {
			on := true
			d.content = &on
		}
		out = append(out, d)
	}
	return out
}

// normalizeEnvEndpoint applies the OTLP endpoint convention: a value
// without a scheme is https:// (the OTel spec's default), and a port
// alone is not special here — weft always exports OTLP/HTTP.
func normalizeEnvEndpoint(u string) string {
	if strings.Contains(u, "://") {
		return u
	}
	return "https://" + u
}

// parseHeaderList reads the OTEL_EXPORTER_OTLP_HEADERS form:
// comma-separated key=value pairs.
func parseHeaderList(s string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out
}

// dedupURL is the URL two destinations are compared by: scheme, host
// and path — the "same URL" of S2.3's de-duplication rule.
func dedupURL(raw string) string {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Scheme + "://" + u.Host + u.Path
}

// dedupe removes environment destinations whose URL an explicit
// destination already uses (the explicit one wins).
func dedupe(explicit, env []dest) []dest {
	used := map[string]bool{}
	for _, d := range explicit {
		switch d.kind {
		case destStudio, destOTLP:
			used[dedupURL(d.url)] = true
		}
	}
	var out []dest
	for _, d := range env {
		if used[dedupURL(d.url)] {
			continue
		}
		out = append(out, d)
	}
	return out
}
