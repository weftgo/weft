// Package reqread holds what obsdb's two backends share to answer
// DB.Prompt, DB.Tools and DB.Catalogs over the records they read: the
// lookups by hash and the one-per-hash catalog list.
package reqread

import "github.com/weftgo/weft/obsdb"

// FindPrompt returns the lowest-index prompt record with hash; ok is
// false when none has it (and always for the empty hash).
func FindPrompt(prompts []obsdb.PromptRecord, hash string) (obsdb.PromptRecord, bool) {
	for _, p := range prompts {
		if hash != "" && p.Hash == hash {
			return p, true
		}
	}
	return obsdb.PromptRecord{}, false
}

// FindTools returns the lowest-index tools record with hash; ok is
// false when none has it (and always for the empty hash).
func FindTools(tools []obsdb.ToolsRecord, hash string) (obsdb.ToolsRecord, bool) {
	for _, t := range tools {
		if hash != "" && t.Hash == hash {
			return t, true
		}
	}
	return obsdb.ToolsRecord{}, false
}

// UniqueCatalogs keeps the first (lowest-index) tools record of each
// hash, in index order: DB.Catalogs' answer.
func UniqueCatalogs(tools []obsdb.ToolsRecord) []obsdb.ToolsRecord {
	seen := map[string]bool{}
	out := make([]obsdb.ToolsRecord, 0, len(tools))
	for _, t := range tools {
		if seen[t.Hash] {
			continue
		}
		seen[t.Hash] = true
		out = append(out, t)
	}
	return out
}
