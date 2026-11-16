package douniu

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

func TestFixedNiu(t *testing.T) {
	niuNiu := cards(11, 0, 11, 1, 11, 2, 5, 0, 5, 1)
	if niuPoint(niuNiu) != 10 {
		t.Fatalf("J,J,J,5,5 应为牛牛，得到 %d", niuPoint(niuNiu))
	}
	none := cards(1, 0, 1, 1, 1, 2, 2, 0, 2, 1)
	if niuPoint(none) != 0 {
		t.Fatalf("A,A,A,2,2 应为没牛，得到 %d", niuPoint(none))
	}
	if niuPoint(niuNiu) <= niuPoint(none) {
		t.Fatal("牛牛应大于没牛")
	}
}

func TestGrabSettleSumAndHiddenBanker(t *testing.T) {
	g := game{}
	bankerCards := cards(11, 0, 11, 1, 11, 2, 5, 0, 5, 1)
	idleCards := cards(1, 0, 1, 1, 1, 2, 2, 0, 2, 1)
	d := data{
		Seats: []int{1, 2},
		Players: []player{
			{Seat: 1, Cards: bankerCards},
			{Seat: 2, Cards: idleCards},
		},
	}
	state := rule.State{Phase: "grab", Data: rule.MustJSON(d), Deadline: rule.After(rule.TurnTimeout)}
	now := time.Now()

	if _, _, err := g.Apply(state, 9, rule.Action{Type: "Grab", Payload: json.RawMessage(`{"Multiple":1}`)}, now); err != errcode.ErrNotYourTurn {
		t.Fatalf("unknown seat: %v", err)
	}
	if _, _, err := g.Apply(state, 1, rule.Action{Type: "Grab", Payload: json.RawMessage(`{}`)}, now); err != errcode.ErrBadPayload {
		t.Fatalf("empty grab: %v", err)
	}
	if _, _, err := g.Apply(state, 1, rule.Action{Type: "Grab", Payload: json.RawMessage(`{"Multiple":4}`)}, now); err != errcode.ErrBadAction {
		t.Fatalf("multiple 4: %v", err)
	}

	next, events, err := g.Apply(state, 1, rule.Action{Type: "Grab", Payload: json.RawMessage(`{"Multiple":2}`)}, now)
	if err != nil {
		t.Fatal(err)
	}
	if next.Settled || len(events) != 1 || events[0].Type == rule.EventSettle {
		t.Fatalf("first grab should wait: %+v", events)
	}
	idle, err := g.View(next, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(idle.Public), "Rank") || containsRank(idle.Private, 11) {
		t.Fatalf("idle saw banker cards public=%s private=%s", idle.Public, idle.Private)
	}
	if !containsRank(idle.Private, 1) || !containsRank(idle.Private, 2) {
		t.Fatalf("idle private %s", idle.Private)
	}
	var priv struct {
		Cards []rule.Card `json:"Cards"`
	}
	if err := json.Unmarshal(idle.Private, &priv); err != nil {
		t.Fatal(err)
	}
	if len(priv.Cards) != 5 {
		t.Fatalf("private cards %d", len(priv.Cards))
	}

	settled, events, err := g.Apply(next, 2, rule.Action{Type: "Grab", Payload: json.RawMessage(`{"Multiple":0}`)}, now)
	if err != nil {
		t.Fatal(err)
	}
	assertScores(t, events, []int{1, 2}, map[int]int{1: 2, 2: -2})
	if !settled.Settled || settled.Deadline != nil {
		t.Fatalf("settled=%v deadline=%v", settled.Settled, settled.Deadline)
	}
	after, err := g.View(settled, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after.Public), "Rank") || containsRank(after.Private, 11) {
		t.Fatalf("idle view leaked banker cards public=%s private=%s", after.Public, after.Private)
	}
	var opened struct {
		Hands []openHand `json:"Hands"`
	}
	raw, err := json.Marshal(events[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &opened); err != nil {
		t.Fatal(err)
	}
	if len(opened.Hands) != 2 || opened.Hands[0].Niu != 10 || opened.Hands[1].Niu != 0 {
		t.Fatalf("hands %+v", opened.Hands)
	}
}

func TestTimeoutGrabZeroSum(t *testing.T) {
	g := game{}
	d := data{
		Seats: []int{2, 1},
		Players: []player{
			{Seat: 2, Cards: cards(11, 0, 11, 1, 11, 2, 5, 0, 5, 1), Grabbed: true, Multiple: 0},
			{Seat: 1, Cards: cards(1, 0, 1, 1, 1, 2, 2, 3, 3, 0)},
		},
	}
	state := rule.State{Phase: "grab", Data: rule.MustJSON(d), Deadline: rule.After(rule.TurnTimeout)}
	settled, events, err := g.OnTimeout(state, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	assertScores(t, events, []int{2, 1}, map[int]int{2: 1, 1: -1})
	var got data
	if err := rule.Decode(settled.Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Banker != 1 || got.BankMultiple != 1 {
		t.Fatalf("banker=%d multiple=%d", got.Banker, got.BankMultiple)
	}
	idle, err := g.View(settled, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(idle.Public), "Rank") || containsRank(idle.Private, 3) {
		t.Fatalf("idle saw banker cards public=%s private=%s", idle.Public, idle.Private)
	}
}

func containsRank(raw json.RawMessage, rank int) bool {
	var body struct {
		Cards []rule.Card `json:"Cards"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return false
	}
	for _, card := range body.Cards {
		if card.Rank == rank {
			return true
		}
	}
	return false
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
	got := map[int]int{}
	sum := 0
	for _, row := range body.Scores {
		if _, ok := got[row.Seat]; ok {
			t.Fatalf("duplicate seat %d", row.Seat)
		}
		got[row.Seat] = row.Delta
		sum += row.Delta
	}
	if sum != 0 {
		t.Fatalf("sum %d %+v", sum, body.Scores)
	}
	if len(got) != len(seats) {
		t.Fatalf("scores %+v", body.Scores)
	}
	for _, seatNo := range seats {
		if _, ok := got[seatNo]; !ok {
			t.Fatalf("missing %d", seatNo)
		}
		if got[seatNo] != want[seatNo] {
			t.Fatalf("seat %d delta %d want %d (%+v)", seatNo, got[seatNo], want[seatNo], body.Scores)
		}
	}
}
