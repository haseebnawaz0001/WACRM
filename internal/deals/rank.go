package deals

// Board ordering (plan 07).
//
// Dropping a card between two others has to be cheap. Integer positions force
// a renumber of every card below the drop, which is a write per card and a
// race between two people dragging at once. A sortable string only ever needs
// the one row that moved: the new key is chosen to fall between its
// neighbours, and the neighbours are not touched.

const (
	rankFirst byte = 'a' // one below every usable character
	rankLast  byte = 'z'
)

// rankBetween returns a key that sorts strictly after prev and strictly before
// next. An empty prev means "start of the column"; an empty next means "end".
func rankBetween(prev, next string) string {
	// Equal or inverted neighbours mean the column's keys are already broken
	// (hand-written data, or a rebalance that never ran). Appending after prev
	// still produces a valid key rather than looping forever looking for a gap
	// that does not exist.
	if next != "" && prev >= next {
		next = ""
	}

	var out []byte
	// Once prev is known to be below next at some position, every deeper
	// position is free: anything after prev's remainder is still before next.
	diverged := false

	for i := 0; ; i++ {
		p := rankFirst
		if i < len(prev) {
			p = prev[i]
		}
		n := rankLast + 1
		if !diverged && i < len(next) {
			n = next[i]
		}

		if p+1 < n {
			return string(append(out, p+(n-p)/2))
		}
		out = append(out, p)
		if p < n {
			diverged = true
		}
	}
}
