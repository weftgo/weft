package thread

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/weftgo/weft"
)

// Entry is the sealed set of session entry kinds (ADR 0011 §2): a
// session is an append-only tree of these, and the leaf is the entry
// the next one attaches to. External types cannot join, so switches
// over entries stay exhaustively lintable; weft adds kinds additively
// under the "v" rule below. On the wire every entry carries a "type"
// discriminator and UnmarshalEntry restores it — the same rule and the
// same compatibility contract as the core's events and message parts
// (ADR 0001, ADR 0004).
//
// Every entry, of every kind, carries an ID, a ParentID (empty for the
// session's root entry) and a Created time. Whether a kind's content
// enters the model's context is part of the kind's contract: message
// and custom_message do; the bookkeeping kinds never do; compaction
// and branch_summary contribute their summary text.
type Entry interface {
	isEntry()
}

// MessageEntry is one weft.Message in the transcript, embedded with
// the core's message wire (ADR 0001) verbatim. It is how conversation
// content is stored, and it is always in the model's context.
type MessageEntry struct {
	ID       string       `json:"id"`
	ParentID string       `json:"parent,omitempty"`
	Created  time.Time    `json:"created"`
	Message  weft.Message `json:"message"`
}

// TurnEntry is the per-turn ledger: the run's id (<session>-t<n>), its
// stop reason, usage and step count, the error text on failure, the
// calls left pending on the approval boundary, and whether the turn
// was canceled. It records the turn's outcome, never its content — the
// messages are MessageEntries — and it does not enter the model's
// context.
type TurnEntry struct {
	ID         string              `json:"id"`
	ParentID   string              `json:"parent,omitempty"`
	Created    time.Time           `json:"created"`
	RunID      string              `json:"run_id"`
	StopReason weft.StopReason     `json:"stop_reason,omitempty"`
	Usage      weft.Usage          `json:"usage"`
	Steps      int                 `json:"steps,omitempty"`
	Err        string              `json:"err,omitempty"`
	Pending    []weft.ToolCallPart `json:"pending,omitempty"`
	Canceled   bool                `json:"canceled,omitempty"`
	// LastInput is the compaction trigger's baseline for the turn:
	// the provider-reported input of the run's final model step, plus
	// the estimated tokens of the tail that report cannot cover (the
	// final step's own messages) — one number, recovered identically
	// by a live session and a reopen (ADR 0020 §2: reported tokens
	// are the signal; only what the report cannot cover is estimated).
	// Absent on turns that ran no step.
	LastInput int64 `json:"last_input,omitempty"`
}

// Reason is why a compaction ran (ADR 0020 §1) — the value a
// CompactionEntry records on the wire.
type Reason string

// The reasons a compaction entry records.
const (
	// ReasonManual marks a compaction the caller asked for: Compact,
	// or ApplyCompaction over a previewed plan.
	ReasonManual Reason = "manual"
	// ReasonThreshold marks a compaction the configured trigger
	// started: the measured context crossed the window minus the
	// reserve (ADR 0020 §2).
	ReasonThreshold Reason = "threshold"
	// ReasonTrim marks a trim: a trimmer pre-pass brought the context
	// under the line, so no summary was made — the entry's Summary is
	// empty.
	ReasonTrim Reason = "trim"
	// ReasonFromHook marks a compaction whose plan a BeforeCompact
	// hook replaced.
	ReasonFromHook Reason = "from_hook"
	// ReasonOverflow marks the compaction that follows a turn failing
	// with weft.ErrContextOverflow, before the turn's one re-run
	// (ADR 0020 §5).
	ReasonOverflow Reason = "overflow"
)

