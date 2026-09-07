package main

import (
	"math/rand"
	"strings"
)

// participantTypes is the round-robin cycle of conversation participant types.
var participantTypes = []string{"single-user", "two-user", "two-agent"}

// ParticipantType returns the participant type for a given conversation index
// using a round-robin assignment.
func ParticipantType(index int) string {
	return participantTypes[index%len(participantTypes)]
}

// TurnPairCount returns a realistic number of turn pairs for a conversation
// based on a probability distribution. Each turn pair produces 2 stored entries
// (one USER/first-turn + one AI/second-turn), so the actual entry count is
// TurnPairCount() * 2:
//   - 60% short:  2–10 turn pairs  → 4–20 entries
//   - 30% medium: 11–100 turn pairs → 22–200 entries
//   - 10% long:   101–2000 turn pairs → 202–4000 entries
func TurnPairCount(r *rand.Rand) int {
	p := r.Float64()
	switch {
	case p < 0.60:
		return r.Intn(9) + 2 // [2, 10]
	case p < 0.90:
		return r.Intn(90) + 11 // [11, 100]
	default:
		return r.Intn(1900) + 101 // [101, 2000]
	}
}

// EntryText returns a random text string sampled from three length buckets:
//   - 70% short:  ~50 chars
//   - 20% medium: ~500 chars
//   - 10% long:   ~5000 chars
func EntryText(r *rand.Rand) string {
	p := r.Float64()
	switch {
	case p < 0.70:
		return randomText(r, 50)
	case p < 0.90:
		return randomText(r, 500)
	default:
		return randomText(r, 5000)
	}
}

// SeedForkChains returns the fixed number of fork chains to seed.
func SeedForkChains() int {
	return 10
}

// randomText generates a string of approximately n characters using a
// repeating word pattern so it looks like natural prose in API payloads.
func randomText(r *rand.Rand, n int) string {
	words := []string{
		"load", "test", "conversation", "entry", "memory", "service",
		"benchmark", "scale", "performance", "query", "pagination",
		"message", "context", "history", "agent", "user",
	}
	var sb strings.Builder
	for sb.Len() < n {
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(words[r.Intn(len(words))])
	}
	return sb.String()[:n]
}
