// Package similar finds tickets about the same thing using TF-IDF, a classic
// way to compare texts by their words:
//
//   - TF: a word that appears often in a ticket describes it well.
//   - IDF: a word that appears in few tickets is more telling than one that
//     appears everywhere ("please", "hello").
//
// Each ticket becomes a list of weighted words, and two tickets are as
// similar as the angle between their lists is small (cosine similarity).
// It runs locally, needs no training and can explain itself with the words
// two tickets share.
package similar

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Doc is one ticket's text. Callers should pass masked text.
type Doc struct {
	ID      int64
	Subject string
	Body    string
}

// Match says how alike a candidate is to the target.
type Match struct {
	ID          int64    `json:"ticket_id"`
	Score       float64  `json:"score"`        // 0 to 1, higher is more alike
	SharedTerms []string `json:"shared_terms"` // the words that drove the match
}

const (
	maxSharedTerms = 5
	subjectWeight  = 2 // a subject says more than the same number of body words
)

// Rank returns up to k candidates most like target, best first, skipping
// scores below minScore and the target itself.
func Rank(target Doc, candidates []Doc, k int, minScore float64) []Match {
	docs := make([]Doc, 0, len(candidates)+1)
	docs = append(docs, target)
	for _, c := range candidates {
		if c.ID != target.ID {
			docs = append(docs, c)
		}
	}
	counts := make([]map[string]float64, len(docs))
	df := map[string]int{}
	for i, d := range docs {
		counts[i] = termCounts(d)
		for term := range counts[i] {
			df[term]++
		}
	}
	vecs := make([]map[string]float64, len(docs))
	for i := range docs {
		vecs[i] = weigh(counts[i], df, len(docs))
	}

	var out []Match
	for i := 1; i < len(docs); i++ {
		score, shared := cosine(vecs[0], vecs[i])
		if score >= minScore && score > 0 {
			out = append(out, Match{ID: docs[i].ID, Score: round3(score), SharedTerms: shared})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ID < out[j].ID // stable output for equal scores
	})
	if k > 0 && len(out) > k {
		out = out[:k]
	}
	return out
}

func termCounts(d Doc) map[string]float64 {
	counts := map[string]float64{}
	for _, w := range tokens(d.Subject) {
		counts[w] += subjectWeight
	}
	for _, w := range tokens(d.Body) {
		counts[w]++
	}
	return counts
}

// weigh builds the normalised TF-IDF vector for one document.
func weigh(counts map[string]float64, df map[string]int, n int) map[string]float64 {
	vec := make(map[string]float64, len(counts))
	var norm float64
	for term, tf := range counts {
		// Smoothed IDF stays positive even for words found in every ticket.
		idf := math.Log(float64(1+n)/float64(1+df[term])) + 1
		w := (1 + math.Log(tf)) * idf // log-scaled TF stops repeats dominating
		vec[term] = w
		norm += w * w
	}
	if norm == 0 {
		return vec
	}
	norm = math.Sqrt(norm)
	for term := range vec {
		vec[term] /= norm
	}
	return vec
}

// cosine multiplies matching words; each product is also how much that word
// contributed, which gives us the explanation.
func cosine(a, b map[string]float64) (float64, []string) {
	type contribution struct {
		term string
		w    float64
	}
	var total float64
	var parts []contribution
	for term, wa := range a {
		if wb, ok := b[term]; ok {
			total += wa * wb
			parts = append(parts, contribution{term, wa * wb})
		}
	}
	sort.Slice(parts, func(i, j int) bool {
		if parts[i].w != parts[j].w {
			return parts[i].w > parts[j].w
		}
		return parts[i].term < parts[j].term
	})
	var shared []string
	for i := 0; i < len(parts) && i < maxSharedTerms; i++ {
		shared = append(shared, parts[i].term)
	}
	return total, shared
}

// tokens lowercases, splits on anything that is not a letter or digit, and
// drops tiny words, pure numbers and stop words.
func tokens(s string) []string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := words[:0]
	for _, w := range words {
		if len([]rune(w)) < 3 || stopWords[w] || isNumber(w) {
			continue
		}
		out = append(out, w)
	}
	return out
}

func isNumber(w string) bool {
	for _, r := range w {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

var stopWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`the and for are but not you your yours with this that these those have has had was were been being
		from they them their there then than what when where which who whom why how can could would should will just
		our out any all its into about over also too very more most some such only own same here hello dear thanks thank
		please regards team support kindly let get got need want like com www http https`) {
		m[w] = true
	}
	return m
}()