// CompactionEntry records one compaction (ADR 0020 §1): the summary
// text, the id of the first entry kept raw after it, the estimated
// size of the model's context before it, the reason it ran, the
// summarizer's cost and identity, the details (files read, pinned
// entries kept through), and a hash of the summarized range. Nothing
// is deleted — the summarized entries stay in the file, and the
// context at a leaf is the latest summary compaction's summary on the
// path, the entries it pinned, then the entries from its first kept
// id onward.
//
// A trim that needed no summary is the same entry with an empty
// Summary, Reason "trim" and a Trim record naming exactly what was
// stubbed. A trim never moves the boundary: the latest summary
// compaction below it keeps governing the context, the trim's stubs
// layer over the kept range, and its FirstKept only repeats the
// boundary in force when it landed.
type CompactionEntry struct {
	ID        string    `json:"id"`
	ParentID  string    `json:"parent,omitempty"`
	Created   time.Time `json:"created"`
	Summary   string    `json:"summary,omitempty"`
	FirstKept string    `json:"first_kept"`
	// TokensBefore is the estimated size of the context the model was
	// shown when the compaction ran — the compacted view, not the raw
	// path.
	TokensBefore int64  `json:"tokens_before"`
	Reason       Reason `json:"reason,omitempty"`
	// SummarizerUsage and SummarizerModel name what the summary cost
	// and which model made it (the cost ledger, ADR 0020 §4) — absent
	// on a trim, which summarizes nothing.
	SummarizerUsage weft.Usage     `json:"summarizer_usage,omitzero"`
	SummarizerModel weft.ModelInfo `json:"summarizer_model,omitzero"`
	FilesRead       []string       `json:"files_read,omitempty"`
	// FilesModified is a format-1 wire field kept readable: no build
	// writes it (the sandbox write log it was reserved for was
	// abandoned), and a file that carries it round-trips unchanged.
	FilesModified []string `json:"files_modified,omitempty"`
	Pinned        []string `json:"pinned,omitempty"`
	RangeHash     string   `json:"range_hash,omitempty"`
	// Trim is the trim record: present exactly on a trim, absent on a
	// summary compaction. An entry carrying one is written with "v":5
	// — a reader that does not know the record would replay the trim
	// wrongly, so it must fail loudly instead (ADR 0011 §6).
	Trim *TrimRecord `json:"trim,omitempty"`
}

// isTrim reports whether the entry is a trim record rather than a
// summary compaction: no summary, reason trim. A trim never governs
// the context's boundary and never feeds the iterative summary chain.
func (e CompactionEntry) isTrim() bool {
	return e.Summary == "" && e.Reason == ReasonTrim
}

// TrimRecord is what a trim did, persisted so the context replays it
// from the file alone (ADR 0020, amendment 2026-10-01): every tool
// result the trimmer replaced, with the replacement text. The walk
// applies exactly these stubs — it never consults the session's
// configured Trimmer — so a recorded trim reads the same under any
// options, in any process.
type TrimRecord struct {
	Stubs []TrimStub `json:"stubs"`
}

