package thread

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/weftgo/weft/core"
	"github.com/weftgo/weft/core/wefttest"
)

// kindSamples holds one fully populated value of every entry kind in
// the sealed set: every field that can alias — slice, map, pointer,
// raw JSON, and the parts inside a message — is set, so a clone that
// shares any of them is caught. TestKindSamplesCoverTheSealedSet
// fails when a kind is added to entry.go and not here.
func kindSamples() []Entry {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	msg := func() core.Message {
		return core.Message{Role: core.RoleAssistant, Content: []core.Part{
			core.TextPart{Text: "text"},
			core.ReasoningPart{Text: "why", Signature: "sig"},
			core.ToolCallPart{ID: "call_1", Name: "lookup", Args: json.RawMessage(`{"order":1}`)},
			core.ToolResultPart{CallID: "call_1", Name: "lookup", Content: "ok"},
			core.FilePart{MediaType: "image/png", Data: []byte{1, 2, 3, 4}},
		}}
	}
	steer := msg()
	return []Entry{
		MessageEntry{ID: "e_message", ParentID: "e_p", Created: at, Message: msg()},
		TurnEntry{ID: "e_turn", ParentID: "e_p", Created: at, RunID: "s-t1",
			Pending: []core.ToolCallPart{{ID: "call_9", Name: "refund", Args: json.RawMessage(`{"a":1}`)}}},
		CompactionEntry{ID: "e_compaction", ParentID: "e_p", Created: at, Summary: "s", FirstKept: "e_k",
			Trim:      &TrimRecord{Stubs: []TrimStub{{Entry: "e_k", CallID: "c1", Content: "stub"}}},
			FilesRead: []string{"a.go"}, FilesModified: []string{"b.go"}, Pinned: []string{"e_pin"}},
		BranchSummaryEntry{ID: "e_branch_summary", ParentID: "e_p", Created: at, Summary: "s", FromEntry: "e_f"},
		LeafEntry{ID: "e_leaf", ParentID: "e_p", Created: at, Entry: "e_message"},
		LabelEntry{ID: "e_label", ParentID: "e_p", Created: at, Entry: "e_t", Name: "n"},
		InfoEntry{ID: "e_info", ParentID: "e_p", Created: at, Title: "t", Meta: map[string]string{"k": "v"}},
		CustomEntry{ID: "e_custom", ParentID: "e_p", Created: at, Kind: "cart", Data: json.RawMessage(`{"items":2}`)},
		CustomMessageEntry{ID: "e_custom_message", ParentID: "e_p", Created: at, Kind: "note", Message: msg()},
		ApprovalRequestEntry{ID: "e_request", ParentID: "e_p", Created: at, CallID: "call_1", Tool: "refund",
			Args: json.RawMessage(`{"order":1}`), ArgsSHA256: "abc", RunID: "s-t1"},
		ApprovalDecisionEntry{ID: "e_decision", ParentID: "e_p", Created: at, CallID: "call_1", Outcome: OutcomeApprove},
		ApprovalAuditEntry{ID: "e_audit", ParentID: "e_p", Created: at, CallID: "call_1", Step: StepResume, Decisions: []string{"e_d"}},
		GrantEntry{ID: "e_grant", ParentID: "e_p", Created: at, Grant: Grant{Tool: "refund",
			Args: []Arg{ArgEquals("/order", json.RawMessage(`1`)), ArgEquals("/sku", json.RawMessage(`"a"`))}}},
		GrantRevokedEntry{ID: "e_revoked", ParentID: "e_p", Created: at, GrantID: "e_grant"},
		ReceiptEntry{ID: "e_receipt", ParentID: "e_p", Created: at, Status: ReceiptQueued, Msg: &steer},
		PoolReceiptEntry{ID: "e_pool", ParentID: "e_p", Created: at, Status: PoolAccepted, Child: "s_child",
			Usage: core.Usage{InputTokens: 3}},
	}
}

// The samples name every type of the sealed set — read from the
// source, where each kind declares itself with an isEntry method — so
// the tables below cannot fall behind a new kind.
func TestKindSamplesCoverTheSealedSet(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "entry.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var sealed []string
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "isEntry" || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		if id, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
			sealed = append(sealed, id.Name)
		}
	}
	var sampled []string
	for _, e := range kindSamples() {
		sampled = append(sampled, reflect.TypeOf(e).Name())
	}
	sort.Strings(sealed)
	sort.Strings(sampled)
	if len(sealed) < 16 {
		t.Fatalf("found %d sealed kinds in entry.go; the scan is broken", len(sealed))
	}
	if !reflect.DeepEqual(sealed, sampled) {
		t.Fatalf("kindSamples covers\n  %v\nthe sealed set is\n  %v\nadd the new kind to kindSamples (and give cloneEntry and withParent a case)", sampled, sealed)
	}
}

