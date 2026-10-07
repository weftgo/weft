// Package ingest is Studio's OTLP/HTTP receiver (S4.4): POST /v1/traces
// and /v1/logs, protobuf and JSON, optional gzip, a 16 MiB limit on
// the decompressed body, and the publish-then-write pipeline —
//
//	decode → obsdb.FromOTLP… → publish to the live hub → DB.Write
//
// — so the live tail never waits on the database. A write failure
// answers 503 so the exporter retries; the retry's duplicate live
// frames are dropped by the client on (run, kind, pos).
//
// The mapping itself lives in obsdb (FromOTLPTraces/FromOTLPLogs),
// so the in-process local sink and this receiver produce identical
// rows. Auth is the caller's: the handlers take an authorized
// predicate (Studio: the ingest token, or loopback with none
// configured).
package ingest

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/weftgo/weft/obsdb"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// MaxBody is the request-body limit after decompression (S4.4): 16
// MiB. Above it the receiver answers 413.
const MaxBody = 16 << 20

// Traces returns the POST /v1/traces handler: decode, publish, write.
func Traces(db obsdb.DB, hub obsdb.Hub, authorized func(*http.Request) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			writeIngestError(w, r, http.StatusUnauthorized, "unauthorized",
				"ingest requires Authorization: Bearer <ingest token>")
			return
		}
		req := new(coltracepb.ExportTraceServiceRequest)
		if !decode(w, r, req) {
			return
		}
		batch := obsdb.Batch{Spans: obsdb.FromOTLPTraces(req)}
		writeBatch(w, r, db, hub, batch, new(coltracepb.ExportTraceServiceResponse))
	}
}

// Logs returns the POST /v1/logs handler: decode, publish, write.
func Logs(db obsdb.DB, hub obsdb.Hub, authorized func(*http.Request) bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			writeIngestError(w, r, http.StatusUnauthorized, "unauthorized",
				"ingest requires Authorization: Bearer <ingest token>")
			return
		}
		req := new(collogspb.ExportLogsServiceRequest)
		if !decode(w, r, req) {
			return
		}
		batch := obsdb.Batch{Records: obsdb.FromOTLPLogs(req)}
		writeBatch(w, r, db, hub, batch, new(collogspb.ExportLogsServiceResponse))
	}
}

// decode reads the request body and unmarshals it into msg as
// protobuf or JSON per the Content-Type. The limit is 16 MiB after
// decompression (S4.4): a gzip stream is capped past the gunzip, the
// raw body at the wire. It answers the client itself on failure and
// reports whether decoding succeeded.
func decode(w http.ResponseWriter, r *http.Request, msg proto.Message) bool {
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (ct != "application/x-protobuf" && ct != "application/json") {
		writeIngestError(w, r, http.StatusUnsupportedMediaType, "unsupported",
			"Content-Type must be application/x-protobuf or application/json")
		return false
	}
	// The wire cap; a gzip stream is capped again past the gunzip.
	raw := io.Reader(http.MaxBytesReader(w, r.Body, MaxBody))
	body := raw
	switch enc := r.Header.Get("Content-Encoding"); enc {
	case "", "identity":
	case "gzip":
		zr, err := gzip.NewReader(raw)
		if err != nil {
			writeIngestError(w, r, http.StatusBadRequest, "bad_request",
				"Content-Encoding gzip: "+err.Error())
			return false
		}
		defer func() { _ = zr.Close() }()
		// One byte past the limit: reading that much is itself the 413.
		body = io.LimitReader(zr, MaxBody+1)
	default:
		writeIngestError(w, r, http.StatusUnsupportedMediaType, "unsupported",
			"Content-Encoding "+enc+" is not supported (identity or gzip)")
		return false
	}
	b, err := io.ReadAll(body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeIngestError(w, r, http.StatusRequestEntityTooLarge, "bad_request",
				fmt.Sprintf("request body exceeds %d MiB after decompression", MaxBody>>20))
			return false
		}
		writeIngestError(w, r, http.StatusBadRequest, "bad_request", "read body: "+err.Error())
		return false
	}
	if len(b) > MaxBody {
		writeIngestError(w, r, http.StatusRequestEntityTooLarge, "bad_request",
			fmt.Sprintf("request body exceeds %d MiB after decompression", MaxBody>>20))
		return false
	}
	if ct == "application/x-protobuf" {
		err = proto.Unmarshal(b, msg)
	} else {
		err = protojson.Unmarshal(b, msg)
	}
	if err != nil {
		writeIngestError(w, r, http.StatusBadRequest, "bad_request",
			"decode "+ct+": "+err.Error())
		return false
	}
	return true
}