// TrimStub is one stubbed tool result: the entry that holds it, the
// call it answers, and the content the model sees in its place. The
// stored result is untouched; IsError is what the stubbed part
// reports (false for the built-in trimmer's stub).
type TrimStub struct {
	Entry   string `json:"entry"`
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

// BranchSummaryEntry summarizes the branch a Session.Branch leaves
// behind (ADR 0020 §6): Summary is the text and FromEntry is the entry
// the abandoned branch grew from, so the context shows the summary in
// place of that branch's messages. Branch summaries share no cache
// prefix with the main line; that cost is documented, not hidden.
type BranchSummaryEntry struct {
	ID        string    `json:"id"`
	ParentID  string    `json:"parent,omitempty"`
	Created   time.Time `json:"created"`
	Summary   string    `json:"summary"`
	FromEntry string    `json:"from_entry"`
}

// LeafEntry moves the session's leaf to an existing entry — branch
// navigation without a rewrite (ADR 0011 §3). The next entry attaches
// to Entry, and the model's context is rebuilt from there.
type LeafEntry struct {
	ID       string    `json:"id"`
	ParentID string    `json:"parent,omitempty"`
	Created  time.Time `json:"created"`
	Entry    string    `json:"entry"`
}

// LabelEntry names an entry — bookmarks, checkpoints, a UI's anchors.
// The label never enters the model's context.
type LabelEntry struct {
	ID       string    `json:"id"`
	ParentID string    `json:"parent,omitempty"`
	Created  time.Time `json:"created"`
	Entry    string    `json:"entry"`
	Name     string    `json:"name"`
}

// InfoEntry edits the session's title and metadata as an append, never
// a rewrite: the current title is the last info entry's Title, and the
// current metadata is every info entry's Meta merged in order.
type InfoEntry struct {
	ID       string            `json:"id"`
	ParentID string            `json:"parent,omitempty"`
	Created  time.Time         `json:"created"`
	Title    string            `json:"title,omitempty"`
	Meta     map[string]string `json:"meta,omitempty"`
}

// CustomEntry carries application state: a caller-chosen Kind and
// opaque JSON Data. It survives every compaction (ADR 0020 §4) and
// never enters the model's context. A nil Data writes no "data" key —
// omitempty keeps nil and absent the same value both ways, so a
// round trip through the wire never turns "no data" into "null".
type CustomEntry struct {
	ID       string          `json:"id"`
	ParentID string          `json:"parent,omitempty"`
	Created  time.Time       `json:"created"`
	Kind     string          `json:"kind"`
	Data     json.RawMessage `json:"data,omitempty"`
}

// CustomMessageEntry carries an application message: a caller-chosen
// Kind and a weft.Message that is always in the model's context — how
// an application injects a note the model must see without attributing
// it to the user.
type CustomMessageEntry struct {
	ID       string       `json:"id"`
	ParentID string       `json:"parent,omitempty"`
	Created  time.Time    `json:"created"`
	Kind     string       `json:"kind"`
	Message  weft.Message `json:"message"`
}

// ApprovalRequestEntry is a parked call made durable (ADR 0021 §1): a
// call the core's approval boundary left unexecuted, recorded with the
// arguments and their SHA-256 so a decision can name exactly what it
// decided, the run that parked it, why it parked, and an optional
// expiry — a request past its expiry takes no decision and is denied
// with the stated reason the next time the session looks at the
// boundary (ADR 0021 §5). It is written in the same Append as the
// turn that parked it, so no window exists where the turn is durable
// and the request is not. Its ID names this one occurrence of the
// call: call ids repeat across turns, the entry id never does, and a
// signed decision is bound to it. It never enters the model's context;
// the pending call itself stays unresolved in the transcript until a
// decision resolves it.
type ApprovalRequestEntry struct {
	ID         string          `json:"id"`
	ParentID   string          `json:"parent,omitempty"`
	Created    time.Time       `json:"created"`
	CallID     string          `json:"call_id"`
	Tool       string          `json:"tool"`
	Args       json.RawMessage `json:"args,omitempty"`
	ArgsSHA256 string          `json:"args_sha256"`
	RunID      string          `json:"run_id"`
	Reason     string          `json:"reason,omitempty"`
	Expiry     time.Time       `json:"expiry,omitzero"`
	// Child names the delegated child session this request mirrors,
	// and Wrapper the parent-side delegating call it parks under (ADR
	// 0022 §7): a pool child's parked call is requested in its own
	// session and mirrored here so the parent's Pending surfaces it
	// with its lineage. Both empty on an ordinary request.
	Child   string `json:"child,omitempty"`
	Wrapper string `json:"wrapper,omitempty"`
}

// ApprovalDecisionEntry is one decision over a parked call (ADR 0021
// §1): approve, deny with a reason, or resolve with content computed
// outside the process (resolve_error marks it an error). It records
// Who decided, When (the entry's Created), Via which channel — "user"
// for Decide, "signed" for DecideSigned, "approver" for the live chain
// step, "grant" for a grant's approval, "expiry" for an expired
// request's automatic denial, "interrupt" for the denial an
// interrupting Send records, "child" for a pool delegation's
// resolution — and the run the decided request belonged to. It never
// enters the model's context; the model sees the decision only
// through the result the resumed run produces.
type ApprovalDecisionEntry struct {
	ID       string    `json:"id"`
	ParentID string    `json:"parent,omitempty"`
	Created  time.Time `json:"created"`
	CallID   string    `json:"call_id"`
	Outcome  Outcome   `json:"outcome"`
	Reason   string    `json:"reason,omitempty"`
	Content  string    `json:"content,omitempty"`
	Who      string    `json:"who,omitempty"`
	Via      string    `json:"via,omitempty"`
	RunID    string    `json:"run_id,omitempty"`
	// Nonce and KeyID are the signed-decision replay guard's record
	// (ADR 0021 §3): a decision that arrived signed carries the nonce
	// it answered and the key that vouched for it, so a replayed
	// signature is detectable from the file alone — across restarts.
	// KeyID is also the signed decision's identity under Quorum: one
	// key is one approver, whatever Who says. Empty on the in-process
	// paths, which mint no challenge.
	Nonce string `json:"nonce,omitempty"`
	KeyID string `json:"key_id,omitempty"`
	// RequestID is the id of the request entry the decision answers —
	// the occurrence of the call, which a call id alone does not name.
	// Set on every decision recorded over a parked request — Decide,
	// DecideSigned (whose challenge is bound to it), an expiry or
	// interrupt denial; empty on a chain step's decision (a grant, the
	// Approver), written in the same append as the request or without
	// one, and RunID names the occurrence either way.
	RequestID string `json:"request_id,omitempty"`
	// Always records that the decision was an "approve and always
	// allow" (ApproveAlways): the grant is minted when the call's
	// effective verdict becomes approve — at once without a quorum,
	// with the completing approval under one — and never when the
	// verdict is anything else.
	Always bool `json:"always,omitempty"`
}

// ApprovalAuditEntry is the chain's own trail (ADR 0021 §2): every step
// the decision chain takes over a call — a grant matched, the Approver
// consulted (decided, declined, timed out), the park, an expiry
// denial, a signed decision refused, a resume started —
// leaves one of these, including automatic approvals, so s.Audit()
// can tell the whole story from the file alone. Step and Outcome name
// the step and how it ended; Detail is prose for a human reader and
// never parsed — what the session reads back lives in the typed
// fields. It never enters the model's context.
type ApprovalAuditEntry struct {
	ID       string    `json:"id"`
	ParentID string    `json:"parent,omitempty"`
	Created  time.Time `json:"created"`
	CallID   string    `json:"call_id,omitempty"`
	Step     string    `json:"step"`
	Outcome  string    `json:"outcome,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	RunID    string    `json:"run_id,omitempty"`
	// GrantID names the grant a StepGrant entry matched — a session
	// grant's entry id, or with GrantShared the GrantStore's own id
	// for it. A session grant's MaxUses is counted from these fields:
	// one entry is one use.
	GrantID     string `json:"grant_id,omitempty"`
	GrantShared bool   `json:"grant_shared,omitempty"`
	// KeyID names the keyring key a refused signed decision claimed
	// (StepSigned), when the ring holds it.
	KeyID string `json:"key_id,omitempty"`
	// Decisions lists the decision entries a resume applied
	// (StepResume, outcome "started"). A decision is spent by the
	// resume that applied it: it resolves its call on that resume's
	// own line of the tree and nowhere else, so a Branch back to the
	// decided boundary asks for a new decision instead of running the
	// call again on the old one.
	Decisions []string `json:"decisions,omitempty"`
}

// GrantEntry is a session-scoped grant made durable (ADR 0021 §4): a
// standing approval — or, with Deny, a standing refusal — for future
// calls of one tool whose arguments match every predicate. It is the
// decision chain's first step: a matching live grant decides at once,
// audited, including the automatic approval. Liveness is derived, never
// stored: a GrantRevokedEntry naming the grant ends it, an Expiry
// passes, or its MaxUses is reached — uses counted from the audit
// entries the chain writes when it matches (their GrantID). It never enters the
// model's context; the model sees a grant only through the result of
// the call it allowed or refused.
type GrantEntry struct {
	ID       string    `json:"id"`
	ParentID string    `json:"parent,omitempty"`
	Created  time.Time `json:"created"`
	Grant
}

// GrantRevokedEntry ends a grant (ADR 0021 §4): revocation is an
// append, never a rewrite — the grant entry stays, the walk reads the
// revocation after it, and the audit trail keeps both.
type GrantRevokedEntry struct {
	ID       string    `json:"id"`
	ParentID string    `json:"parent,omitempty"`
	Created  time.Time `json:"created"`
	GrantID  string    `json:"grant_id"`
}

// ReceiptEntry is the steering receipt (ADR 0019): the
// journey of one message accepted while the session was busy under
// the Steer policy. One entry records acceptance — Status "queued",
// the message on Msg — and a second, linked by Receipt, records the
// fate: "delivered" (the running run's steering drain took it; RunID
// names the run, and the message landed in that run's transcript),
// "deferred" (it runs as the next turn instead — a StopWhen end, an
// open approval boundary, or the run ended before the drain; Turn
// names the follow-up turn's receipt), or "dropped" (ClearQueue).
// Receipt entries never enter the model's context: the message
// reaches the model through the run that delivered it or the
// follow-up turn that ran it, exactly once.
type ReceiptEntry struct {
	ID       string        `json:"id"`
	ParentID string        `json:"parent,omitempty"`
	Created  time.Time     `json:"created"`
	Receipt  string        `json:"receipt,omitempty"`
	Status   string        `json:"status"`
	Msg      *weft.Message `json:"msg,omitempty"`
	RunID    string        `json:"run_id,omitempty"`
	Turn     string        `json:"turn,omitempty"`
}

// Receipt statuses — the wire values, pinned by the format-3 goldens.
const (
	// ReceiptQueued marks acceptance: the message is held for the
	// running turn's steering drain.
	ReceiptQueued = "queued"
	// ReceiptDelivered marks a message the running run drained: it is
	// an ordinary transcript message of that run, named by RunID.
	ReceiptDelivered = "delivered"
	// ReceiptDeferred marks a message that became a follow-up turn
	// (Turn names its receipt): a StopWhen end, an open approval
	// boundary, or the run ended before the drain.
	ReceiptDeferred = "deferred"
	// ReceiptDropped marks a message removed by ClearQueue before
	// delivery: it never reaches the model.
	ReceiptDropped = "dropped"
)

// PoolReceiptEntry is the pool receipt (ADR 0022 §4): the journey of
// one child run a thread/pool started for this session. One entry
// records acceptance — Status "accepted", the child session on Child,
// the task on Prompt — a second records the slot acquisition and
// start ("running"), and a third, linked by Receipt, records the
// settlement: "done" (Stop carries the child's answer, Usage its
// total), "failed" (Stop the cause), "canceled" (an explicit Cancel),
// or "capped" (the child died on a budget — MaxSteps or a usage
// limit). Call names the delegating tool call for wrapped
// delegations. Pool receipt entries never enter the model's context:
// the answer reaches the model as the delegating call's result (a
// sync delegation) or however the application delivers it (an async
// one); the entry is the ledger, not the channel.
type PoolReceiptEntry struct {
	ID       string     `json:"id"`
	ParentID string     `json:"parent,omitempty"`
	Created  time.Time  `json:"created"`
	Receipt  string     `json:"receipt,omitempty"`
	Status   string     `json:"status"`
	Child    string     `json:"child,omitempty"`
	Call     string     `json:"call,omitempty"`
	Prompt   string     `json:"prompt,omitempty"`
	Stop     string     `json:"stop,omitempty"`
	Usage    weft.Usage `json:"usage,omitzero"`
}

// Pool receipt statuses — the wire values, pinned by the format-4
// goldens. The machine is accepted → running → exactly one of done,
// failed, canceled, capped.
const (
	// PoolAccepted marks the delegation recorded and queued for a slot.
	PoolAccepted = "accepted"
	// PoolRunning marks the slot acquired and the child session's turn
	// started — the wait between acceptance and running is the pool's
	// queue, visible.
	PoolRunning = "running"
	// PoolDone marks a child that ran to its intended end; Stop is its
	// answer, Usage its total cost.
	PoolDone = "done"
	// PoolFailed marks a child whose run failed; Stop is the cause.
	PoolFailed = "failed"
	// PoolCanceled marks a child canceled by an explicit Cancel (or the
	// pool's Close) — never by the submitting turn's own end, which an
	// async child survives by design (ADR 0022 D4).
	PoolCanceled = "canceled"
	// PoolCapped marks a child that died on a budget — ErrMaxSteps or
	// ErrUsageLimit (DeerFlow's token-capped/turn-capped collapsed; the
	// stop reason distinguishes them).
	PoolCapped = "capped"
)

func (MessageEntry) isEntry()          {}
func (TurnEntry) isEntry()             {}
func (CompactionEntry) isEntry()       {}
func (BranchSummaryEntry) isEntry()    {}
func (LeafEntry) isEntry()             {}
func (LabelEntry) isEntry()            {}
func (InfoEntry) isEntry()             {}
func (CustomEntry) isEntry()           {}
func (CustomMessageEntry) isEntry()    {}
func (ApprovalRequestEntry) isEntry()  {}
func (ApprovalDecisionEntry) isEntry() {}
func (ApprovalAuditEntry) isEntry()    {}
func (GrantEntry) isEntry()            {}
func (GrantRevokedEntry) isEntry()     {}
func (ReceiptEntry) isEntry()          {}
func (PoolReceiptEntry) isEntry()      {}

// idOf returns the entry's ID — the tree node's name, the one field
// every kind carries at the same meaning. The sealed set keeps the
// switch exhaustive by construction; the empty string returns only for
// a zero value no code path in this package builds.
func idOf(e Entry) string {
	switch e := e.(type) {
	case MessageEntry:
		return e.ID
	case TurnEntry:
		return e.ID
	case CompactionEntry:
		return e.ID
	case BranchSummaryEntry:
		return e.ID
	case LeafEntry:
		return e.ID
	case LabelEntry:
		return e.ID
	case InfoEntry:
		return e.ID
	case CustomEntry:
		return e.ID
	case CustomMessageEntry:
		return e.ID
	case ApprovalRequestEntry:
		return e.ID
	case ApprovalDecisionEntry:
		return e.ID
	case ApprovalAuditEntry:
		return e.ID
	case GrantEntry:
		return e.ID
	case GrantRevokedEntry:
		return e.ID
	case ReceiptEntry:
		return e.ID
	case PoolReceiptEntry:
		return e.ID
	}
	return ""
}

// parentOf returns the entry's ParentID — where it attached in the
// tree, empty for a root. A leaf entry's parent is where the leaf
// entry itself was appended; the entry it navigates to is its own
// Entry field, not its parent.
func parentOf(e Entry) string {
	switch e := e.(type) {
	case MessageEntry:
		return e.ParentID
	case TurnEntry:
		return e.ParentID
	case CompactionEntry:
		return e.ParentID
	case BranchSummaryEntry:
		return e.ParentID
	case LeafEntry:
		return e.ParentID
	case LabelEntry:
		return e.ParentID
	case InfoEntry:
		return e.ParentID
	case CustomEntry:
		return e.ParentID
	case CustomMessageEntry:
		return e.ParentID
	case ApprovalRequestEntry:
		return e.ParentID
	case ApprovalDecisionEntry:
		return e.ParentID
	case ApprovalAuditEntry:
		return e.ParentID
	case GrantEntry:
		return e.ParentID
	case GrantRevokedEntry:
		return e.ParentID
	case ReceiptEntry:
		return e.ParentID
	case PoolReceiptEntry:
		return e.ParentID
	}
	return ""
}

// withParent returns the entry with its ParentID replaced — the one
// rewrite a copy of a path may need (Fork): an entry whose stored
// parent the copy does not carry attaches to the nearest entry the
// copy does. The entry's id and everything else are untouched, and
// the stored original is never rewritten — only the copy differs.
func withParent(e Entry, parent string) Entry {
	switch e := e.(type) {
	case MessageEntry:
		e.ParentID = parent
		return e
	case TurnEntry:
		e.ParentID = parent
		return e
	case CompactionEntry:
		e.ParentID = parent
		return e
	case BranchSummaryEntry:
		e.ParentID = parent
		return e
	case LeafEntry:
		e.ParentID = parent
		return e
	case LabelEntry:
		e.ParentID = parent
		return e
	case InfoEntry:
		e.ParentID = parent
		return e
	case CustomEntry:
		e.ParentID = parent
		return e
	case CustomMessageEntry:
		e.ParentID = parent
		return e
	case ApprovalRequestEntry:
		e.ParentID = parent
		return e
	case ApprovalDecisionEntry:
		e.ParentID = parent
		return e
	case ApprovalAuditEntry:
		e.ParentID = parent
		return e
	case GrantEntry:
		e.ParentID = parent
		return e
	case GrantRevokedEntry:
		e.ParentID = parent
		return e
	case ReceiptEntry:
		e.ParentID = parent
		return e
	case PoolReceiptEntry:
		e.ParentID = parent
		return e
	}
	return e
}

// Wire discriminators for entry kinds.
const (
	kindMessage          = "message"
	kindTurn             = "turn"
	kindCompaction       = "compaction"
	kindBranchSummary    = "branch_summary"
	kindLeaf             = "leaf"
	kindLabel            = "label"
	kindInfo             = "info"
	kindCustom           = "custom"
	kindCustomMessage    = "custom_message"
	kindApprovalRequest  = "approval_request"
	kindApprovalDecision = "approval_decision"
	kindApprovalAudit    = "approval_audit"
	kindGrant            = "grant"
	kindGrantRevoked     = "grant_revoked"
	kindReceipt          = "receipt"
	kindPoolReceipt      = "pool_receipt"
)

// The per-type MarshalJSON methods below are deliberately repetitive,
// for the reason recorded at the events' wire-alias declaration
// (events.go in the core, copied here for the same sealed-set cost): a
// generic helper cannot preserve the flattened wire shape, and the
// *Wire aliases have no methods, so encoding them uses the plain
// struct encoding — the MarshalJSON methods add the discriminator
// without recursing into themselves.
type (
	messageEntryWire          MessageEntry
	turnEntryWire             TurnEntry
	compactionEntryWire       CompactionEntry
	branchSummaryEntryWire    BranchSummaryEntry
	leafEntryWire             LeafEntry
	labelEntryWire            LabelEntry
	infoEntryWire             InfoEntry
	customEntryWire           CustomEntry
	customMessageEntryWire    CustomMessageEntry
	approvalRequestEntryWire  ApprovalRequestEntry
	approvalDecisionEntryWire ApprovalDecisionEntry
	approvalAuditEntryWire    ApprovalAuditEntry
	grantEntryWire            GrantEntry
	grantRevokedEntryWire     GrantRevokedEntry
	receiptEntryWire          ReceiptEntry
	poolReceiptEntryWire      PoolReceiptEntry
)

// approvalEntryV is the entry version the approval kinds carry on the
// wire (ADR 0011 §6, ADR 0021): the approvals format is 2, so a
// format-1 reader fails loudly on a session that used approvals
// instead of guessing at kinds it does not know.
const approvalEntryV = 2

// receiptEntryV is the entry version the steering receipt carries on
// the wire (ADR 0011 §6, ADR 0019): the steering format is 3, so a
// format-2 reader fails loudly on a session that steered.
const receiptEntryV = 3

// poolReceiptV is the entry version the pool receipt carries on the
// wire (ADR 0011 §6, ADR 0022): the pool's format is 4, so a
// format-3 reader fails loudly on a session that used the pool
// instead of guessing at a kind it does not know.
const poolReceiptV = 4

// trimRecordV is the entry version a compaction entry carries on the
// wire when it holds a trim record (ADR 0011 §6, ADR 0020's 2026-10-01
// amendment): the record is what the context replays, so a reader
// from before it fails loudly on such an entry instead of replaying
// the trim from its own options. A compaction entry without a trim
// record stays a format-1 line with no "v".
const trimRecordV = 5

// MarshalJSON encodes the entry with its "type" discriminator.
func (e MessageEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		messageEntryWire
	}{kindMessage, messageEntryWire(e)})
}

// MarshalJSON encodes the entry with its "type" discriminator.
func (e TurnEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		turnEntryWire
	}{kindTurn, turnEntryWire(e)})
}

// MarshalJSON encodes the entry with its "type" discriminator. A
// summary compaction is a format-1 line and carries no "v"; an entry
// holding a trim record carries "v":5, its minimum reader version.
func (e CompactionEntry) MarshalJSON() ([]byte, error) {
	v := 0
	if e.Trim != nil {
		v = trimRecordV
	}
	return json.Marshal(struct {
		Type string `json:"type"`
		V    int    `json:"v,omitempty"`
		compactionEntryWire
	}{kindCompaction, v, compactionEntryWire(e)})
}

// MarshalJSON encodes the entry with its "type" discriminator.
func (e BranchSummaryEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		branchSummaryEntryWire
	}{kindBranchSummary, branchSummaryEntryWire(e)})
}

// MarshalJSON encodes the entry with its "type" discriminator.
func (e LeafEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		leafEntryWire
	}{kindLeaf, leafEntryWire(e)})
}

// MarshalJSON encodes the entry with its "type" discriminator.
func (e LabelEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		labelEntryWire
	}{kindLabel, labelEntryWire(e)})
}

// MarshalJSON encodes the entry with its "type" discriminator.
func (e InfoEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		infoEntryWire
	}{kindInfo, infoEntryWire(e)})
}

// MarshalJSON encodes the entry with its "type" discriminator.
func (e CustomEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		customEntryWire
	}{kindCustom, customEntryWire(e)})
}

// MarshalJSON encodes the entry with its "type" discriminator.
func (e CustomMessageEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		customMessageEntryWire
	}{kindCustomMessage, customMessageEntryWire(e)})
}

// The approval kinds marshal with "v":2 — their minimum-reader version
// (ADR 0011 §6) — beside the "type" discriminator; the format-1 kinds
// above omit "v" and always will.

// MarshalJSON encodes the entry with its "type" discriminator and
// "v":2.
func (e ApprovalRequestEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		V    int    `json:"v"`
		approvalRequestEntryWire
	}{kindApprovalRequest, approvalEntryV, approvalRequestEntryWire(e)})
}

// MarshalJSON encodes the entry with its "type" discriminator and
// "v":2.
func (e ApprovalDecisionEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		V    int    `json:"v"`
		approvalDecisionEntryWire
	}{kindApprovalDecision, approvalEntryV, approvalDecisionEntryWire(e)})
}

// MarshalJSON encodes the entry with its "type" discriminator and
// "v":2.
func (e ApprovalAuditEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		V    int    `json:"v"`
		approvalAuditEntryWire
	}{kindApprovalAudit, approvalEntryV, approvalAuditEntryWire(e)})
}

// MarshalJSON encodes the entry with its "type" discriminator and
// "v":2.
func (e GrantEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		V    int    `json:"v"`
		grantEntryWire
	}{kindGrant, approvalEntryV, grantEntryWire(e)})
}

// MarshalJSON encodes the entry with its "type" discriminator and
// "v":2.
func (e GrantRevokedEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		V    int    `json:"v"`
		grantRevokedEntryWire
	}{kindGrantRevoked, approvalEntryV, grantRevokedEntryWire(e)})
}

// MarshalJSON encodes the entry with its "type" discriminator and
// "v":3.
func (e ReceiptEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		V    int    `json:"v"`
		receiptEntryWire
	}{kindReceipt, receiptEntryV, receiptEntryWire(e)})
}

// MarshalJSON encodes the entry with its "type" discriminator and
// "v":4.
func (e PoolReceiptEntry) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type string `json:"type"`
		V    int    `json:"v"`
		poolReceiptEntryWire
	}{kindPoolReceipt, poolReceiptV, poolReceiptEntryWire(e)})
}

// kindVersion returns the highest entry version this build reads for a
// wire kind (ADR 0011 §6). Kinds born in format 1 are version 1 and
// carry no "v" on the wire; a kind added after format 1 — approvals,
// steering receipts, pool receipts — is written with "v":N, its
// minimum reader version, and registers here at that version. A
// format-1 kind that later gains a field an older reader would misread
// writes "v":N on the entries that carry it, and registers at N the
// same way (the compaction entry's trim record). A reader that
// does not know a kind at all, or knows it only at a lower version,
// fails loudly (UnmarshalEntry) instead of guessing.
func kindVersion(kind string) (int, bool) {
	switch kind {
	case kindMessage, kindTurn, kindBranchSummary,
		kindLeaf, kindLabel, kindInfo, kindCustom, kindCustomMessage:
		return 1, true
	case kindCompaction:
		// Born in format 1; read up to the trim record's version.
		return trimRecordV, true
	case kindApprovalRequest, kindApprovalDecision, kindApprovalAudit,
		kindGrant, kindGrantRevoked:
		return approvalEntryV, true
	case kindReceipt:
		return receiptEntryV, true
	case kindPoolReceipt:
		return poolReceiptV, true
	}
	return 0, false
}

// UnmarshalEntry decodes one entry line, dispatching on its "type"
// discriminator. The loud rules of the format (ADR 0011 §5–§6): an
// entry kind this build does not know fails with ErrNewerFormat — it
// was written by a newer weft and is never silently skipped; a known
// kind carrying a "v" above the version this build reads of it fails
// the same way; every other unknown key on the line is additive and
// ignored.
func UnmarshalEntry(b []byte) (Entry, error) {
	var head struct {
		Type string `json:"type"`
		V    int    `json:"v"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return nil, err
	}
	if head.Type == "" {
		return nil, fmt.Errorf("thread: entry has no %q field", "type")
	}
	ver, known := kindVersion(head.Type)
	if !known {
		return nil, fmt.Errorf("%w: entry kind %q is unknown to this build", ErrNewerFormat, head.Type)
	}
	if head.V > ver {
		return nil, fmt.Errorf("%w: entry kind %q carries v=%d, this build reads v=%d", ErrNewerFormat, head.Type, head.V, ver)
	}
	switch head.Type {
	case kindMessage:
		var v messageEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return MessageEntry(v), nil
	case kindTurn:
		var v turnEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return TurnEntry(v), nil
	case kindCompaction:
		var v compactionEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return CompactionEntry(v), nil
	case kindBranchSummary:
		var v branchSummaryEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return BranchSummaryEntry(v), nil
	case kindLeaf:
		var v leafEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return LeafEntry(v), nil
	case kindLabel:
		var v labelEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return LabelEntry(v), nil
	case kindInfo:
		var v infoEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return InfoEntry(v), nil
	case kindCustom:
		var v customEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return CustomEntry(v), nil
	case kindCustomMessage:
		var v customMessageEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return CustomMessageEntry(v), nil
	case kindApprovalRequest:
		var v approvalRequestEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return ApprovalRequestEntry(v), nil
	case kindApprovalDecision:
		var v approvalDecisionEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return ApprovalDecisionEntry(v), nil
	case kindApprovalAudit:
		var v approvalAuditEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return ApprovalAuditEntry(v), nil
	case kindGrant:
		var v grantEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return GrantEntry(v), nil
	case kindGrantRevoked:
		var v grantRevokedEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return GrantRevokedEntry(v), nil
	case kindReceipt:
		var v receiptEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return ReceiptEntry(v), nil
	case kindPoolReceipt:
		var v poolReceiptEntryWire
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return PoolReceiptEntry(v), nil
	default:
		// Unreachable — kindVersion gates the switch — but a kind
		// registered there and forgotten here must never decode as
		// something else.
		return nil, fmt.Errorf("thread: entry kind %q passed the version gate but has no decoder", head.Type)
	}
}
