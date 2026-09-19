module github.com/weftgo/weft/mcp

go 1.26

// Dev-time resolution while the root module is unpublished (TODO §1.1):
// once weftgo/weft is pushed and tagged, drop this replace and require
// a tagged root.
replace github.com/weftgo/weft => ../

require (
	github.com/google/jsonschema-go v0.4.3
	github.com/weftgo/weft v0.0.0-00010101000000-000000000000
)
