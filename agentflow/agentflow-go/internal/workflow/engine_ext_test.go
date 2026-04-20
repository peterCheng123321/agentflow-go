package workflow

import (
	"strings"
	"testing"
	"time"
)

func TestFullLifecycleToArchive(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Full Client", "Civil Litigation", "Test", "Initial message")

	if c.State != StateClientCapture {
		t.Fatalf("expected initial state %s, got %s", StateClientCapture, c.State)
	}

	states := []string{
		StateClientCapture,
		StateInitialContact,
		StateCaseEvaluation,
		StateFeeCollection,
		StateGroupCreation,
		StateMaterialIngestion,
		StateDocumentGeneration,
		StateClientApproval,
		StateFinalPDFSend,
		StateArchiveClose,
	}

	for i := 1; i < len(states); i++ {
		targetState := states[i]
		if hitlGates[targetState] {
			err := e.ApproveHITL(c.CaseID, targetState, true, "approved")
			if err != nil {
				t.Fatalf("ApproveHITL for %s failed: %v", targetState, err)
			}
		}
		err := e.AdvanceState(c.CaseID)
		if err != nil {
			t.Fatalf("Advance to %s failed: %v", targetState, err)
		}
		snap, _ := e.GetCaseSnapshot(c.CaseID)
		if snap.State != targetState {
			t.Errorf("at step %d: expected state %s, got %s", i, targetState, snap.State)
		}
	}
}

func TestAdvancePastFinalState(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Client", "Type", "Source", "")

	for _, state := range []string{
		StateInitialContact,
		StateCaseEvaluation,
		StateFeeCollection,
		StateGroupCreation,
		StateMaterialIngestion,
		StateDocumentGeneration,
		StateClientApproval,
		StateFinalPDFSend,
		StateArchiveClose,
	} {
		if hitlGates[state] {
			e.ApproveHITL(c.CaseID, state, true, "")
		}
		e.AdvanceState(c.CaseID)
	}

	err := e.AdvanceState(c.CaseID)
	if err == nil {
		t.Fatal("expected error when advancing past final state")
	}
}

func TestAdvanceWithoutHITLApproval(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Client", "Type", "Source", "")

	e.AdvanceState(c.CaseID)
	e.AdvanceState(c.CaseID)

	err := e.AdvanceState(c.CaseID)
	if err == nil {
		t.Fatal("expected error when advancing to HITL state without approval")
	}
	if !strings.Contains(err.Error(), "HITL") {
		t.Errorf("expected HITL error message, got %v", err)
	}
}

func TestAdvanceNonExistentCase(t *testing.T) {
	e := NewEngine(10, "", nil)
	err := e.AdvanceState("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent case")
	}
}

func TestApproveHITLNonExistentCase(t *testing.T) {
	e := NewEngine(10, "", nil)
	err := e.ApproveHITL("nonexistent", StateCaseEvaluation, true, "")
	if err == nil {
		t.Fatal("expected error for nonexistent case")
	}
}

func TestApproveHITLNonHITLState(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Client", "Type", "Source", "")

	err := e.ApproveHITL(c.CaseID, StateInitialContact, true, "")
	if err == nil {
		t.Fatal("expected error for non-HITL state approval")
	}
}

func TestHITLRejectionThenApproval(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Client", "Type", "Source", "")

	e.AdvanceState(c.CaseID)

	e.ApproveHITL(c.CaseID, StateCaseEvaluation, false, "needs more info")

	err := e.AdvanceState(c.CaseID)
	if err == nil {
		t.Fatal("expected error after HITL rejection")
	}

	e.ApproveHITL(c.CaseID, StateCaseEvaluation, true, "approved now")

	err = e.AdvanceState(c.CaseID)
	if err != nil {
		t.Fatalf("expected success after re-approval, got %v", err)
	}

	snap, _ := e.GetCaseSnapshot(c.CaseID)
	if snap.State != StateCaseEvaluation {
		t.Errorf("expected state %s, got %s", StateCaseEvaluation, snap.State)
	}
}

func TestAddNoteNonExistentCase(t *testing.T) {
	e := NewEngine(10, "", nil)
	e.AddNote("nonexistent", "some note")
}

func TestAttachDocumentDuplicate(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Client", "Type", "Source", "")

	e.AttachDocument(c.CaseID, "doc.txt")
	e.AttachDocument(c.CaseID, "doc.txt")

	snap, _ := e.GetCaseSnapshot(c.CaseID)
	if len(snap.UploadedDocuments) != 1 {
		t.Errorf("expected 1 document (dedup), got %d", len(snap.UploadedDocuments))
	}
}

func TestAttachDocumentWithExtras(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Client", "Type", "Source", "")

	e.AttachDocument(c.CaseID, "doc.txt", map[string]interface{}{
		"classification": map[string]interface{}{
			"document_type": "civil_complaint",
		},
	})

	snap, _ := e.GetCaseSnapshot(c.CaseID)
	if len(snap.AIFileSummaries) != 1 {
		t.Fatalf("expected 1 AI file summary, got %d", len(snap.AIFileSummaries))
	}
	if snap.AIFileSummaries[0]["filename"] != "doc.txt" {
		t.Errorf("expected filename 'doc.txt', got %v", snap.AIFileSummaries[0]["filename"])
	}
}

