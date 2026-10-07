package eval

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

func pool(n int, repoOf func(i int) string) []Record {
	var out []Record
	for i := 0; i < n; i++ {
		live := &LiveOutcome{Outcome: []Outcome{OutcomeVerifiedSuccess, OutcomeFailure, OutcomeBlocked, OutcomeRescued}[i%4]}
		out = append(out, Record{ID: fmt.Sprintf("rec_%02d", i), RepoURL: repoOf(i), Category: categories[i%len(categories)], Live: live})
	}
	return out
}

func ids(rs []Record) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

func TestSampleDeterministic(t *testing.T) {
	p := pool(20, func(i int) string { return "/r/a" })
	a, err := Sample(p, 15, 42)
	if err != nil {
		t.Fatal(err)
	}
	shuffled := slices.Clone(p)
	slices.Reverse(shuffled) // input order must not matter
	b, _ := Sample(shuffled, 15, 42)
	if !slices.Equal(ids(a), ids(b)) {
		t.Errorf("same seed, different draws:\n%v\n%v", ids(a), ids(b))
	}
	c, _ := Sample(p, 15, 43)
	if slices.Equal(ids(a), ids(c)) {
		t.Error("different seeds gave the same draw")
	}
	if len(a) != 15 || len(slices.Compact(slices.Sorted(slices.Values(ids(a))))) != 15 {
		t.Errorf("draw has duplicates or wrong size: %v", ids(a))
	}
}

func TestSampleIgnoresLiveOutcome(t *testing.T) {
	p := pool(8, func(int) string { return "/r/a" })
	s, _ := Sample(p, 8, 1)
	seen := map[Outcome]bool{}
	for _, r := range s {
		seen[r.Live.Outcome] = true
	}
	for _, o := range []Outcome{OutcomeVerifiedSuccess, OutcomeFailure, OutcomeBlocked, OutcomeRescued} {
		if !seen[o] {
			t.Errorf("live outcome %s never drawn", o)
		}
	}
}

func TestSampleTooSmall(t *testing.T) {
	if _, err := Sample(pool(3, func(int) string { return "x" }), 4, 1); !errors.Is(err, ErrNotEnoughRecords) {
		t.Errorf("err = %v, want ErrNotEnoughRecords", err)
	}
}

func TestSampleMix(t *testing.T) {
	majority := SampleMix(pool(5, func(i int) string { return map[bool]string{true: "/r/a", false: "/r/b"}[i < 3] }))
	if majority.Repos["/r/a"] != 3 || majority.Repos["/r/b"] != 2 || !majority.SingleRepoMajority {
		t.Errorf("mix = %+v", majority)
	}
	if majority.Categories[string(categories[0])] != 1 {
		t.Errorf("categories = %v", majority.Categories)
	}
	half := SampleMix(pool(4, func(i int) string { return map[bool]string{true: "/r/a", false: "/r/b"}[i < 2] }))
	if half.SingleRepoMajority {
		t.Error("exactly half is not a majority")
	}
}
