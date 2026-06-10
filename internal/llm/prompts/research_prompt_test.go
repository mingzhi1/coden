package prompts

import (
	"strings"
	"testing"
)

// TestInputterAdvertisesResearch locks the single-source intent kinds: the
// Inputter prompt must offer "research" so the research workflow is reachable.
func TestInputterAdvertisesResearch(t *testing.T) {
	p := Inputter("")
	if !strings.Contains(p, "research") {
		t.Error("Inputter prompt must advertise the research kind (else research workflow is unreachable)")
	}
}

// TestPlannerGathersFirst locks the adaptive-precursor instruction: when materials
// are incomplete, the Planner orders investigation before implementation.
func TestPlannerGathersFirst(t *testing.T) {
	p := Planner("code_gen")
	if !strings.Contains(p, "web_search") {
		t.Error("Planner prompt must mention web_search for external investigation")
	}
	if !strings.Contains(strings.ToLower(p), "investigation task") {
		t.Error("Planner prompt must instruct gather-first (investigation task) when info is incomplete")
	}
}

// TestExecutorResearchesBeforeImplementing locks the research-before-implement
// instruction in the Executor prompt.
func TestExecutorResearchesBeforeImplementing(t *testing.T) {
	p := Executor(true, "")
	if !strings.Contains(p, "web_search") {
		t.Error("Executor prompt must tell it to web_search external knowledge before implementing")
	}
}
