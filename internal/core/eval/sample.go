package eval

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
)

// ErrNotEnoughRecords means there are fewer replayable records than a replay needs.
var ErrNotEnoughRecords = errors.New("not enough replayable records")

// Sample draws n records with a seeded shuffle (spec FR-012). The draw ignores live
// outcomes, and doesn't depend on the order of pool.
func Sample(pool []Record, n int, seed int64) ([]Record, error) {
	if n > len(pool) {
		return nil, fmt.Errorf("%w: have %d, need %d", ErrNotEnoughRecords, len(pool), n)
	}
	sorted := slices.Clone(pool)
	slices.SortFunc(sorted, func(a, b Record) int { return strings.Compare(a.ID, b.ID) })
	r := rand.New(rand.NewPCG(uint64(seed), 0x9E3779B97F4A7C15))
	r.Shuffle(len(sorted), func(i, j int) { sorted[i], sorted[j] = sorted[j], sorted[i] })
	return sorted[:n], nil
}

// Mix is a sample's composition, reported so a single-codebase result is visible.
type Mix struct {
	Repos              map[string]int `json:"repos"`
	Categories         map[string]int `json:"categories"`
	SingleRepoMajority bool           `json:"single_repo_majority"`
}

// SampleMix counts repositories and categories; one repository holding more than half
// the sample is flagged (FR-012).
func SampleMix(sample []Record) Mix {
	m := Mix{Repos: map[string]int{}, Categories: map[string]int{}}
	for _, r := range sample {
		m.Repos[r.RepoURL]++
		m.Categories[string(r.Category)]++
	}
	for _, n := range m.Repos {
		if 2*n > len(sample) {
			m.SingleRepoMajority = true
		}
	}
	return m
}
