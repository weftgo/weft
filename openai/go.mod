module github.com/weftgo/weft/openai

go 1.26

require (
	github.com/openai/openai-go v1.12.0
	github.com/weftgo/weft v0.0.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/metric v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
)

// Dev-time resolution while the root module is unpublished (TODO §1.1):
// once weftgo/weft is pushed and tagged, drop this replace and require
// a tagged root.
replace github.com/weftgo/weft => ../
