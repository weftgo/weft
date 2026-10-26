package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/weftgo/weft/obsdb"
	linkruntime "github.com/weftgo/weft/studio/runtime"
)

// The experiments routes (WEFT-PLAYGROUND §10.4, S4.2): an experiment
// is a saved group of playground runs — name, variants, inputs — and
// is itself a queryable object (PQ4): the definition lives in the
// small obsdb table, the runs join by weft.experiment.id. POST
// creates or updates (the matrix runner writes it before issuing the
// cells' commands); GET lists the history and reads one with its runs.
//
// The join is by id alone: POST /api/playground/runs accepts any
// experiment_id, saved or not, and labels the run with it — so a
// re-run sent with a saved experiment's id joins that experiment (its
// runs[], run_count and cost), and a definition saved after its runs
// finds them all. Nothing is stored per run here: the runs list is the
// runs table read by weft.experiment.id (RunQuery.ExperimentID, the
// same filter /api/runs and /api/search take as experiment_id=).
//
// These are Studio-only surfaces by nature (§2): the panel reaches
// them through "open in Studio" with the context carried over. Nothing
// here is public-id-shaped — the history lists every definition
// (prompts and input texts included) and the detail every run of the
// experiment, whatever its public id — so a panel token is refused on
// all three routes (S4.6), the reads like the write.

// experimentBody is the POST body and the wire shape of one
// experiment: the §10.4 document, dates read-only. Beside the matrix
// (variants, inputs) it keeps the rest of the §5.1 command the cells
// were sent with (obsdb.ExperimentCommand, flat: engine, side_effects,
// thread, from_step, source_run_id, transcript_edits), and each variant
// its overrides object verbatim plus its own engine / side_effects /
// thread when they differ — so a saved experiment reopens as the form
// that ran it (plan F5). Every one of those is optional: an experiment
// saved before Studio kept them reads back without them. A POST
// replaces the whole definition: a field it leaves out is cleared.
type experimentBody struct {
	ID       string                    `json:"id"`
	Name     string                    `json:"name"`
	Agent    string                    `json:"agent"`
	Variants []obsdb.ExperimentVariant `json:"variants"`
	Inputs   []obsdb.ExperimentInput   `json:"inputs"`
	// PassPredicate is the matrix's pass predicate (UX plan D8,
	// UX-C-38), kept verbatim: Studio bounds its length
	// (maxPassPredicateBytes) and reads nothing into it — the language
	// is the playground's. Omitted when none.
	PassPredicate string `json:"pass_predicate,omitempty"`
	obsdb.ExperimentCommand
}

// maxPassPredicateBytes bounds an experiment's pass predicate.
const maxPassPredicateBytes = 4096

func bodyOf(e obsdb.Experiment) experimentBody {
	return experimentBody{ID: e.ID, Name: e.Name, Agent: e.Agent, Variants: e.Variants, Inputs: e.Inputs,
		PassPredicate: e.PassPredicate, ExperimentCommand: e.ExperimentCommand}
}

// experimentSummary is what the history and the detail both carry
// about an experiment's runs, derived at read time from the runs
// labelled with its id (never stored): run_count counts its top-level
// runs (the detail's runs[]), and the cost block is their whole bill —
// each run's own + delegated (pricing.go), unknown with a reason when a
// run is not priced or the runs could not all be read. succeeded counts
// the top-level runs that finished with a result (the list's pass rate,
// succeeded / run_count) and last_run is the newest one's start (UX-C-36);
// both are omitted past the walk's cap, where the runs were not all read.
type experimentSummary struct {
	RunCount  int        `json:"run_count"`
	Succeeded *int       `json:"succeeded,omitempty"`
	LastRun   *time.Time `json:"last_run,omitempty"`
	costFields
}

// experimentRow is one row of GET /api/experiments: the definition,
// its dates and the summary, so the history needs no per-row detail
// read.
type experimentRow struct {
	experimentBody
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	experimentSummary
}

// experimentView is GET /api/experiments/{id}'s document: the
// definition plus the runs grouped under it (§10.4's shape) and the
// summary.
type experimentView struct {
	experimentBody
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	experimentSummary
	Runs []runRow `json:"runs"`
	// Unsaved marks an experiment id its runs carry with no saved
	// definition (UX-C-15): a replay sent with an experiment_id the
	// matrix never saved, or one whose definition is gone. The
	// document is then synthetic — no variants, no inputs, no dates,
	// the agent its runs name — and its runs are listed as usual.
	Unsaved bool `json:"unsaved,omitempty"`
}

