package router

import "time"

// RouteClassKey identifies a (route, task_class) pair for routing statistics.
type RouteClassKey struct {
	RouteID   string
	TaskClass string
}

// RoutingStats holds Beta-prior success statistics for one (route, task_class) pair (FR-003).
type RoutingStats struct {
	RouteID   string
	TaskClass string
	Alpha     int // successes + 1
	Beta      int // failures + 1
	Samples   int // total observations
	UpdatedAt time.Time
}

// PSuccess returns the Beta-prior estimate of P(verified success).
// Before 10 samples the prior dominates (starts at alpha=1, beta=1 → P=0.5).
func (s RoutingStats) PSuccess() float64 {
	total := s.Alpha + s.Beta
	if total == 0 {
		return 0.5
	}
	return float64(s.Alpha) / float64(total)
}

// Update returns a new RoutingStats with the given outcome recorded (FR-011).
func (s RoutingStats) Update(success bool, now time.Time) RoutingStats {
	s.Samples++
	if success {
		s.Alpha++
	} else {
		s.Beta++
	}
	s.UpdatedAt = now
	return s
}
