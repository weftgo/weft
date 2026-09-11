module github.com/weftgo/weft/openai

go 1.26

require (
	github.com/openai/openai-go v1.12.0
	github.com/weftgo/weft v0.0.0
)

require (
	github.com/tidwall/gjson v1.14.4 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
)

// Dev-time resolution while the root module is unpublished (TODO §1.1):
// once weftgo/weft is pushed and tagged, drop this replace and require
// a tagged root.
replace github.com/weftgo/weft => ../
