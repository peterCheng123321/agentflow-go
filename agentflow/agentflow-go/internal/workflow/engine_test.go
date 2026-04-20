package workflow

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestWorkflowLifecycle(t *testing.T) {
	e := NewEngine(10, "", nil)
	c := e.CreateCase("Test Client", "Civil Litigation", "Test", "Msg")

	if c.State != StateClientCapture {
		t.Errorf("Expected initial state %s, got %s", StateClientCapture, c.State)
	}

	// 1. Advance to INITIAL_CONTACT
	err := e.AdvanceState(c.CaseID)
	if err != nil {
		t.Fatalf("Advance to INITIAL_CONTACT failed: %v", err)
	}

	snap, _ := e.GetCaseSnapshot(c.CaseID)
	if snap.State != StateInitialContact {
		t.Errorf("Expected state %s, got %s", StateInitialContact, snap.State)
	}

	// 2. Advance to CASE_EVALUATION (HITL gate)
	// Must approve BEFORE advancing TO StateCaseEvaluation
	err = e.ApproveHITL(c.CaseID, StateCaseEvaluation, true, "Looks good")
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	err = e.AdvanceState(c.CaseID)
	if err != nil {
		t.Fatalf("Advance to CASE_EVALUATION failed: %v", err)
	}

	snap, _ = e.GetCaseSnapshot(c.CaseID)
	if snap.State != StateCaseEvaluation {
		t.Errorf("Expected state %s, got %s", StateCaseEvaluation, snap.State)
	}

	// 3. Advance to FEE_COLLECTION
	err = e.AdvanceState(c.CaseID)
	if err != nil {
		t.Fatalf("Advance to FEE_COLLECTION failed: %v", err)
	}

	snap, _ = e.GetCaseSnapshot(c.CaseID)
	if snap.State != StateFeeCollection {
		t.Errorf("Expected state %s, got %s", StateFeeCollection, snap.State)
	}
}

func TestDeadlockStress(t *testing.T) {
	e := NewEngine(100, "", nil)
	caseID := e.CreateCase("Stress Client", "Civil", "Stress", "").CaseID

	const goroutines = 20
	const iterations = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				switch (id + j) % 5 {
				case 0:
					e.AddNote(caseID, fmt.Sprintf("Note %d", j))
				case 1:
					e.GetCaseSnapshot(caseID)
				case 2:
					e.AdvanceState(caseID)
				case 3:
					e.ApproveHITL(caseID, StateCaseEvaluation, true, "Bulk")
				case 4:
					e.AttachDocument(caseID, "doc.txt")
				}
			}
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Deadlock detected")
	}
}

func TestEviction(t *testing.T) {
	e := NewEngine(2, "", nil)
	e.CreateCase("C1", "Type", "Source", "")
	time.Sleep(time.Millisecond)
	e.CreateCase("C2", "Type", "Source", "")
	time.Sleep(time.Millisecond)
	e.CreateCase("C3", "Type", "Source", "")

	cases := e.ListCases()
	if len(cases) != 2 {
		t.Errorf("Expected 2 cases, got %d", len(cases))
	}
}