// checkExperiment refuses a definition the matrix could not have sent:
// the modes are the §5.1 command's words (playground.go's
// validateCommand reads the same ones), from_step an ordinal, the
// edits a list. The edits themselves are checked when a cell is run,
// against its source run — here they are kept as sent.
func checkExperiment(b experimentBody) string {
	if b.ID == "" {
		return "an experiment needs an id (the weft.experiment.id its runs carry)"
	}
	// The run command's experiment_id rule (playground.go): an id saved
	// here must be one a cell can be sent with.
	if !linkruntime.ValidCommandID(b.ID) {
		return "id must be 1 to 128 characters of [A-Za-z0-9._:-] (the experiment_id its runs are sent with)"
	}
	if len(b.Variants) == 0 || len(b.Inputs) == 0 {
		return "an experiment defines at least one variant and one input"
	}
	modes := func(at, engine, sideEffects, thread string) string {
		switch engine {
		case "", "live", "scripted":
		default:
			return at + "engine must be live or scripted"
		}
		switch sideEffects {
		case "", "substitute", "park", "allow":
		default:
			return at + "side_effects must be substitute, park or allow"
		}
		switch thread {
		case "", "ephemeral", "fork":
		default:
			return at + "thread must be ephemeral or fork"
		}
		return ""
	}
	if msg := modes("", b.Engine, b.SideEffects, b.Thread); msg != "" {
		return msg
	}
	for i, v := range b.Variants {
		if msg := modes("variant "+strconv.Quote(v.Key)+": ", v.Engine, v.SideEffects, v.Thread); msg != "" {
			return msg
		}
		if !jsonIs(v.Overrides, '{') {
			return fmt.Sprintf("variants[%d].overrides must be an object", i)
		}
	}
	if b.FromStep != nil && *b.FromStep < 0 {
		return "from_step must be a step ordinal (0 or more)"
	}
	for i, in := range b.Inputs {
		if in.FromStep != nil && *in.FromStep < 0 {
			return fmt.Sprintf("inputs[%d].from_step must be a step ordinal (0 or more)", i)
		}
	}
	if !jsonIs(b.TranscriptEdits, '[') {
		return "transcript_edits must be a list"
	}
	if len(b.PassPredicate) > maxPassPredicateBytes {
		return fmt.Sprintf("pass_predicate is %d bytes, at most %d", len(b.PassPredicate), maxPassPredicateBytes)
	}
	return ""
}

// jsonIs says a raw JSON value is absent, null, or opens with open (an
// object's '{', a list's '['): the decoder already proved it is JSON.
func jsonIs(raw json.RawMessage, open byte) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null")) || t[0] == open
}

// The history's bound: GET /api/experiments lists the limit most
// recently updated experiments (?limit=, 0 or absent the default,
// clamped to the max; no cursor yet) with total, how many are saved.
const (
	experimentListDefault = 50
	experimentListMax     = 200
)

// serveExperiments is GET /api/experiments (the history) and POST
// /api/experiments (create or update a definition by id).
func (s *Server) serveExperiments(w http.ResponseWriter, r *http.Request) {
	if idFrom(r).panel != nil {
		forbidden(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		limit, ok := limitParam(w, r, r.URL.Query())
		if !ok {
			return
		}
		switch {
		case limit == 0:
			limit = experimentListDefault
		case limit > experimentListMax:
			limit = experimentListMax
		}
		list, err := s.db.Experiments(r.Context())
		if err != nil {
			dbError(w, r, "experiments", "", err)
			return
		}
		// Newest update first (obsdb.DB.Experiments): the cut keeps the
		// most recently updated, so the per-row summaries — a runs read
		// each — stay bounded however many experiments were saved.
		total := len(list)
		list = list[:min(limit, len(list))]
		out := make([]experimentRow, 0, len(list))
		c := s.coster(r.Context())
		for _, e := range list {
			sum, err := s.experimentSummary(r.Context(), c, e.ID)
			if err != nil {
				dbError(w, r, "experiment runs", e.ID, err)
				return
			}
			out = append(out, experimentRow{experimentBody: bodyOf(e), Created: e.Created, Updated: e.Updated, experimentSummary: sum})
		}
		writeJSON(w, r, http.StatusOK, struct {
			Experiments []experimentRow `json:"experiments"`
			Total       int             `json:"total"`
		}{out, total})
	case http.MethodPost:
		var body experimentBody
		if !decodeBody(w, r, "experiment body", &body) {
			return
		}
		if msg := checkExperiment(body); msg != "" {
			badRequest(w, r, msg)
			return
		}
		if t := bytes.TrimSpace(body.TranscriptEdits); bytes.Equal(t, []byte("null")) || len(t) == 0 {
			body.TranscriptEdits = nil
		}
		if err := s.db.SaveExperiment(r.Context(), obsdb.Experiment{
			ID: body.ID, Name: body.Name, Agent: body.Agent,
			Variants: body.Variants, Inputs: body.Inputs,
			PassPredicate: body.PassPredicate, ExperimentCommand: body.ExperimentCommand,
		}); err != nil {
			dbError(w, r, "experiment", body.ID, err)
			return
		}
		saved, err := s.db.Experiment(r.Context(), body.ID)
		if err != nil {
			dbError(w, r, "experiment", body.ID, err)
			return
		}
		// The answer is the detail's document but runs[]: a re-save of
		// an experiment that already ran says what it holds.
		sum, err := s.experimentSummary(r.Context(), s.coster(r.Context()), saved.ID)
		if err != nil {
			dbError(w, r, "experiment runs", saved.ID, err)
			return
		}
		writeJSON(w, r, http.StatusOK, experimentView{
			experimentBody:    bodyOf(saved),
			Created:           saved.Created,
			Updated:           saved.Updated,
			experimentSummary: sum,
		})
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "the experiments routes are GET and POST")
	}
}

