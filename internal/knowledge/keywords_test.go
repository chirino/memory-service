package knowledge

import (
	"strings"
	"testing"
)

func TestExtractKeywords_TwoClusters(t *testing.T) {
	texts := ClusterTexts{
		0: "Python Flask FastAPI migration SQLAlchemy Python async Python testing pytest",
		1: "Kubernetes deployment pod container Docker Helm autoscaler Kubernetes resources",
	}

	results := ExtractKeywords(texts, 5)
	if len(results) != 2 {
		t.Fatalf("expected 2 keyword results, got %d", len(results))
	}

	// Cluster 0 should have Python-related keywords.
	kw0 := KeywordStrings(results[0].Keywords)
	if !containsAny(kw0, "python", "flask", "fastapi") {
		t.Errorf("cluster 0 keywords %v should contain python-related terms", kw0)
	}

	// Cluster 1 should have Kubernetes-related keywords.
	kw1 := KeywordStrings(results[1].Keywords)
	if !containsAny(kw1, "kubernetes", "deployment", "docker", "helm") {
		t.Errorf("cluster 1 keywords %v should contain kubernetes-related terms", kw1)
	}
}

func TestExtractKeywords_DistinctiveTermsRankHigher(t *testing.T) {
	// "server" and "application" appear in both clusters — they should rank
	// lower than terms unique to each cluster.
	texts := ClusterTexts{
		0: "server application review quality standards review quality review practices quality",
		1: "server application deployment pipeline automation release deployment pipeline deployment",
	}

	results := ExtractKeywords(texts, 5)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// Cluster 0's top keyword should be "review" or "quality" (unique), not "server" or "application" (shared).
	kw0 := results[0].Keywords
	if len(kw0) > 0 && (kw0[0].Term == "server" || kw0[0].Term == "application") {
		t.Errorf("shared terms should not be top keyword in cluster 0, got %q", kw0[0].Term)
	}
	// Cluster 1's top keyword should be "deployment" or "pipeline" (unique).
	kw1 := results[1].Keywords
	if len(kw1) > 0 && (kw1[0].Term == "server" || kw1[0].Term == "application") {
		t.Errorf("shared terms should not be top keyword in cluster 1, got %q", kw1[0].Term)
	}
}

func TestExtractKeywords_SingleCluster(t *testing.T) {
	texts := ClusterTexts{
		0: "database PostgreSQL query optimization index performance",
	}

	results := ExtractKeywords(texts, 3)
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].Keywords) != 3 {
		t.Errorf("expected 3 keywords, got %d", len(results[0].Keywords))
	}
}

func TestExtractKeywords_Empty(t *testing.T) {
	results := ExtractKeywords(nil, 5)
	if results != nil {
		t.Errorf("expected nil for empty input, got %v", results)
	}
}

func TestExtractKeywords_ZeroTopN(t *testing.T) {
	texts := ClusterTexts{0: "hello world"}
	results := ExtractKeywords(texts, 0)
	if results != nil {
		t.Errorf("expected nil for topN=0, got %v", results)
	}
}

func TestGenerateLabel(t *testing.T) {
	keywords := []ScoredKeyword{
		{Term: "python", Score: 0.5},
		{Term: "flask", Score: 0.3},
		{Term: "fastapi", Score: 0.2},
		{Term: "migration", Score: 0.1},
	}

	label := GenerateLabel(keywords, 3)
	if label != "python, flask, fastapi" {
		t.Errorf("expected 'python, flask, fastapi', got '%s'", label)
	}
}

func TestGenerateLabel_FewerThanMax(t *testing.T) {
	keywords := []ScoredKeyword{
		{Term: "docker", Score: 0.5},
	}
	label := GenerateLabel(keywords, 5)
	if label != "docker" {
		t.Errorf("expected 'docker', got '%s'", label)
	}
}

func TestTokenize_FiltersStopWordsAndShortTokens(t *testing.T) {
	tokens := tokenize("The quick brown fox can not use a simple solution")
	for _, token := range tokens {
		if len(token) < 3 {
			t.Errorf("token %q should have been filtered (too short)", token)
		}
		if stopWords[token] {
			t.Errorf("token %q should have been filtered (stop word)", token)
		}
	}
	if !contains(tokens, "quick") || !contains(tokens, "brown") || !contains(tokens, "fox") {
		t.Errorf("expected content words to remain, got %v", tokens)
	}
}

func TestKeywordStrings(t *testing.T) {
	kws := []ScoredKeyword{
		{Term: "alpha", Score: 1.0},
		{Term: "beta", Score: 0.5},
	}
	strs := KeywordStrings(kws)
	if len(strs) != 2 || strs[0] != "alpha" || strs[1] != "beta" {
		t.Errorf("expected [alpha beta], got %v", strs)
	}
}

func containsAny(slice []string, targets ...string) bool {
	for _, s := range slice {
		for _, target := range targets {
			if strings.EqualFold(s, target) {
				return true
			}
		}
	}
	return false
}

func contains(slice []string, target string) bool {
	for _, s := range slice {
		if s == target {
			return true
		}
	}
	return false
}
