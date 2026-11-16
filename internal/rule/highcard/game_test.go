package highcard

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"table/internal/errcode"
	"table/internal/rule"
)

func TestRegistered(t *testing.T) {
	got, ok := rule.Get("highcard")
	if !ok || got.Code() != "highcard" || got.MinSeats() != 2 || got.MaxSeats() != 8 {
		t.Fatalf("registry %+v %v", got, ok)
	}
}

func TestNormalHandSettlesZeroSum(t *testing.T) {
	g := Game{}
	seats := []int{1, 3, 5, 7}
	state, events, err := g.Start(rule.StartContext{Seats: seats})
	if err != nil {
		t.Fatal(err)
	}
	if err := assertNoDeck(t, g, state, 0); err != nil {
		t.Fatal(err)
	}
	_, _, err = g.Apply(state, 3, rule.Action{Type: "Draw"}, time.Now())
	if !errors.Is(err, errcode.ErrNotYourTurn) {
		t.Fatalf("wrong seat: %v", err)
	}
	_, _, err = g.Apply(state, 1, rule.Action{Type: "Roll"}, time.Now())
	if !errors.Is(err, errcode.ErrBadAction) {
		t.Fatalf("bad action: %v", err)
	}

	for !state.Settled {
		var body data
		if err := rule.Decode(state.Data, &body); err != nil {
			t.Fatal(err)
		}
		var ev []rule.Event
		state, ev, err = g.Apply(state, body.Turn, rule.Action{Type: "Draw"}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, ev...)
		if err := assertNoDeck(t, g, state, len(body.Drawn)+1); err != nil {
			t.Fatal(err)
		}
	}
	scores := assertSettled(t, events, seats)
	var body data
	if err := rule.Decode(state.Data, &body); err != nil {
		t.Fatal(err)
	}
	want := expectWinner(body.Drawn)
	for _, row := range scores {
		if row.Seat == want && row.Delta <= 0 {
			t.Fatalf("winner %d %+v", want, scores)
		}
		if row.Seat != want && row.Delta >= 0 {
			t.Fatalf("loser %d %+v", row.Seat, scores)
		}
	}
}

func TestTimeoutDraws(t *testing.T) {
	g := Game{}
	seats := []int{4, 6}
	state, events, err := g.Start(rule.StartContext{Seats: seats})
	if err != nil {
		t.Fatal(err)
	}
	for !state.Settled {
		var ev []rule.Event
		state, ev, err = g.OnTimeout(state, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, ev...)
	}
	assertSettled(t, events, seats)
	if err := assertNoDeck(t, g, state, len(seats)); err != nil {
		t.Fatal(err)
	}
}

func TestAceBeatsKingAndSuitBreaksTie(t *testing.T) {
	ace := rule.Card{Rank: 1, Suit: 0}
	king := rule.Card{Rank: 13, Suit: 3}
	if !beats(ace, king) || beats(king, ace) {
		t.Fatal("ace")
	}
	low := rule.Card{Rank: 10, Suit: 0}
	high := rule.Card{Rank: 10, Suit: 3}
	if !beats(high, low) || beats(low, high) {
		t.Fatal("suit")
	}
	seat := winnerSeat([]drawn{{Seat: 2, Card: king}, {Seat: 8, Card: ace}, {Seat: 4, Card: high}})
	if seat != 8 {
		t.Fatalf("winner %d", seat)
	}
}

func assertNoDeck(t *testing.T, g Game, state rule.State, drawn int) error {
	t.Helper()
	view, err := g.View(state, 1)
	if err != nil {
		return err
	}
	if string(view.Private) != "{}" {
		t.Fatalf("private %s", view.Private)
	}
	raw := string(view.Public)
	if strings.Contains(raw, `"Deck"`) {
		t.Fatalf("deck field %s", raw)
	}
	var parsed any
	if err := json.Unmarshal(view.Public, &parsed); err != nil {
		return err
	}
	if got := countCards(parsed); got != drawn {
		t.Fatalf("public cards %d want %d in %s", got, drawn, raw)
	}
	var secret struct {
		Deck []rule.Card `json:"Deck"`
	}
	if err := json.Unmarshal(state.Data, &secret); err != nil {
		return err
	}
	if len(secret.Deck) == 0 {
		t.Fatal("state lost the remaining deck")
	}
	return nil
}

func countCards(v any) int {
	switch node := v.(type) {
	case map[string]any:
		if _, ok := node["Rank"]; ok {
			if _, ok := node["Suit"]; ok {
				return 1
			}
		}
		n := 0
		for _, child := range node {
			n += countCards(child)
		}
		return n
	case []any:
		n := 0
		for _, child := range node {
			n += countCards(child)
		}
		return n
	default:
		return 0
	}
}

func expectWinner(cards []drawn) int {
	best := cards[0]
	for _, item := range cards[1:] {
		rankA, rankB := best.Card.Rank, item.Card.Rank
		if rankA == 1 {
			rankA = 14
		}
		if rankB == 1 {
			rankB = 14
		}
		if rankB > rankA || (rankB == rankA && item.Card.Suit > best.Card.Suit) {
			best = item
		}
	}
	return best.Seat
}

func assertSettled(t *testing.T, events []rule.Event, seats []int) []rule.Delta {
	t.Helper()
	var got []rule.Event
	for _, ev := range events {
		if ev.Type == rule.EventSettle {
			got = append(got, ev)
		}
	}
	if len(got) != 1 || got[0].Scope != rule.ScopePublic {
		t.Fatalf("settle %+v", got)
	}
	body, ok := got[0].Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload %#v", got[0].Payload)
	}
	scores, ok := body["Scores"].([]rule.Delta)
	if !ok {
		t.Fatalf("scores %#v", body["Scores"])
	}
	if len(scores) != len(seats) {
		t.Fatalf("scores %+v", scores)
	}
	sum := 0
	seen := map[int]bool{}
	for _, row := range scores {
		sum += row.Delta
		seen[row.Seat] = true
	}
	if sum != 0 {
		t.Fatalf("sum %d %+v", sum, scores)
	}
	for _, seat := range seats {
		if !seen[seat] {
			t.Fatalf("missing %d %+v", seat, scores)
		}
	}
	return scores
}
