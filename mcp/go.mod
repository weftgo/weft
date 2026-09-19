module github.com/weftgo/weft/mcp

go 1.26

// Dev-time resolution while the root module is unpublished (TODO §1.1):
// once weftgo/weft is pushed and tagged, drop this replace and require
// a tagged root.
replace github.com/weftgo/weft => ../

require (
	github.com/google/jsonschema-go v0.4.3
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/weftgo/weft v0.0.0-00010101000000-000000000000
)

require (
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)