func TestDetachDocument(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Client", "Type", "Source", "")

	e.AttachDocument(c.CaseID, "doc1.txt")
	e.AttachDocument(c.CaseID, "doc2.txt")

	err := e.DetachDocument(c.CaseID, "doc1.txt")
	if err != nil {
		t.Fatalf("DetachDocument failed: %v", err)
	}

	snap, _ := e.GetCaseSnapshot(c.CaseID)
	if len(snap.UploadedDocuments) != 1 {
		t.Errorf("expected 1 document after detach, got %d", len(snap.UploadedDocuments))
	}
	if snap.UploadedDocuments[0] != "doc2.txt" {
		t.Errorf("expected 'doc2.txt', got %q", snap.UploadedDocuments[0])
	}
}

func TestDetachNonExistentDocument(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Client", "Type", "Source", "")
	e.AttachDocument(c.CaseID, "doc1.txt")

	err := e.DetachDocument(c.CaseID, "nonexistent.txt")
	if err != nil {
		t.Fatalf("DetachDocument silently ignores nonexistent docs: %v", err)
	}

	snap, _ := e.GetCaseSnapshot(c.CaseID)
	if len(snap.UploadedDocuments) != 1 {
		t.Errorf("expected 1 document (no change), got %d", len(snap.UploadedDocuments))
	}
}

func TestDetachNonExistentCase(t *testing.T) {
	e := NewEngine(10, "", nil)
	err := e.DetachDocument("nonexistent", "doc.txt")
	if err == nil {
		t.Fatal("expected error for nonexistent case")
	}
}

func TestSetAICaseSummary(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Client", "Type", "Source", "")

	err := e.SetAICaseSummary(c.CaseID, "This is a comprehensive AI summary.")
	if err != nil {
		t.Fatalf("SetAICaseSummary failed: %v", err)
	}

	snap, _ := e.GetCaseSnapshot(c.CaseID)
	if snap.AICaseSummary != "This is a comprehensive AI summary." {
		t.Errorf("expected AI summary, got %q", snap.AICaseSummary)
	}
}

func TestSetAICaseSummaryNonExistent(t *testing.T) {
	e := NewEngine(10, "", nil)
	err := e.SetAICaseSummary("nonexistent", "summary")
	if err == nil {
		t.Fatal("expected error for nonexistent case")
	}
}

func TestUpdateCase(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Old Name", "Old Type", "Source", "")

	err := e.UpdateCase(c.CaseID, "New Name", "New Type")
	if err != nil {
		t.Fatalf("UpdateCase failed: %v", err)
	}

	snap, _ := e.GetCaseSnapshot(c.CaseID)
	if snap.ClientName != "New Name" {
		t.Errorf("expected client name 'New Name', got %q", snap.ClientName)
	}
	if snap.MatterType != "New Type" {
		t.Errorf("expected matter type 'New Type', got %q", snap.MatterType)
	}
}

func TestUpdateCaseNonExistent(t *testing.T) {
	e := NewEngine(10, "", nil)
	err := e.UpdateCase("nonexistent", "Name", "Type")
	if err == nil {
		t.Fatal("expected error for nonexistent case")
	}
}

func TestDeleteCase(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Client", "Type", "Source", "")

	err := e.DeleteCase(c.CaseID)
	if err != nil {
		t.Fatalf("DeleteCase failed: %v", err)
	}

	cases := e.ListCases()
	if len(cases) != 0 {
		t.Errorf("expected 0 cases after delete, got %d", len(cases))
	}
}

func TestDeleteCaseNonExistent(t *testing.T) {
	e := NewEngine(10, "", nil)
	err := e.DeleteCase("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent case")
	}
}

func TestNodeHistoryAccumulation(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Client", "Type", "Source", "")

	e.AdvanceState(c.CaseID)
	e.AdvanceState(c.CaseID)

	snap, _ := e.GetCaseSnapshot(c.CaseID)
	if len(snap.NodeHistory) != 2 {
		t.Errorf("expected 2 node history entries, got %d", len(snap.NodeHistory))
	}
}

func TestCreateCaseGeneratesID(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Client", "Type", "Source", "")

	if c.CaseID == "" {
		t.Fatal("expected non-empty case ID")
	}
	if !strings.HasPrefix(c.CaseID, "LAW-") {
		t.Errorf("expected case ID to start with 'LAW-', got %q", c.CaseID)
	}
}

func TestCreateCaseTimestamps(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Client", "Type", "Source", "")

	if c.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
	if c.UpdatedAt.IsZero() {
		t.Error("expected non-zero UpdatedAt")
	}
}

func TestDeepCopyCaseIsolation(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Client", "Type", "Source", "")
	e.AttachDocument(c.CaseID, "doc.txt")

	snap1, _ := e.GetCaseSnapshot(c.CaseID)
	docs1 := len(snap1.UploadedDocuments)

	e.AttachDocument(c.CaseID, "doc2.txt")

	snap2, _ := e.GetCaseSnapshot(c.CaseID)
	docs2 := len(snap2.UploadedDocuments)

	if docs1 != 1 {
		t.Errorf("first snapshot should have 1 doc, got %d", docs1)
	}
	if docs2 != 2 {
		t.Errorf("second snapshot should have 2 docs, got %d", docs2)
	}
}

func TestListCasesOrder(t *testing.T) {
	e := NewEngine(10, "", nil)
	e.CreateCase("A", "Type", "Source", "")
	time.Sleep(time.Millisecond)
	e.CreateCase("B", "Type", "Source", "")
	time.Sleep(time.Millisecond)
	e.CreateCase("C", "Type", "Source", "")

	cases := e.ListCases()
	if len(cases) != 3 {
		t.Fatalf("expected 3 cases, got %d", len(cases))
	}
}

func TestGetCaseSnapshotNonExistent(t *testing.T) {
	e := NewEngine(10, "", nil)
	_, ok := e.GetCaseSnapshot("nonexistent")
	if ok {
		t.Error("expected false for nonexistent case")
	}
}
