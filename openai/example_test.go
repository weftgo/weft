package openai_test

import (
	"context"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/openai"
)

// Constructing a model performs no I/O; a key ($OPENAI_API_KEY) is read
// only when a run starts. Swap providers by swapping this one line.
func ExampleModel() {
	m := openai.Model("gpt-4o-mini")
	echo := weft.Tool("echo", "Echo a message.", func(ctx context.Context, in struct {
		Message string `json:"message" jsonschema:"the text to repeat"`
	}) (string, error) {
		return in.Message, nil
	})
	agt := weft.New(m, echo)
	_ = agt // a run is agt.Generate(ctx, weft.Prompt("Echo: hello"))
}
