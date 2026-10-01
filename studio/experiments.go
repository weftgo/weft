package studio

import (
	"encoding/json"
	"errors"
	"net/http"
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
// them through "open in Studio" with the context carried over.

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
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			badRequest(w, r, "experiment body: "+err.Error())
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
		// A panel token does not write experiment definitions (the
		// matrix is Studio's surface; the panel hands off).
		if idFrom(r).panel != nil {
			forbidden(w, r)
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

// serveExperiment is GET /api/experiments/{id}: the definition with
// the runs grouped under it, newest last (matrix order reads better).
func (s *Server) serveExperiment(w http.ResponseWriter, r *http.Request) {
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
	page, err := s.db.Runs(r.Context(), obsdb.RunQuery{ExperimentID: id, ParentRunID: "*"})
	if err != nil {
		dbError(w, r, "experiment runs", id, err)
		return
	}
	runs := make([]runRow, 0, len(page.Runs))
	for _, rec := range page.Runs {
		runs = append(runs, row(rec))
	}
	writeJSON(w, r, http.StatusOK, experimentView{
		experimentBody: experimentBody{ID: e.ID, Name: e.Name, Agent: e.Agent, Variants: e.Variants, Inputs: e.Inputs},
		Created:        e.Created,
		Updated:        e.Updated,
		Runs:           runs,
	})
}