// experimentRunPages caps the detail's walk of an experiment's runs:
// 20 pages of 500.
const experimentRunPages = 20

// serveExperiment is GET /api/experiments/{id}: the definition with
// the runs grouped under it, newest last (matrix order reads better).
// The runs are the experiment's own — the top-level ones: a variant's
// subagent children inherit weft.experiment.id with the rest of the
// metadata, and they are reached through their parent like any child.
func (s *Server) serveExperiment(w http.ResponseWriter, r *http.Request) {
	if idFrom(r).panel != nil {
		forbidden(w, r)
		return
	}
	id := r.PathValue("id")
	e, err := s.db.Experiment(r.Context(), id)
	unsaved := false
	if errors.Is(err, obsdb.ErrNotFound) {
		// Runs labelled with the id but no definition: a synthetic
		// one (runs only), never a 404 for an id /runs?experiment_id=
		// lists runs under (UX-C-15).
		page, rerr := s.db.Runs(r.Context(), obsdb.RunQuery{ExperimentID: id, Limit: 1})
		if rerr != nil {
			dbError(w, r, "experiment runs", id, rerr)
			return
		}
		if len(page.Runs) == 0 {
			notFound(w, r, "unknown experiment "+id)
			return
		}
		e = obsdb.Experiment{ID: id, Agent: page.Runs[0].Agent,
			Variants: []obsdb.ExperimentVariant{}, Inputs: []obsdb.ExperimentInput{}}
		unsaved, err = true, nil
	}
	if err != nil {
		dbError(w, r, "experiment", id, err)
		return
	}
	// Every run, not the default page's newest 50: a 3×20 matrix is 60.
	query := obsdb.RunQuery{ExperimentID: id, Limit: 500}
	runs := []runRow{}
	c := s.coster(r.Context())
	for pages := 0; pages < experimentRunPages; pages++ {
		page, err := s.db.Runs(r.Context(), query)
		if err != nil {
			dbError(w, r, "experiment runs", id, err)
			return
		}
		c.primeTrees(page.Runs)
		for _, rec := range page.Runs {
			runs = append(runs, c.row(rec))
		}
		if page.NextBefore == nil || len(page.Runs) == 0 {
			break
		}
		query.Before, query.BeforeID = *page.NextBefore, page.NextBeforeID
	}
	slices.Reverse(runs) // the database lists newest first
	sum, err := s.experimentSummary(r.Context(), c, id)
	if err != nil {
		dbError(w, r, "experiment runs", id, err)
		return
	}
	writeJSON(w, r, http.StatusOK, experimentView{
		experimentBody:    bodyOf(e),
		Created:           e.Created,
		Updated:           e.Updated,
		experimentSummary: sum,
		Runs:              runs,
		Unsaved:           unsaved,
	})
}

// experimentSummary walks every run labelled with the experiment's id
// — its subagent children included, which inherit the label — once:
// the top-level ones are counted, and the set is priced as a session's
// is (setCost), so the tree costs no children reads. Past the walk's
// cap the count comes from the database's own total and the cost is
// unknown, saying why. A failed read is the caller's error.
func (s *Server) experimentSummary(ctx context.Context, c *coster, id string) (experimentSummary, error) {
	q := obsdb.RunQuery{ExperimentID: id, ParentRunID: "*", Limit: 500}
	var runs []obsdb.RunRow
	for pages := 0; ; pages++ {
		if pages == experimentRunPages {
			page, err := s.db.Runs(ctx, obsdb.RunQuery{ExperimentID: id, Limit: 1})
			if err != nil {
				return experimentSummary{}, err
			}
			why := fmt.Sprintf("the experiment holds more than %d runs: too many to price at read time", experimentRunPages*500)
			return experimentSummary{RunCount: page.Total, costFields: cost{badge: costUnknown, reason: why}.fields()}, nil
		}
		page, err := s.db.Runs(ctx, q)
		if err != nil {
			return experimentSummary{}, err
		}
		runs = append(runs, page.Runs...)
		if page.NextBefore == nil || len(page.Runs) == 0 {
			break
		}
		q.Before, q.BeforeID = *page.NextBefore, page.NextBeforeID
	}
	n, ok := 0, 0
	var last *time.Time
	for _, r := range runs {
		if r.ParentRunID != "" || r.CallerRunID != "" {
			continue // a Subagent child, or a run a tool call started (O11)
		}
		n++
		if r.Status == obsdb.StatusSucceeded {
			ok++
		}
		if last == nil || r.Started.After(*last) {
			t := r.Started.UTC()
			last = &t
		}
	}
	return experimentSummary{RunCount: n, Succeeded: &ok, LastRun: last, costFields: c.setCost(runs, "run").fields()}, nil
}