// unsetAliasable reports every slice, map and pointer reachable in v
// that is nil or empty — a field the sample forgot to set, which the clone
// test could then not catch being shared.
func unsetAliasable(v reflect.Value, path string, out *[]string) {
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			unsetAliasable(v.Elem(), path, out)
		}
	case reflect.Pointer:
		if v.IsNil() {
			*out = append(*out, path)
			return
		}
		unsetAliasable(v.Elem(), path, out)
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			return
		}
		for i := 0; i < v.NumField(); i++ {
			unsetAliasable(v.Field(i), path+"."+v.Type().Field(i).Name, out)
		}
	case reflect.Slice:
		if v.Len() == 0 {
			*out = append(*out, path)
			return
		}
		for i := 0; i < v.Len(); i++ {
			unsetAliasable(v.Index(i), path+"[]", out)
		}
	case reflect.Map:
		if v.Len() == 0 {
			*out = append(*out, path)
		}
	}
}

// scribble overwrites everything reachable from v that a caller
// holding a snapshot could write to: slice elements, map values, the
// bytes behind raw JSON and file data, the message behind a pointer.
// What it cannot reach through v is exactly what the snapshot does
// not share.
func scribble(v reflect.Value) {
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			scribble(v.Elem())
		}
	case reflect.Pointer:
		if !v.IsNil() {
			scribble(v.Elem())
		}
	case reflect.Struct:
		if v.Type() == reflect.TypeOf(time.Time{}) {
			return
		}
		for i := 0; i < v.NumField(); i++ {
			scribble(v.Field(i))
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			el := v.Index(i) // always addressable: it is the backing array
			scribble(el)
			switch el.Kind() {
			case reflect.Uint8:
				el.SetUint('X')
			case reflect.String:
				el.SetString("scribbled")
			case reflect.Interface, reflect.Struct:
				el.Set(reflect.Zero(el.Type()))
			}
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			scribble(v.MapIndex(k))
			v.SetMapIndex(k, reflect.Zero(v.Type().Elem()))
		}
		if v.Type().Key().Kind() == reflect.String {
			v.SetMapIndex(reflect.ValueOf("scribbled").Convert(v.Type().Key()), reflect.Zero(v.Type().Elem()))
		}
	}
}

// Entries, Path and Audit hand out deep copies of every kind in the
// sealed set: a caller that scribbles over everything a snapshot lets
// it reach leaves the session — and what it stores next — unchanged.
func TestSnapshotsShareNothingWithTheSession(t *testing.T) {
	for _, sample := range kindSamples() {
		var unset []string
		unsetAliasable(reflect.ValueOf(sample), reflect.TypeOf(sample).Name(), &unset)
		for _, f := range unset {
			t.Errorf("kindSamples: %s is unset — the clone test cannot see it being shared", f)
		}
	}

	ctx := context.Background()
	st := Memory()
	agent := core.New(wefttest.Script())
	s, err := Create(ctx, st, agent)
	if err != nil {
		t.Fatal(err)
	}
	// One chain, so Path of the last entry walks every kind.
	samples := kindSamples()
	parent := ""
	for i, e := range samples {
		samples[i] = withParent(e, parent)
		parent = idOf(e)
	}
	if err := st.Append(ctx, s.ID(), samples...); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, st, s.ID(), agent)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	want, err := json.Marshal(s.Entries())
	if err != nil {
		t.Fatal(err)
	}
	unchanged := func(after string) {
		t.Helper()
		s.mu.Lock()
		got, err := json.Marshal(s.order)
		s.mu.Unlock()
		if err != nil {
			t.Fatalf("after scribbling over %s the tree no longer encodes: %v", after, err)
		}
		if string(got) != string(want) {
			t.Errorf("scribbling over %s changed the session's tree:\n got %s\nwant %s", after, got, want)
		}
	}

	entries := s.Entries()
	if len(entries) != len(samples) {
		t.Fatalf("Entries = %d, want %d", len(entries), len(samples))
	}
	for i := range entries {
		v := reflect.New(reflect.TypeOf(entries[i])).Elem()
		v.Set(reflect.ValueOf(entries[i]))
		scribble(v)
		unchanged("Entries()[" + reflect.TypeOf(entries[i]).Name() + "]")
	}

	path, err := s.Path(parent)
	if err != nil || len(path) != len(samples) {
		t.Fatalf("Path = %d entries, %v; want %d", len(path), err, len(samples))
	}
	scribble(reflect.ValueOf(&path).Elem())
	unchanged("Path")

	audit := s.Audit()
	scribble(reflect.ValueOf(&audit).Elem())
	unchanged("Audit")

	// And the direct statement, per kind: the clone differs from its
	// source after the scribble, the source does not move.
	for _, sample := range kindSamples() {
		before, _ := json.Marshal(sample)
		clone := cloneEntry(sample)
		v := reflect.New(reflect.TypeOf(clone)).Elem()
		v.Set(reflect.ValueOf(clone))
		scribble(v)
		after, _ := json.Marshal(sample)
		if string(before) != string(after) {
			t.Errorf("cloneEntry(%T) shares state with its source:\n before %s\n after  %s", sample, before, after)
		}
	}
}

