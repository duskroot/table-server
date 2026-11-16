package rule

import "table/internal/errcode"

// Card is a standard playing card. Rank 1 is Ace, 11 Jack, 12 Queen, 13 King.
// Suit 0 spade, 1 heart, 2 club, 3 diamond.
type Card struct {
	Rank int `json:"Rank"`
	Suit int `json:"Suit"`
}

func NewShuffledDeck() []Card {
	d := make([]Card, 0, 52)
	for suit := 0; suit < 4; suit++ {
		for rank := 1; rank <= 13; rank++ {
			d = append(d, Card{Rank: rank, Suit: suit})
		}
	}
	Shuffle(len(d), func(i, j int) { d[i], d[j] = d[j], d[i] })
	return d
}

// Take removes n cards from the front of the deck.
func Take(deck []Card, n int) ([]Card, []Card, error) {
	if n < 0 || len(deck) < n {
		return nil, deck, errcode.New("500002", "牌堆不足", "")
	}
	drawn := append([]Card(nil), deck[:n]...)
	rest := append([]Card(nil), deck[n:]...)
	return drawn, rest, nil
}
