package domain

import "testing"

func TestActionResultsStayFrozenAndThreadStopsDependents(t *testing.T) {
	a := CurationAction{Status: "RUNNING"}
	a.ReconcileJobs([]ActionJobResult{{JobID: "one", Status: "SUCCEEDED", Effects: []ActionEffect{{Kind: "CANDIDATES_ADDED", Count: 2}}}, {JobID: "two", Status: "RUNNING"}})
	if a.Status != "RUNNING" || len(a.Jobs) != 2 {
		t.Fatal(a)
	}
	a.ReconcileJobs([]ActionJobResult{{JobID: "one", Status: "SUCCEEDED", Effects: []ActionEffect{{Kind: "CANDIDATES_ADDED", Count: 2}}}, {JobID: "two", Status: "FAILED", ReasonCode: "CATALOG_UNAVAILABLE"}})
	if a.Status != "FAILED" || a.ReasonCode != ReasonPartialFailure || a.Jobs[0].Effects[0].Count != 2 || a.Jobs[1].ReasonCode != "CATALOG_UNAVAILABLE" {
		t.Fatal(a)
	}
	a.ReconcileJobs([]ActionJobResult{{JobID: "two", Status: "PENDING"}})
	if len(a.Jobs) != 2 || a.Status != "FAILED" {
		t.Fatal("retry rewrote terminal receipt")
	}
	if a.Transition("RUNNING") == nil {
		t.Fatal("terminal Action reopened")
	}
	thread := CurationThread{Status: "RUNNING", Actions: []CurationAction{{Status: "SUCCEEDED"}, a, {Status: "PENDING"}}}
	thread.RefreshStatus()
	if thread.Status != "FAILED" || thread.ReasonCode != ReasonPartialFailure || thread.Actions[2].Status != "SKIPPED" {
		t.Fatal(thread)
	}
}
func TestActionWithoutAnySucceededJobFailsPlainly(t *testing.T) {
	a := CurationAction{Status: "RUNNING"}
	a.ReconcileJobs([]ActionJobResult{{JobID: "one", Status: "FAILED", ReasonCode: "DEADLINE_EXCEEDED"}, {JobID: "two", Status: "CANCELLED"}})
	if a.Status != "FAILED" || a.ReasonCode != "CURATION_PRIMITIVE_FAILED" {
		t.Fatal(a)
	}
	b := CurationAction{Status: "RUNNING"}
	b.ReconcileJobs([]ActionJobResult{{JobID: "one", Status: "SUCCEEDED", Effects: []ActionEffect{{Kind: "NO_RESULTS"}}}, {JobID: "two", Status: "SUCCEEDED", Effects: []ActionEffect{{Kind: "CANDIDATES_ADDED", Count: 3}}}})
	if b.Status != "SUCCEEDED" || b.ReasonCode != "" {
		t.Fatal(b)
	}
}
func TestActionSelectionWaitAndCancellation(t *testing.T) {
	a := CurationAction{Type: CurationActionAutoStart, Status: "RUNNING"}
	if a.Transition("WAITING_SELECTION") != nil || a.Transition("PENDING") != nil || a.Transition("RUNNING") != nil {
		t.Fatal(a)
	}
	t1 := CurationThread{Status: "INTERPRETING", Actions: []CurationAction{{Status: "SUCCEEDED"}, a, {Status: "PENDING"}}}
	t1.Cancel()
	if t1.Actions[0].Status != "SUCCEEDED" || t1.Actions[1].Status != "CANCELLED" || t1.Actions[2].Status != "SKIPPED" {
		t.Fatal(t1)
	}
}
