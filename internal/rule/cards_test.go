package rule

import "testing"

func TestDeckIs52UniqueCards(t *testing.T) {
	deck := NewShuffledDeck()
	if len(deck) != 52 {
		t.Fatalf("len %d", len(deck))
	}
	seen := map[Card]bool{}
	for _, card := range deck {
		if card.Rank < 1 || card.Rank > 13 || card.Suit < 0 || card.Suit > 3 {
			t.Fatalf("card %+v", card)
		}
		seen[card] = true
	}
	if len(seen) != 52 {
		t.Fatalf("unique %d", len(seen))
	}
	drawn, rest, err := Take(deck, 3)
	if err != nil || len(drawn) != 3 || len(rest) != 49 {
		t.Fatalf("take %v %d %d", err, len(drawn), len(rest))
	}
}

func TestBalanceSumsToZero(t *testing.T) {
	got := Balance([]int{1, 2, 3, 4}, []int{2, 4})
	sum := 0
	for _, row := range got {
		sum += row.Delta
	}
	if sum != 0 {
		t.Fatalf("sum %d %+v", sum, got)
	}
	draw := Balance([]int{1, 2}, nil)
	if draw[0].Delta != 0 || draw[1].Delta != 0 {
		t.Fatalf("draw %+v", draw)
	}
}
