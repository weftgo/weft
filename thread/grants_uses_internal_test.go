package thread

import (
	"testing"
	"time"
)

// A session grant's uses are its audit entries' GrantID fields and
// nothing else: prose that reads like a match counts for nothing, a
// shared grant's match under a colliding id counts for nothing, and
// the matches a running chain has made but not yet appended count in
// full.
func TestGrantUsesIgnoreProse(t *testing.T) {
	const id = "e_grant"
	s := &Session{order: []Entry{
		GrantEntry{ID: id, Grant: Grant{Tool: "run", MaxUses: 2}},
		ApprovalAuditEntry{ID: "e_a1", Step: StepGrant, Outcome: "approved", Detail: "grant " + id},
		ApprovalAuditEntry{ID: "e_a2", Step: StepGrant, Outcome: "approved", Detail: "shared grant " + id, GrantID: id, GrantShared: true},
		ApprovalAuditEntry{ID: "e_a3", Step: StepPark, Outcome: "parked", GrantID: id},
	}}
	now := time.Now()
	if live := s.liveGrantsLocked(now, nil); len(live) != 1 {
		t.Fatalf("prose, a shared match and another step spent the grant: %d live", len(live))
	}
	s.order = append(s.order, ApprovalAuditEntry{ID: "e_a4", Step: StepGrant, Outcome: "denied", GrantID: id})
	if live := s.liveGrantsLocked(now, nil); len(live) != 1 {
		t.Fatalf("one use of two spent the grant: %d live", len(live))
	}
	if live := s.liveGrantsLocked(now, map[string]int{id: 1}); len(live) != 0 {
		t.Fatalf("a recorded use plus a pending one left a two-use grant live: %d", len(live))
	}
}
