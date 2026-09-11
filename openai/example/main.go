// Command example runs a two-step agent conversation against the real
// OpenAI API (or any OPENAI_BASE_URL server). OPENAI_API_KEY must be
// set; the tool call and its round-trip are printed.
//
// From this directory: go run . "What's the weather in Paris?"
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/weftgo/weft"
	"github.com/weftgo/weft/openai"
)

func main() {
	prompt := "What's the weather in Paris? Use the weather tool."
	if len(os.Args) > 1 {
		prompt = os.Args[1]
	}
	model := openai.Model("gpt-4o-mini")
	agt := weft.New(model,
		weft.Name("openai-example"),
		weft.Instructions("You are a terse assistant. Use the tool when asked."),
		weft.Tool("weather", "Report the weather for a city.", func(ctx context.Context, in struct {
			City string `json:"city" jsonschema:"the city to report on"`
		}) (string, error) {
			return fmt.Sprintf("sunny, 22°C, in %s", in.City), nil
		}),
	)
	res, err := agt.Generate(context.Background(), weft.Prompt(prompt))
	if err != nil {
		fmt.Fprintln(os.Stderr, "run failed:", err)
		os.Exit(1)
	}
	for _, step := range res.Steps {
		for _, c := range step.ToolCalls {
			fmt.Printf("tool %s(%s)\n", c.Name, c.Args)
		}
	}
	fmt.Println(res.Text())
	fmt.Printf("(%d steps, %d tokens)\n", res.NumSteps(), res.Usage.Total())
}
