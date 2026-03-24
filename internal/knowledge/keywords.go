package knowledge

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// ClusterTexts maps a cluster label (or index) to the combined text of its members.
type ClusterTexts map[int]string

// KeywordResult holds the top keywords for a single cluster.
type KeywordResult struct {
	ClusterLabel int
	Keywords     []ScoredKeyword
}

// ScoredKeyword is a term with its c-TF-IDF score.
type ScoredKeyword struct {
	Term  string
	Score float64
}

// ExtractKeywords computes c-TF-IDF across all clusters and returns the top-N
// keywords per cluster. c-TF-IDF treats each cluster's combined text as a
// single document and identifies terms that are distinctively frequent in one
// cluster compared to all others.
//
// topN controls how many keywords are returned per cluster.
func ExtractKeywords(clusterTexts ClusterTexts, topN int) []KeywordResult {
	if len(clusterTexts) == 0 || topN <= 0 {
		return nil
	}

	// Tokenize each cluster's text.
	clusterTokens := make(map[int][]string, len(clusterTexts))
	for label, text := range clusterTexts {
		clusterTokens[label] = tokenize(text)
	}

	// Compute term frequency per cluster (normalized by cluster token count).
	clusterTF := make(map[int]map[string]termFreq, len(clusterTokens))
	for label, tokens := range clusterTokens {
		counts := make(map[string]int)
		for _, t := range tokens {
			counts[t]++
		}
		total := float64(len(tokens))
		if total == 0 {
			continue
		}
		tf := make(map[string]termFreq, len(counts))
		for term, count := range counts {
			tf[term] = termFreq{tf: float64(count) / total, count: count}
		}
		clusterTF[label] = tf
	}

	// Compute document frequency: in how many clusters does each term appear?
	df := make(map[string]int)
	for _, tf := range clusterTF {
		for term := range tf {
			df[term]++
		}
	}

	numClusters := float64(len(clusterTexts))

	// Compute c-TF-IDF score for each (cluster, term) pair.
	var results []KeywordResult
	labels := sortedIntKeys(clusterTF)
	for _, label := range labels {
		tf := clusterTF[label]
		scored := make([]ScoredKeyword, 0, len(tf))
		for term, entry := range tf {
			idf := math.Log(1 + numClusters/float64(df[term]))
			score := entry.tf * idf
			scored = append(scored, ScoredKeyword{Term: term, Score: score})
		}

		// Sort by score descending.
		sort.Slice(scored, func(i, j int) bool {
			if scored[i].Score != scored[j].Score {
				return scored[i].Score > scored[j].Score
			}
			return scored[i].Term < scored[j].Term
		})

		n := topN
		if n > len(scored) {
			n = len(scored)
		}

		results = append(results, KeywordResult{
			ClusterLabel: label,
			Keywords:     scored[:n],
		})
	}

	return results
}

// GenerateLabel creates a comma-separated label from the top keywords.
func GenerateLabel(keywords []ScoredKeyword, maxTerms int) string {
	n := maxTerms
	if n > len(keywords) {
		n = len(keywords)
	}
	terms := make([]string, n)
	for i := 0; i < n; i++ {
		terms[i] = keywords[i].Term
	}
	return strings.Join(terms, ", ")
}

// KeywordStrings extracts just the term strings from scored keywords.
func KeywordStrings(keywords []ScoredKeyword) []string {
	result := make([]string, len(keywords))
	for i, kw := range keywords {
		result[i] = kw.Term
	}
	return result
}

// tokenize splits text into lowercase tokens, filtering stop words and short tokens.
func tokenize(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	var tokens []string
	for _, w := range words {
		if len(w) < 3 {
			continue
		}
		if stopWords[w] {
			continue
		}
		tokens = append(tokens, w)
	}
	return tokens
}

func sortedIntKeys(m map[int]map[string]termFreq) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

type termFreq struct {
	tf    float64
	count int
}

// stopWords is a minimal English stop word set for filtering low-signal tokens.
var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "are": true, "but": true,
	"not": true, "you": true, "all": true, "can": true, "has": true,
	"her": true, "was": true, "one": true, "our": true, "out": true,
	"this": true, "that": true, "with": true, "have": true, "from": true,
	"they": true, "been": true, "said": true, "each": true, "will": true,
	"what": true, "when": true, "make": true, "like": true, "than": true,
	"them": true, "then": true, "into": true, "just": true, "your": true,
	"some": true, "could": true, "would": true, "there": true, "their": true,
	"about": true, "which": true, "these": true, "other": true, "should": true,
	"after": true, "being": true, "those": true, "where": true, "while": true,
	"does": true, "doing": true, "done": true, "here": true, "also": true,
	"very": true, "more": true, "most": true, "only": true, "over": true,
	"such": true, "well": true, "how": true, "use": true, "using": true,
	"used": true, "need": true, "want": true, "think": true,
}
