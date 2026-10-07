package version_test

import (
	"fmt"

	"github.com/weftgo/weft/version"
)

// Runtime is what a binary prints for its version: the tag a consumer
// built against, or the source's own tag inside the workspace.
func ExampleRuntime() {
	v := version.Runtime()
	fmt.Println(v == version.Version) // in this repository's tests, build info reads "(devel)"
	// Output: true
}
