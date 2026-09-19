// Package jsonconflict holds a fixture type for schema tests that need
// an embedded field to collide with a struct's own JSON name. The type
// is defined in its own package because go vet's structtag check
// reports that intentional duplication when the embedded type is
// visible in the package being vetted.
package jsonconflict

// Base declares the JSON names "name" (a required string) and "keep".
type Base struct {
	Name string `json:"name"`
	Keep string `json:"keep"`
}
