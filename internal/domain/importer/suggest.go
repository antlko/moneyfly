package importer

import "strings"

// The distance threshold for proposing a name.
//
// docs/04-import-monefy.md §4.7 gives `Clouth` -> `Clothes` as the exemplar and
// calls it distance 2. It is actually 3 (clo|uth vs clo|thes: drop `u`, add `e`,
// add `s`); the spec's arithmetic was wrong and has been corrected there. Every
// other misspelling in the observed data is 1 or 2, so the rule is: two edits,
// and a third once the longer name reaches seven characters. Short names stay
// tight, because at four characters three edits is a different word.
const (
	maxEditDistance = 2
	longNameRunes   = 7
	longNameMaxEdit = 3
)

// allowedDistance is the threshold for one pair of names.
func allowedDistance(a, b string) int {
	longest := len([]rune(a))
	if n := len([]rune(b)); n > longest {
		longest = n
	}
	if longest >= longNameRunes {
		return longNameMaxEdit
	}
	return maxEditDistance
}

// candidate is a canonical name a source name could map onto.
type candidate struct {
	ID   int64
	Name string
}

// suggest proposes a target for an unmapped source name. It never applies
// anything: the mapping screen requires confirmation, and only confirmation
// creates the alias.
//
// Two tiers, in order:
//
//	high   — the same name ignoring case and surrounding whitespace
//	medium — edit distance <= 2
func suggest(sourceName string, candidates []candidate) *Suggestion {
	normalised := normalise(sourceName)
	if normalised == "" {
		return nil
	}
	for _, c := range candidates {
		if normalise(c.Name) == normalised {
			return &Suggestion{ID: c.ID, Name: c.Name, Confidence: "high"}
		}
	}

	best := -1
	bestDistance := 0
	for i, c := range candidates {
		target := normalise(c.Name)
		d := levenshtein(normalised, target)
		if d > allowedDistance(normalised, target) {
			continue
		}
		if best < 0 || d < bestDistance {
			best, bestDistance = i, d
		}
	}
	if best < 0 {
		return nil
	}
	return &Suggestion{ID: candidates[best].ID, Name: candidates[best].Name, Confidence: "medium"}
}

func normalise(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// levenshtein is the edit distance over runes, so a Cyrillic name is not
// measured in bytes.
func levenshtein(a, b string) int {
	ar, br := []rune(a), []rune(b)
	if len(ar) == 0 {
		return len(br)
	}
	if len(br) == 0 {
		return len(ar)
	}
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
