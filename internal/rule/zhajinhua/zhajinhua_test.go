package zhajinhua

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"table/internal/errcode"
	"table/internal/rule"
)

func cards(pairs ...int) []rule.Card {
	out := make([]rule.Card, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, rule.Card{Rank: pairs[i], Suit: pairs[i+1]})
	}
	return out
}

func TestFixedHands(t *testing.T) {
	trips := cards(2, 0, 2, 1, 2, 2)
	straightFlush := cards(12, 0, 13, 0, 1, 0)
	if cmpHands(trips, straightFlush) <= 0 {
		t.Fatal("豹子应大于顺金")
	}
	pair := cards(2, 0, 2, 1, 3, 2)
	high := cards(9, 0, 7, 1, 4, 2)
	if cmpHands(pair, high) <= 0 {
		t.Fatal("对子应大于散牌")
	}
	a23 := cards(1, 0, 2, 1, 3, 2)
	qka := cards(12, 1, 13, 2, 1, 3)
	if cmpHands(a23, qka) >= 0 {
		t.Fatal("A23 应小于 QKA")
	}
	a23Flush := cards(1, 0, 2, 0, 3, 0)
	qkaFlush := cards(12, 1, 13, 1, 1, 1)
	if cmpHands(a23Flush, qkaFlush) >= 0 {
		t.Fatal("顺金 A23 应小于 QKA")
	}
	if cmpHands(cards(1, 0, 5, 0, 9, 0), cards(1, 1, 5, 1, 9, 1)) != 0 {
		t.Fatal("花色相同牌点应完全相同")
	}
}

