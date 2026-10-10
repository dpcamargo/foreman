package router

import (
	"testing"
	"time"
)

func TestPSuccessPriorDominates(t *testing.T) {
	s := RoutingStats{Alpha: 1, Beta: 1}
	p := s.PSuccess()
	if p != 0.5 {
		t.Errorf("PSuccess() = %v, want 0.5 (prior)", p)
	}
}

func TestPSuccessAfterObservations(t *testing.T) {
	s := RoutingStats{Alpha: 6, Beta: 3, Samples: 8}
	p := s.PSuccess()
	want := 6.0 / 9.0
	if p != want {
		t.Errorf("PSuccess() = %v, want %v", p, want)
	}
}

func TestUpdateSuccess(t *testing.T) {
	now := time.Now()
	s := RoutingStats{Alpha: 1, Beta: 1}
	s2 := s.Update(true, now)
	if s2.Alpha != 2 || s2.Beta != 1 || s2.Samples != 1 {
		t.Errorf("after success: alpha=%d beta=%d samples=%d, want 2/1/1", s2.Alpha, s2.Beta, s2.Samples)
	}
}

func TestUpdateFailure(t *testing.T) {
	now := time.Now()
	s := RoutingStats{Alpha: 1, Beta: 1}
	s2 := s.Update(false, now)
	if s2.Alpha != 1 || s2.Beta != 2 || s2.Samples != 1 {
		t.Errorf("after failure: alpha=%d beta=%d samples=%d, want 1/2/1", s2.Alpha, s2.Beta, s2.Samples)
	}
}

func TestPSuccessZeroTotal(t *testing.T) {
	s := RoutingStats{}
	if p := s.PSuccess(); p != 0.5 {
		t.Errorf("PSuccess() = %v, want 0.5 for zero stats", p)
	}
}
