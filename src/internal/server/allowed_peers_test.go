package server

import (
	"math"
	"testing"
)

func TestIntersectAllowedPeersPreservesOrder(t *testing.T) {
	candidates := []string{"peer-A", "peer-B", "peer-C", "peer-D"}
	allowed := "peer-C,peer-A"
	result := intersectAllowedPeers(candidates, allowed)
	// Order follows candidates (priority-sorted), not the header.
	want := []string{"peer-A", "peer-C"}
	if len(result) != len(want) {
		t.Fatalf("got %v, want %v", result, want)
	}
	for i, p := range result {
		if p != want[i] {
			t.Fatalf("result[%d] = %q, want %q", i, p, want[i])
		}
	}
}

func TestIntersectAllowedPeersEmptyHeader(t *testing.T) {
	candidates := []string{"peer-A", "peer-B"}
	if got := intersectAllowedPeers(candidates, ""); len(got) != len(candidates) {
		t.Fatalf("empty header should be a no-op, got %v", got)
	}
}

func TestIntersectAllowedPeersNoMatch(t *testing.T) {
	candidates := []string{"peer-A", "peer-B"}
	allowed := "peer-X,peer-Y"
	if got := intersectAllowedPeers(candidates, allowed); len(got) != 0 {
		t.Fatalf("no match should return empty, got %v", got)
	}
}

func TestIntersectAllowedPeersWhitespace(t *testing.T) {
	candidates := []string{"peer-A", "peer-B"}
	allowed := " peer-A , peer-B "
	result := intersectAllowedPeers(candidates, allowed)
	if len(result) != 2 {
		t.Fatalf("whitespace should be trimmed, got %v", result)
	}
}

func TestIntersectAllowedPeersEmptyCandidates(t *testing.T) {
	if got := intersectAllowedPeers(nil, "peer-A"); len(got) != 0 {
		t.Fatalf("nil candidates should return empty, got %v", got)
	}
}

// --- Price-aware routing (routing.price_weight_decay) ---

func TestParseAllowedPeerOrder(t *testing.T) {
	order := parseAllowedPeerOrder("peer-C, peer-A ,,peer-A, peer-B")
	want := []string{"peer-C", "peer-A", "peer-B"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order[%d] = %q, want %q (header order must be preserved)", i, order[i], want[i])
		}
	}
}

func TestParseAllowedPeerOrderEmpty(t *testing.T) {
	if got := parseAllowedPeerOrder(" , ,"); len(got) != 0 {
		t.Fatalf("blank header should yield empty order, got %v", got)
	}
}

func TestPriceAwareScoresDecayByRank(t *testing.T) {
	// Gate stamps cheapest-first: peer-C is rank 0 (cheapest).
	order := []string{"peer-C", "peer-B", "peer-A"}
	scores := priceAwareScores([]string{"peer-A", "peer-B", "peer-C"}, order, 0.5)
	byID := map[string]float64{}
	for _, wc := range scores {
		byID[wc.peerID] = wc.score
	}
	if byID["peer-C"] != 1.0 {
		t.Fatalf("cheapest peer score = %v, want 1.0", byID["peer-C"])
	}
	if byID["peer-B"] != 0.5 {
		t.Fatalf("second peer score = %v, want 0.5", byID["peer-B"])
	}
	if byID["peer-A"] != 0.25 {
		t.Fatalf("third peer score = %v, want 0.25", byID["peer-A"])
	}
}

func TestPriceAwareScoresUnknownPeerGetsTailWeight(t *testing.T) {
	order := []string{"peer-C"}
	scores := priceAwareScores([]string{"peer-C", "peer-Z"}, order, 0.5)
	byID := map[string]float64{}
	for _, wc := range scores {
		byID[wc.peerID] = wc.score
	}
	if byID["peer-C"] != 1.0 {
		t.Fatalf("ranked peer score = %v, want 1.0", byID["peer-C"])
	}
	if byID["peer-Z"] != math.Pow(0.5, 2) {
		t.Fatalf("unranked peer score = %v, want %v", byID["peer-Z"], math.Pow(0.5, 2))
	}
}

func TestPriceAwareScoresEmpty(t *testing.T) {
	if got := priceAwareScores(nil, []string{"peer-C"}, 0.5); got != nil {
		t.Fatalf("nil ids should return nil, got %v", got)
	}
}

// TestPriceAwareSelectionFavorsCheapest verifies the end-to-end selection
// bias: with decay 0.5 the cheapest affordable peer should win roughly 4x
// as often as the most expensive one, and selection must never leave the
// candidate set.
func TestPriceAwareSelectionFavorsCheapest(t *testing.T) {
	order := []string{"peer-cheap", "peer-mid", "peer-pricey"}
	ids := []string{"peer-pricey", "peer-mid", "peer-cheap"} // deliberately unsorted
	weighted := priceAwareScores(ids, order, 0.5)

	picks := map[string]int{}
	for i := 0; i < 20000; i++ {
		p := weightedRandomSelect(weighted)
		if p != "peer-cheap" && p != "peer-mid" && p != "peer-pricey" {
			t.Fatalf("picked peer outside candidate set: %q", p)
		}
		picks[p]++
	}
	if picks["peer-cheap"] <= picks["peer-pricey"] {
		t.Fatalf("cheapest peer should dominate: cheap=%d pricey=%d", picks["peer-cheap"], picks["peer-pricey"])
	}
	// Weights 1, 0.5, 0.25 => expected shares ~57%, ~29%, ~14%.
	if picks["peer-cheap"] < 10000 {
		t.Fatalf("cheapest peer share too low: %d/20000", picks["peer-cheap"])
	}
}

func TestOrderCandidatesByPriceCheapestFirst(t *testing.T) {
	// Gate order: peer-C cheapest, then peer-B, then peer-A.
	order := []string{"peer-C", "peer-B", "peer-A"}
	got := orderCandidatesByPrice([]string{"peer-A", "peer-C", "peer-B"}, order)
	want := []string{"peer-C", "peer-B", "peer-A"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestOrderCandidatesByPriceStableForUnknownPeers(t *testing.T) {
	// Peers missing from the gate order keep their relative order at the tail.
	order := []string{"peer-C", "peer-B"}
	got := orderCandidatesByPrice([]string{"peer-Z", "peer-C", "peer-Y", "peer-B"}, order)
	want := []string{"peer-C", "peer-B", "peer-Z", "peer-Y"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (stable tail expected)", got, want)
		}
	}
}

// TestPriceAwareRetryNextCheapest simulates the retry loop semantics: after
// the cheapest peer fails and is excluded, the price-ordered candidate list
// surfaces the next-cheapest peer at index 0.
func TestPriceAwareRetryNextCheapest(t *testing.T) {
	order := []string{"peer-cheap", "peer-mid", "peer-pricey"}
	candidates := orderCandidatesByPrice([]string{"peer-pricey", "peer-cheap", "peer-mid"}, order)
	if candidates[0] != "peer-cheap" {
		t.Fatalf("first pick candidate = %q, want peer-cheap", candidates[0])
	}
	// excludePeers preserves order; simulate excluding the failed cheapest.
	excluded := map[string]bool{"peer-cheap": true}
	remaining := excludePeers(candidates, excluded)
	if len(remaining) != 2 || remaining[0] != "peer-mid" {
		t.Fatalf("retry candidate = %v, want [peer-mid peer-pricey]", remaining)
	}
}