func TestApplyHidesCardsAndFoldSettles(t *testing.T) {
	g := game{}
	d := data{
		Seats: []int{1, 2}, Ante: 1, Base: 100, Pot: 2, Stake: 1, Turn: 1,
		Players: []seat{
			{Seat: 1, Chips: 99, Cards: cards(1, 0, 13, 1, 12, 2)},
			{Seat: 2, Chips: 99, Cards: cards(7, 0, 8, 1, 9, 2)},
		},
	}
	state := rule.State{Phase: "play", Data: rule.MustJSON(d), Deadline: rule.After(rule.TurnTimeout)}
	now := time.Now()

	if _, _, err := g.Apply(state, 2, rule.Action{Type: "Fold"}, now); err != errcode.ErrNotYourTurn {
		t.Fatalf("fold out of turn: %v", err)
	}
	if _, _, err := g.Apply(state, 1, rule.Action{Type: "Bet", Payload: json.RawMessage(`{}`)}, now); err != errcode.ErrBadPayload {
		t.Fatalf("empty bet: %v", err)
	}
	if _, _, err := g.Apply(state, 1, rule.Action{Type: "Bet", Payload: json.RawMessage(`{"Amount":0}`)}, now); err != errcode.ErrBadAction {
		t.Fatalf("zero bet: %v", err)
	}

	next, _, err := g.Apply(state, 2, rule.Action{Type: "Look"}, now)
	if err != nil {
		t.Fatal(err)
	}
	var mid data
	if err := rule.Decode(next.Data, &mid); err != nil {
		t.Fatal(err)
	}
	if mid.Turn != 1 || !mid.Players[1].Looked {
		t.Fatalf("look rotated turn: %+v", mid)
	}
	own, err := g.View(next, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(own.Public), "Rank") || strings.Contains(string(own.Private), "Rank") {
		t.Fatalf("seat 1 saw cards before look public=%s private=%s", own.Public, own.Private)
	}
	opponent, err := g.View(next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(opponent.Public), "Rank") {
		t.Fatalf("public leaked cards %s", opponent.Public)
	}
	if !containsRanks(opponent.Private, []int{7, 8, 9}) || containsRanks(opponent.Private, []int{1, 13, 12}) {
		t.Fatalf("seat 2 private %s", opponent.Private)
	}

	next, _, err = g.Apply(next, 1, rule.Action{Type: "Look"}, now)
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := g.View(next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(hidden.Public), "Rank") || containsRanks(hidden.Private, []int{1, 13, 12}) {
		t.Fatalf("seat 2 saw seat 1 cards public=%s private=%s", hidden.Public, hidden.Private)
	}
	shown, err := g.View(next, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !containsRanks(shown.Private, []int{1, 13, 12}) {
		t.Fatalf("seat 1 private %s", shown.Private)
	}

	settled, events, err := g.Apply(next, 1, rule.Action{Type: "Fold"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !settled.Settled || settled.Deadline != nil {
		t.Fatalf("settled=%v deadline=%v", settled.Settled, settled.Deadline)
	}
	assertScores(t, events, []int{1, 2}, map[int]int{1: -1, 2: 1})
	after, err := g.View(settled, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after.Public), "Rank") || containsRanks(after.Private, []int{1, 13, 12}) {
		t.Fatalf("cards leaked after settle public=%s private=%s", after.Public, after.Private)
	}
}

func TestCompareTieAndLookedCost(t *testing.T) {
	g := game{}
	d := data{
		Seats: []int{1, 2, 3}, Ante: 1, Base: 100, Pot: 3, Stake: 1, Turn: 1,
		Players: []seat{
			{Seat: 1, Chips: 99, Cards: cards(1, 0, 5, 0, 9, 0)},
			{Seat: 2, Chips: 99, Cards: cards(1, 1, 5, 1, 9, 1)},
			{Seat: 3, Chips: 99, Cards: cards(3, 0, 4, 1, 6, 2)},
		},
	}
	state := rule.State{Phase: "play", Data: rule.MustJSON(d), Deadline: rule.After(rule.TurnTimeout)}
	now := time.Now()
	state, _, err := g.Apply(state, 1, rule.Action{Type: "Look"}, now)
	if err != nil {
		t.Fatal(err)
	}
	state, events, err := g.Apply(state, 1, rule.Action{Type: "Compare", Payload: json.RawMessage(`{"TargetSeat":2}`)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if state.Settled || len(events) != 1 || events[0].Type == rule.EventSettle {
		t.Fatalf("compare should continue: settled=%v events=%+v", state.Settled, events)
	}
	var got data
	if err := rule.Decode(state.Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Players[0].Chips != 97 || got.Pot != 5 || !got.Players[0].Folded || got.Players[1].Folded || got.Turn != 2 {
		t.Fatalf("tie compare %+v", got)
	}
}

func TestTimeoutFoldsToOneWinner(t *testing.T) {
	g := game{}
	d := data{
		Seats: []int{1, 2, 3}, Ante: 1, Base: 100, Pot: 3, Stake: 1, Turn: 1,
		Players: []seat{
			{Seat: 1, Chips: 99, Cards: cards(2, 0, 3, 1, 4, 2)},
			{Seat: 2, Chips: 99, Cards: cards(5, 0, 6, 1, 7, 2)},
			{Seat: 3, Chips: 99, Cards: cards(8, 0, 9, 1, 10, 2)},
		},
	}
	state := rule.State{Phase: "play", Data: rule.MustJSON(d), Deadline: rule.After(rule.TurnTimeout)}
	now := time.Now()
	var err error
	state, _, err = g.OnTimeout(state, now)
	if err != nil {
		t.Fatal(err)
	}
	state, events, err := g.OnTimeout(state, now)
	if err != nil {
		t.Fatal(err)
	}
	assertScores(t, events, []int{1, 2, 3}, map[int]int{1: -1, 2: -1, 3: 2})
	if !state.Settled {
		t.Fatal("not settled")
	}
}

func containsRanks(raw json.RawMessage, ranks []int) bool {
	var body struct {
		Cards []rule.Card `json:"Cards"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return false
	}
	have := map[int]int{}
	for _, card := range body.Cards {
		have[card.Rank]++
	}
	for _, rank := range ranks {
		if have[rank] == 0 {
			return false
		}
		have[rank]--
	}
	return true
}

func assertScores(t *testing.T, events []rule.Event, seats []int, want map[int]int) {
	t.Helper()
	var settles int
	var body struct {
		Scores []rule.Delta `json:"Scores"`
	}
	for _, ev := range events {
		if ev.Type != rule.EventSettle {
			continue
		}
		settles++
		if ev.Scope != rule.ScopePublic {
			t.Fatalf("scope %d", ev.Scope)
		}
		raw, err := json.Marshal(ev.Payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
	}
	if settles != 1 {
		t.Fatalf("settle events %d", settles)
	}
	if len(body.Scores) != len(seats) {
		t.Fatalf("scores %+v", body.Scores)
	}
	got := map[int]int{}
	sum := 0
	for _, row := range body.Scores {
		got[row.Seat] = row.Delta
		sum += row.Delta
	}
	if sum != 0 {
		t.Fatalf("sum %d %+v", sum, body.Scores)
	}
	for _, seatNo := range seats {
		if _, ok := got[seatNo]; !ok {
			t.Fatalf("missing seat %d in %+v", seatNo, body.Scores)
		}
		if got[seatNo] != want[seatNo] {
			t.Fatalf("seat %d delta %d want %d", seatNo, got[seatNo], want[seatNo])
		}
	}
}
