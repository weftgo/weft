package studio

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/weftgo/weft/obsdb"
)

// The experiments routes (WEFT-PLAYGROUND §10.4, S4.2): an experiment
// is a saved group of playground runs — name, variants, inputs — and
// is itself a queryable object (PQ4): the definition lives in the
// small obsdb table, the runs join by weft.experiment.id. POST
// creates or updates (the matrix runner writes it before issuing the
// cells' commands); GET lists the history and reads one with its runs.
//
// These are Studio-only surfaces by nature (§2): the panel reaches
// them through "open in Studio" with the context carried over. Nothing
// here is public-id-shaped — the history lists every definition
// (prompts and input texts included) and the detail every run of the
// experiment, whatever its public id — so a panel token is refused on
// all three routes (S4.6), the reads like the write.

// experimentBody is the POST body and the wire shape of one
// experiment: the §10.4 document, dates read-only.
type experimentBody struct {
	ID       string                    `json:"id"`
	Name     string                    `json:"name"`
	Agent    string                    `json:"agent"`
	Variants []obsdb.ExperimentVariant `json:"variants"`
	Inputs   []obsdb.ExperimentInput   `json:"inputs"`
}

// experimentView is GET /api/experiments/{id}'s document: the
// definition plus the runs grouped under it (§10.4's shape).
type experimentView struct {
	experimentBody
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
	Runs    []runRow  `json:"runs"`
}

// serveExperiments is GET /api/experiments (the history) and POST
// /api/experiments (create or update a definition by id).
func (s *Server) serveExperiments(w http.ResponseWriter, r *http.Request) {
	if idFrom(r).panel != nil {
		forbidden(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		list, err := s.db.Experiments(r.Context())
		if err != nil {
			dbError(w, r, "experiments", "", err)
			return
		}
		out := make([]experimentBody, 0, len(list))
		for _, e := range list {
			out = append(out, experimentBody{ID: e.ID, Name: e.Name, Agent: e.Agent, Variants: e.Variants, Inputs: e.Inputs})
		}
		writeJSON(w, r, http.StatusOK, struct {
			Experiments []experimentBody `json:"experiments"`
		}{out})
	case http.MethodPost:
		var body experimentBody
		if !decodeBody(w, r, "experiment body", &body) {
			return
		}
		if body.ID == "" {
			badRequest(w, r, "an experiment needs an id (the weft.experiment.id its runs carry)")
			return
		}
		if len(body.Variants) == 0 || len(body.Inputs) == 0 {
			badRequest(w, r, "an experiment defines at least one variant and one input")
			return
		}
		if err := s.db.SaveExperiment(r.Context(), obsdb.Experiment{
			ID: body.ID, Name: body.Name, Agent: body.Agent,
			Variants: body.Variants, Inputs: body.Inputs,
		}); err != nil {
			dbError(w, r, "experiment", body.ID, err)
			return
		}
		saved, err := s.db.Experiment(r.Context(), body.ID)
		if err != nil {
			dbError(w, r, "experiment", body.ID, err)
			return
		}
		writeJSON(w, r, http.StatusOK, experimentView{
			experimentBody: experimentBody{ID: saved.ID, Name: saved.Name, Agent: saved.Agent, Variants: saved.Variants, Inputs: saved.Inputs},
			Created:        saved.Created,
			Updated:        saved.Updated,
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
	if err != nil {
		if errors.Is(err, obsdb.ErrNotFound) {
			notFound(w, r, "unknown experiment "+id)
			return
		}
		dbError(w, r, "experiment", id, err)
		return
	}
	// Every run, not the default page's newest 50: a 3×20 matrix is 60.
	query := obsdb.RunQuery{ExperimentID: id, Limit: 500}
	runs := []runRow{}
	for pages := 0; pages < experimentRunPages; pages++ {
		page, err := s.db.Runs(r.Context(), query)
		if err != nil {
			dbError(w, r, "experiment runs", id, err)
			return
		}
		for _, rec := range page.Runs {
			runs = append(runs, row(rec))
		}
		if page.NextBefore == nil || len(page.Runs) == 0 {
			break
		}
		query.Before, query.BeforeID = *page.NextBefore, page.NextBeforeID
	}
	slices.Reverse(runs) // the database lists newest first
	writeJSON(w, r, http.StatusOK, experimentView{
		experimentBody: experimentBody{ID: e.ID, Name: e.Name, Agent: e.Agent, Variants: e.Variants, Inputs: e.Inputs},
		Created:        e.Created,
		Updated:        e.Updated,
		Runs:           runs,
	})
}
