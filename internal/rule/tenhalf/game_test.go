package tenhalf

import (
	"errors"
	"testing"
	"time"

	"table/internal/errcode"
	"table/internal/rule"
)

func TestRegistered(t *testing.T) {
	got, ok := rule.Get("tenhalf")
	if !ok || got.Code() != "tenhalf" || got.MinSeats() != 2 || got.MaxSeats() != 6 {
		t.Fatalf("registry %+v %v", got, ok)
	}
}

func TestNormalHandSettlesZeroSum(t *testing.T) {
	g := Game{}
	seats := []int{1, 2, 3}
	state, events, err := g.Start(rule.StartContext{Seats: seats})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = g.Apply(state, 1, rule.Action{Type: "Stand"}, time.Now())
	if !errors.Is(err, errcode.ErrNotYourTurn) {
		t.Fatalf("dealer early: %v", err)
	}
	_, _, err = g.Apply(state, 3, rule.Action{Type: "Hit"}, time.Now())
	if !errors.Is(err, errcode.ErrNotYourTurn) {
		t.Fatalf("wrong seat: %v", err)
	}
	_, _, err = g.Apply(state, 2, rule.Action{Type: "Fold"}, time.Now())
	if !errors.Is(err, errcode.ErrBadAction) {
		t.Fatalf("bad action: %v", err)
	}

	for state.Phase == phasePlay {
		var body data
		if err := rule.Decode(state.Data, &body); err != nil {
			t.Fatal(err)
		}
		var ev []rule.Event
		state, ev, err = g.Apply(state, body.Turn, rule.Action{Type: "Stand"}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, ev...)
	}
	if state.Phase != phaseDealer || state.Settled {
		t.Fatalf("phase %s settled %v", state.Phase, state.Settled)
	}
	var ev []rule.Event
	state, ev, err = g.OnTimeout(state, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	events = append(events, ev...)
	if !state.Settled || state.Phase != phaseEnd {
		t.Fatalf("end %+v", state.Phase)
	}
	assertSettled(t, events, seats)
	view, err := g.View(state, 2)
	if err != nil {
		t.Fatal(err)
	}
	if string(view.Private) != "{}" || len(view.Legal) != 0 {
		t.Fatalf("view %+v", view)
	}
}

func TestBustLoses(t *testing.T) {
	g := Game{}
	seats := []int{1, 2}
	state, _, err := g.Start(rule.StartContext{Seats: seats})
	if err != nil {
		t.Fatal(err)
	}
	for state.Phase == phasePlay {
		var body data
		if err := rule.Decode(state.Data, &body); err != nil {
			t.Fatal(err)
		}
		state, _, err = g.Apply(state, body.Turn, rule.Action{Type: "Hit"}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	var before data
	if err := rule.Decode(state.Data, &before); err != nil {
		t.Fatal(err)
	}
	player := before.hand(2)
	if player == nil || !player.Bust || player.Sum <= 21 {
		t.Fatalf("expected bust %+v", player)
	}
	state, events, err := g.OnTimeout(state, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !state.Settled {
		t.Fatal("dealer timeout did not settle")
	}
	scores := assertSettled(t, events, seats)
	bySeat := map[int]int{}
	for _, row := range scores {
		bySeat[row.Seat] = row.Delta
	}
	if bySeat[2] >= 0 || bySeat[1] != -bySeat[2] {
		t.Fatalf("bust scores %+v", scores)
	}
}

func TestPushIsNotALoser(t *testing.T) {
	d := data{
		Seats:  []int{1, 2, 3, 4},
		Dealer: 1,
		Hands: []hand{
			{Seat: 1, Sum: 16},
			{Seat: 2, Sum: 16},
			{Seat: 3, Sum: 20},
			{Seat: 4, Sum: 8},
		},
	}
	scores := settleScores(d)
	bySeat := map[int]int{}
	sum := 0
	for _, row := range scores {
		bySeat[row.Seat] = row.Delta
		sum += row.Delta
	}
	if sum != 0 || bySeat[2] != 0 || bySeat[3] != 1 || bySeat[4] != -1 || bySeat[1] != 0 {
		t.Fatalf("mixed %+v", scores)
	}

	bust := data{
		Seats:  []int{1, 2},
		Dealer: 1,
		Hands: []hand{
			{Seat: 1, Sum: 24, Bust: true},
			{Seat: 2, Sum: 30, Bust: true},
		},
	}
	scores = settleScores(bust)
	if scores[0].Seat != 1 || scores[0].Delta != 1 || scores[1].Seat != 2 || scores[1].Delta != -1 {
		t.Fatalf("both bust %+v", scores)
	}
}

func TestTimeoutStands(t *testing.T) {
	g := Game{}
	state, _, err := g.Start(rule.StartContext{Seats: []int{1, 2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	next, events, err := g.OnTimeout(state, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if next.Phase != phasePlay || len(events) != 1 || events[0].Type != "Stand" {
		t.Fatalf("timeout %+v %+v", next.Phase, events)
	}
	var body data
	if err := rule.Decode(next.Data, &body); err != nil {
		t.Fatal(err)
	}
	if body.Turn != 3 || !body.hand(2).Done {
		t.Fatalf("advanced %+v", body)
	}
}

func TestPip(t *testing.T) {
	if pip(rule.Card{Rank: 1}) != 2 || pip(rule.Card{Rank: 10}) != 20 || pip(rule.Card{Rank: 13}) != 1 || pip(rule.Card{Rank: 2}) != 4 {
		t.Fatal("pip")
	}
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
