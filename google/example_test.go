package google_test

import (
	"context"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/google"
)

// Constructing a model performs no I/O; credentials ($GEMINI_API_KEY /
// $GOOGLE_API_KEY, then application default) are read on the first run.
// Swap providers by swapping this one line.
func ExampleModel() {
	m := google.Model("gemini-2.5-flash")
	echo := weft.Tool("echo", "Echo a message.", func(ctx context.Context, in struct {
		Message string `json:"message" jsonschema:"the text to repeat"`
	}) (string, error) {
		return in.Message, nil
	})
	agt := weft.New(m, echo)
	_ = agt // a run is agt.Generate(ctx, weft.Prompt("Echo: hello"))
}