// writeBatch runs the S4.4 pipeline for one decoded request: every
// record is published to the hub first (deltas included — the live
// lane carries what the database will not keep), then the batch is
// written. A write failure answers 503 unavailable so the exporter
// retries. The 200 response is an empty Export…ServiceResponse in the
// request's own format. partial_success would name rejected items;
// nothing rejects today — every decodable item maps — so the empty
// response is the only reachable one (S4.4).
func writeBatch(
	w http.ResponseWriter, r *http.Request,
	db obsdb.DB, hub obsdb.Hub, batch obsdb.Batch, empty proto.Message,
) {
	// Publish before the insert: a slow or batched backend must not
	// delay the live tail (S4.4's ordering). Heartbeats and duplicate
	// transports are the client's to dedup on (run, kind, pos).
	if hub != nil {
		ctx := context.Background()
		for _, rec := range batch.Records {
			hub.Publish(ctx, obsdb.RecordFrame(rec))
		}
	}
	if err := db.Write(r.Context(), batch); err != nil {
		writeIngestError(w, r, http.StatusServiceUnavailable, "unavailable",
			"write: "+err.Error())
		return
	}
	// Run frames for the runs the batch touched, when the database
	// does not publish its own (sqlite's Write already does, through
	// the shared hub): the runs list can follow the stream without
	// refetching.
	if hub != nil {
		if _, selfPublishing := db.(interface{ Hub() obsdb.Hub }); !selfPublishing {
			publishRuns(r.Context(), db, hub, batch)
		}
	}
	respond(w, r, empty)
}

// publishRuns re-reads each run the batch touched and publishes its
// row as a run frame. Best effort: a run that cannot be read yet is
// simply not announced (its records already were).
func publishRuns(ctx context.Context, db obsdb.DB, hub obsdb.Hub, batch obsdb.Batch) {
	seen := map[string]struct{}{}
	for _, s := range batch.Spans {
		if id := obsdb.DeriveSpan(s).RunID; id != "" {
			seen[id] = struct{}{}
		}
	}
	for _, rec := range batch.Records {
		if id := obsdb.DeriveRecord(rec).RunID; id != "" {
			seen[id] = struct{}{}
		}
	}
	for id := range seen {
		if det, err := db.Run(ctx, id); err == nil {
			hub.Publish(ctx, obsdb.RunFrame(det.RunRow))
		}
	}
}

// respond writes the 200 with the (empty) service response in the
// request's format: protobuf bytes, or protojson for JSON.
func respond(w http.ResponseWriter, r *http.Request, msg proto.Message) {
	ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		ct = "application/x-protobuf"
	}
	if ct == "application/json" {
		b, err := protojson.Marshal(msg)
		if err != nil {
			writeIngestError(w, r, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte{})
}

// writeIngestError answers with the API's error shape (S4.2): the
// exporter retries on the status code; the body is for the human.
func writeIngestError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(`{"error":{"code":` + quoteJSON(code) + `,"message":` + quoteJSON(msg) + `}}`))
}

// quoteJSON renders s as a JSON string. Codes and messages are plain
// text, but a message may echo request bytes (a header value, a
// decoder's complaint), so the encoder does the quoting: it escapes
// every control character and replaces invalid UTF-8 — the body is
// JSON whatever the client sent.
func quoteJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""` // a string always marshals
	}
	return string(b)
}