// withParent rewrites the parent of every kind and nothing else.
func TestWithParentEveryKind(t *testing.T) {
	for _, e := range kindSamples() {
		got := withParent(e, "e_new")
		if parentOf(got) != "e_new" {
			t.Errorf("withParent(%T) left parent %q", e, parentOf(got))
		}
		if idOf(got) != idOf(e) || reflect.TypeOf(got) != reflect.TypeOf(e) {
			t.Errorf("withParent(%T) changed the entry's identity", e)
		}
		if !reflect.DeepEqual(withParent(got, parentOf(e)), e) {
			t.Errorf("withParent(%T) changed more than the parent", e)
		}
	}
}

// The walk is defensive: Open's validation makes a broken link
// impossible, and a tree that has one anyway — memory disagreeing
// with what was vetted — fails the walk with ErrCorrupt instead of
// returning a path, and so a context, cut short.
func TestPathFailsOnABrokenLink(t *testing.T) {
	ctx := context.Background()
	at := time.Unix(1, 0).UTC()
	build := func(entries ...Entry) *Session {
		t.Helper()
		st := Memory()
		agent := core.New(wefttest.Script())
		s, err := Create(ctx, st, agent)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Append(ctx, s.ID(), entries...); err != nil {
			t.Fatal(err)
		}
		s, err = Open(ctx, st, s.ID(), agent)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	chain := func() []Entry {
		return []Entry{
			MessageEntry{ID: "e_1", Created: at, Message: core.User("one")},
			MessageEntry{ID: "e_2", ParentID: "e_1", Created: at, Message: core.User("two")},
			MessageEntry{ID: "e_3", ParentID: "e_2", Created: at, Message: core.User("three")},
		}
	}

	// A parent the tree does not hold.
	s := build(chain()...)
	s.mu.Lock()
	s.order[1] = withParent(s.order[1], "e_gone")
	s.mu.Unlock()
	_, err := s.Path("e_3")
	var ce *CorruptError
	if !errors.Is(err, ErrCorrupt) || !errors.As(err, &ce) || ce.Entry != "e_2" {
		t.Errorf("Path over a missing parent: err = %v, want ErrCorrupt naming e_2", err)
	}

	// A parent that does not precede its child: the walk must end, and
	// say so.
	s = build(chain()...)
	s.mu.Lock()
	s.order[0] = withParent(s.order[0], "e_3")
	s.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := s.Path("e_3"); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrCorrupt) {
			t.Errorf("Path over a cycle: err = %v, want ErrCorrupt", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Path over a cycle never returned")
	}
}

// Fork builds its Session through the constructor Open uses: the
// state a reopen of the fork's file derives — the compaction trigger's
// measurement, the per-model window, the run counter — is the state
// the fork starts with.
func TestForkDerivesStateLikeOpen(t *testing.T) {
	ctx := context.Background()
	st := Memory()
	model := wefttest.Script(wefttest.Say("one").WithUsage(core.Usage{InputTokens: 1200, OutputTokens: 10}))
	agent := core.New(model)
	windows := ModelWindows(map[core.ModelInfo]int64{core.InfoOf(model): 64_000})
	s, err := Create(ctx, st, agent, windows)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := s.Send(ctx, core.User("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := turn.Wait(); err != nil {
		t.Fatal(err)
	}
	if s.lastInput == 0 {
		t.Fatal("the turn recorded no measurement; the test has nothing to compare")
	}

	f, err := s.Fork(ctx, s.Leaf(), windows)
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	re, err := Open(ctx, st, f.ID(), agent, windows)
	if err != nil {
		t.Fatalf("Open the fork: %v", err)
	}
	if f.cfg.compaction.window != 64_000 {
		t.Errorf("fork window = %d, want the per-model 64000 — Fork skipped the compaction resolve", f.cfg.compaction.window)
	}
	if f.lastInput != s.lastInput || f.lastMeasureLeaf != s.lastMeasureLeaf {
		t.Errorf("fork measurement = %d at %q, want the copied turn's %d at %q",
			f.lastInput, f.lastMeasureLeaf, s.lastInput, s.lastMeasureLeaf)
	}
	for name, got := range map[string][2]any{
		"window":          {f.cfg.compaction.window, re.cfg.compaction.window},
		"lastInput":       {f.lastInput, re.lastInput},
		"lastMeasureLeaf": {f.lastMeasureLeaf, re.lastMeasureLeaf},
		"leaf":            {f.leaf, re.leaf},
		"turns":           {f.turns, re.turns},
		"turnSeq":         {f.turnSeq, re.turnSeq},
	} {
		if got[0] != got[1] {
			t.Errorf("%s: fork has %v, a reopen of its file has %v", name, got[0], got[1])
		}
	}
}
