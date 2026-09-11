module github.com/weftgo/weft/anthropic

go 1.26

require (
	github.com/anthropics/anthropic-sdk-go v1.72.0
	github.com/weftgo/weft v0.0.0-00010101000000-000000000000
)

require (
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/invopop/jsonschema v0.14.0 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/standard-webhooks/standard-webhooks/libraries v0.0.1 // indirect
	github.com/tidwall/gjson v1.18.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.2 // indirect
	golang.org/x/sync v0.16.0 // indirect
)

// Dev-time resolution while the root module is unpublished (TODO §1.1):
// once weftgo/weft is pushed and tagged, drop this replace and require
// a tagged root.
replace github.com/weftgo/weft => ../
