package deals

import (
	"math/rand"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRankBetween_ProducesAKeyInsideTheGap(t *testing.T) {
	cases := []struct{ prev, next string }{
		{"", ""},
		{"n", ""},
		{"", "n"},
		{"a", "b"},
		{"n", "o"},
		{"aaa", "aab"},
	}

	for _, c := range cases {
		got := rankBetween(c.prev, c.next)
		if c.prev != "" {
			assert.Greater(t, got, c.prev, "between(%q,%q) must sort after prev", c.prev, c.next)
		}
		if c.next != "" {
			assert.Less(t, got, c.next, "between(%q,%q) must sort before next", c.prev, c.next)
		}
	}
}

// Broken neighbours (hand-edited data, or a pair that has already been
// reordered) must still yield a usable key rather than hang looking for a gap
// that is not there.
func TestRankBetween_SurvivesInvertedNeighbours(t *testing.T) {
	got := rankBetween("z", "a")
	assert.Greater(t, got, "z")
}

// The whole point of a string key is that repeated inserts into the same gap
// never need the column renumbering. A thousand of them must stay ordered.
func TestRankBetween_StaysOrderedUnderRepeatedInserts(t *testing.T) {
	keys := []string{rankBetween("", "")}

	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 1000; i++ {
		at := rng.Intn(len(keys) + 1)

		prev, next := "", ""
		if at > 0 {
			prev = keys[at-1]
		}
		if at < len(keys) {
			next = keys[at]
		}

		key := rankBetween(prev, next)
		require.NotEqual(t, prev, key)
		require.NotEqual(t, next, key)

		keys = append(keys, "")
		copy(keys[at+1:], keys[at:])
		keys[at] = key
	}

	require.True(t, sort.SliceIsSorted(keys, func(i, j int) bool { return keys[i] < keys[j] }),
		"inserting into arbitrary gaps must never break the ordering")

	// Keys stay short enough to store: a scheme that doubled in length every
	// insert would outgrow the column.
	for _, key := range keys {
		assert.LessOrEqual(t, len(key), 64)
	}
}
