package dice

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"table/internal/errcode"
	"table/internal/rule"
)

func TestRegistered(t *testing.T) {
	got, ok := rule.Get("dice")
	if !ok || got.Code() != "dice" || got.MinSeats() != 2 || got.MaxSeats() != 8 {
		t.Fatalf("registry %+v %v", got, ok)
	}
}

func TestNormalHandSettlesZeroSum(t *testing.T) {
	g := Game{}
	seats := []int{1, 2, 3, 4}
	state, events, err := g.Start(rule.StartContext{Seats: seats})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = g.Apply(state, 2, rule.Action{Type: "Roll"}, time.Now())
	if !errors.Is(err, errcode.ErrNotYourTurn) {
		t.Fatalf("wrong seat: %v", err)
	}
	_, _, err = g.Apply(state, 1, rule.Action{Type: "Fold"}, time.Now())
	if !errors.Is(err, errcode.ErrBadAction) {
		t.Fatalf("bad action: %v", err)
	}
	_, _, err = g.Apply(state, 1, rule.Action{Type: "Roll", Payload: json.RawMessage(`{"n":1}`)}, time.Now())
	if !errors.Is(err, errcode.ErrBadPayload) {
		t.Fatalf("bad payload: %v", err)
	}

	for !state.Settled {
		var body data
		if err := rule.Decode(state.Data, &body); err != nil {
			t.Fatal(err)
		}
		var ev []rule.Event
		state, ev, err = g.Apply(state, body.Turn, rule.Action{Type: "Roll"}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, ev...)
		for _, item := range ev {
			if item.Type != "Rolled" {
				continue
			}
			payload, _ := item.Payload.(map[string]any)
			dice, _ := payload["Dice"].([]int)
			if len(dice) != 2 {
				t.Fatalf("default count %+v", payload)
			}
			sum := 0
			for _, pip := range dice {
				if pip < 1 || pip > 6 {
					t.Fatalf("die %d", pip)
				}
				sum += pip
			}
			if payload["Sum"] != sum {
				t.Fatalf("sum %+v", payload)
			}
		}
	}
	assertSettled(t, events, seats)
	view, err := g.View(state, 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(view.Private) != "{}" || view.Phase != phaseEnd || len(view.Legal) != 0 {
		t.Fatalf("view %+v %s", view, view.Private)
	}
	var pub struct {
		Turn  int
		Rolls []roll
	}
	if err := json.Unmarshal(view.Public, &pub); err != nil {
		t.Fatal(err)
	}
	if pub.Turn != 0 || len(pub.Rolls) != len(seats) {
		t.Fatalf("public %+v", pub)
	}
}

func TestTimeoutRollsCurrentSeat(t *testing.T) {
	g := Game{}
	seats := []int{2, 5, 8}
	state, events, err := g.Start(rule.StartContext{Seats: seats, Config: json.RawMessage(`{"Count":8}`)})
	if err != nil {
		t.Fatal(err)
	}
	var body data
	if err := rule.Decode(state.Data, &body); err != nil {
		t.Fatal(err)
	}
	if body.Count != 5 {
		t.Fatalf("clamp %d", body.Count)
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
	scores := settleScores(t, events)
	for _, row := range scores {
		if row.Seat == 0 {
			t.Fatal(scores)
		}
	}
}

func TestCountDefaultAndTies(t *testing.T) {
	n, err := parseCount(nil)
	if err != nil || n != 2 {
		t.Fatalf("default %d %v", n, err)
	}
	n, err = parseCount(json.RawMessage(`{"Count":1}`))
	if err != nil || n != 1 {
		t.Fatalf("one %d %v", n, err)
	}
	if _, err := parseCount(json.RawMessage(`{`)); !errors.Is(err, errcode.ErrBadPayload) {
		t.Fatalf("json %v", err)
	}
	tied := winnersOf([]roll{{Seat: 1, Sum: 4}, {Seat: 3, Sum: 9}, {Seat: 4, Sum: 9}})
	if len(tied) != 2 || tied[0] != 3 || tied[1] != 4 {
		t.Fatalf("tie %+v", tied)
	}
	all := winnersOf([]roll{{Seat: 1, Sum: 7}, {Seat: 2, Sum: 7}})
	deltas := rule.Balance([]int{1, 2}, all)
	if deltas[0].Delta != 0 || deltas[1].Delta != 0 {
		t.Fatalf("all tie %+v", deltas)
	}
}

func assertSettled(t *testing.T, events []rule.Event, seats []int) {
	t.Helper()
	scores := settleScores(t, events)
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
}

func settleScores(t *testing.T, events []rule.Event) []rule.Delta {
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
	return scores
}
