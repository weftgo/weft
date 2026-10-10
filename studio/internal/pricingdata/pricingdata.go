// Package pricingdata is the hand-maintained source of Studio's default
// pricing table (plan A5): studio/pricing.json is generated from it by
// studio/internal/pricinggen (go generate ./studio/...), committed, and
// held equal to JSON's output by studio's TestPricingJSONIsGenerated.
//
// Edit the table here, never the JSON: bump Dated to the day the rates
// were read, run go generate, commit both.
//
// The rates are the vendors' public per-1M-token list prices in USD for
// the first-party APIs (not Bedrock, Vertex or Azure resale), read on
// Dated. Rules the table keeps:
//
//   - a model whose price depends on the prompt's length (a long-context
//     tier) is left out: one rate per token kind cannot price it exactly;
//   - a model whose rate the author was not sure of is left out — an
//     unpriced run reads "unpriced", never a guessed number;
//   - a rate column the vendor does not publish is omitted, and Studio
//     then prices that token kind at the base rate (input or output);
//   - cache_write is the 5-minute-TTL write rate (Anthropic: 1.25× the
//     input rate; a 1-hour write bills 2×, which the usage cannot tell
//     apart);
//   - reasoning tokens bill as output at every vendor listed, so no
//     reasoning column is set;
//   - globs are path.Match patterns over "provider/model", first match
//     wins, so the most specific entry comes first; exact model ids are
//     preferred over wildcards so a future model never inherits an old
//     price.
package pricingdata

import (
	"bytes"
	"encoding/json"
)

// Dated is the day the rates below were read (ISO 8601).
const Dated = "2026-10-10"

// Note is the JSON file's own comment (JSON has none of its own).
const Note = "Prices are the vendors' public per-1M-token list rates in USD (first-party APIs), read on the dated day. " +
	"model_glob is a path.Match pattern over provider/model; the first match wins. A missing cached_input, " +
	"cache_write or reasoning rate prices those tokens at the input or output rate. " +
	"This file is also the template for a WEFT_PRICING / --pricing override, which replaces this table whole. " +
	"Generated from studio/internal/pricingdata: edit there, then go generate ./studio/..."

// Model is one row: a provider, a model glob and its rates per 1M
// tokens. Nil rates are omitted from the JSON.
type Model struct {
	Provider    string   `json:"provider"`
	ModelGlob   string   `json:"model_glob"`
	Input       float64  `json:"input"`
	Output      float64  `json:"output"`
	CachedInput *float64 `json:"cached_input,omitempty"`
	CacheWrite  *float64 `json:"cache_write,omitempty"`
	Reasoning   *float64 `json:"reasoning,omitempty"`
}

func r(v float64) *float64 { return &v }

// anthropic is an Anthropic row: cache reads at the published rate,
// cache writes at 1.25× input (the 5-minute TTL).
func anthropic(glob string, in, out, cached float64) Model {
	return Model{Provider: "anthropic", ModelGlob: glob, Input: in, Output: out, CachedInput: r(cached), CacheWrite: r(in * 1.25)}
}

// openai is an OpenAI row: cached input at the published rate, no
// write charge (OpenAI's prompt cache bills writes as plain input).
func openai(glob string, in, out, cached float64) Model {
	return Model{Provider: "openai", ModelGlob: glob, Input: in, Output: out, CachedInput: r(cached)}
}

// google is a Gemini row with input and output only: the context-cache
// rates are left out (cached tokens then price at the input rate).
func google(glob string, in, out float64) Model {
	return Model{Provider: "google", ModelGlob: glob, Input: in, Output: out}
}

// Models is the table, most specific first within a family.
var Models = []Model{
	// Anthropic (claude-*). Claude Haiku 5.5 is left out: its rate
	// doubles past 100K prompt tokens (a tier this table cannot price).
	anthropic("claude-fable-5-1", 10, 50, 0.25),
	anthropic("claude-mythos-5-1", 10, 50, 0.25),
	anthropic("claude-fable-5", 10, 50, 1),
	anthropic("claude-mythos-5", 10, 50, 1),
	anthropic("claude-opus-5-5", 4, 20, 0.20),
	anthropic("claude-opus-5", 5, 25, 0.50),
	anthropic("claude-opus-4-8", 5, 25, 0.50),
	anthropic("claude-opus-4-7", 5, 25, 0.50),
	anthropic("claude-opus-4-6", 5, 25, 0.50),
	anthropic("claude-sonnet-5-5", 2, 10, 0.20),
	anthropic("claude-sonnet-5", 2, 10, 0.20),
	anthropic("claude-sonnet-4-6", 3, 15, 0.30),
	anthropic("claude-haiku-4-5", 1, 5, 0.10),
	anthropic("claude-haiku-4-5-20*", 1, 5, 0.10),

	// OpenAI (gpt-*, o*): the aliases and their dated snapshots at the
	// same rate. gpt-4o's older snapshot (2024-05-13) billed more and is
	// left out.
	openai("gpt-5", 1.25, 10, 0.125),
	openai("gpt-5-2025-08-07", 1.25, 10, 0.125),
	openai("gpt-5-mini", 0.25, 2, 0.025),
	openai("gpt-5-mini-2025-08-07", 0.25, 2, 0.025),
	openai("gpt-5-nano", 0.05, 0.40, 0.005),
	openai("gpt-5-nano-2025-08-07", 0.05, 0.40, 0.005),
	openai("gpt-4.1", 2, 8, 0.50),
	openai("gpt-4.1-2025-04-14", 2, 8, 0.50),
	openai("gpt-4.1-mini", 0.40, 1.60, 0.10),
	openai("gpt-4.1-mini-2025-04-14", 0.40, 1.60, 0.10),
	openai("gpt-4.1-nano", 0.10, 0.40, 0.025),
	openai("gpt-4.1-nano-2025-04-14", 0.10, 0.40, 0.025),
	openai("gpt-4o", 2.50, 10, 1.25),
	openai("gpt-4o-2024-08-06", 2.50, 10, 1.25),
	openai("gpt-4o-2024-11-20", 2.50, 10, 1.25),
	openai("gpt-4o-mini", 0.15, 0.60, 0.075),
	openai("gpt-4o-mini-2024-07-18", 0.15, 0.60, 0.075),
	openai("o3", 2, 8, 0.50),
	openai("o3-2025-04-16", 2, 8, 0.50),
	openai("o3-mini", 1.10, 4.40, 0.55),
	openai("o3-mini-2025-01-31", 1.10, 4.40, 0.55),
	openai("o4-mini", 1.10, 4.40, 0.275),
	openai("o4-mini-2025-04-16", 1.10, 4.40, 0.275),
	openai("o1", 15, 60, 7.50),
	openai("o1-2024-12-17", 15, 60, 7.50),

	// Google (gemini-*). Gemini 2.5 Pro is left out: its rate rises
	// past 200K prompt tokens.
	google("gemini-2.5-flash", 0.30, 2.50),
	google("gemini-2.5-flash-lite", 0.10, 0.40),
	google("gemini-2.0-flash", 0.10, 0.40),
	google("gemini-2.0-flash-lite", 0.075, 0.30),
}

// file is pricing.json's shape.
type file struct {
	Dated  string  `json:"dated"`
	Note   string  `json:"note"`
	Models []Model `json:"models"`
}

// JSON is pricing.json's bytes: the table, two-space indented, one
// trailing newline.
func JSON() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(file{Dated: Dated, Note: Note, Models: Models}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
