// Package dice is the high-sum dice game. Seats roll in order; the highest sum wins.
package dice

import (
	"encoding/json"
	"time"

	"table/internal/errcode"
	"table/internal/rule"
)

func init() {
	rule.Register(Game{})
}

// Game is the dice rule.
type Game struct{}

func (Game) Code() string  { return "dice" }
func (Game) MinSeats() int { return 2 }
func (Game) MaxSeats() int { return 8 }

const (
	phaseRoll = "roll"
	phaseEnd  = "end"
)

type roll struct {
	Seat int   `json:"Seat"`
	Dice []int `json:"Dice"`
	Sum  int   `json:"Sum"`
}

type data struct {
	Seats []int  `json:"Seats"`
	Count int    `json:"Count"`
	Turn  int    `json:"Turn"`
	Rolls []roll `json:"Rolls"`
}

func (Game) Start(ctx rule.StartContext) (rule.State, []rule.Event, error) {
	count, err := parseCount(ctx.Config)
	if err != nil {
		return rule.State{}, nil, err
	}
	d := data{
		Seats: append([]int(nil), ctx.Seats...),
		Count: count,
		Rolls: []roll{},
	}
	if len(d.Seats) == 0 {
		return rule.State{}, nil, errcode.ErrBadAction
	}
	d.Turn = d.Seats[0]
	return rule.State{
		Phase:    phaseRoll,
		Data:     rule.MustJSON(d),
		Deadline: rule.After(rule.TurnTimeout),
	}, nil, nil
}

func (g Game) Apply(state rule.State, seat int, action rule.Action, _ time.Time) (rule.State, []rule.Event, error) {
	if state.Settled || state.Phase != phaseRoll {
		return state, nil, errcode.ErrBadAction
	}
	d, err := decode(state.Data)
	if err != nil {
		return state, nil, err
	}
	if action.Type != "Roll" {
		return state, nil, errcode.ErrBadAction
	}
	if err := emptyPayload(action.Payload); err != nil {
		return state, nil, err
	}
	if seat != d.Turn {
		return state, nil, errcode.ErrNotYourTurn
	}
	return g.roll(d)
}

func (g Game) OnTimeout(state rule.State, _ time.Time) (rule.State, []rule.Event, error) {
	if state.Settled || state.Phase != phaseRoll {
		return state, nil, errcode.ErrBadAction
	}
	d, err := decode(state.Data)
	if err != nil {
		return state, nil, err
	}
	return g.roll(d)
}

func (Game) View(state rule.State, seat int) (rule.Snapshot, error) {
	d, err := decode(state.Data)
	if err != nil {
		return rule.Snapshot{}, err
	}
	turn := 0
	if !state.Settled && state.Phase == phaseRoll {
		turn = d.Turn
	}
	legal := []rule.ActionSpec{}
	if turn != 0 && seat == turn {
		legal = append(legal, rule.ActionSpec{Type: "Roll", Hint: "摇骰"})
	}
	pub := struct {
		Turn  int    `json:"Turn"`
		Count int    `json:"Count"`
		Rolls []roll `json:"Rolls"`
	}{Turn: turn, Count: d.Count, Rolls: d.Rolls}
	return rule.Snapshot{
		Phase:    state.Phase,
		Public:   rule.MustJSON(pub),
		Private:  json.RawMessage(`{}`),
		Settled:  state.Settled,
		Deadline: state.Deadline,
		Legal:    legal,
	}, nil
}

func (g Game) roll(d data) (rule.State, []rule.Event, error) {
	dice := make([]int, d.Count)
	sum := 0
	for i := 0; i < d.Count; i++ {
		dice[i] = rule.Intn(6) + 1
		sum += dice[i]
	}
	rec := roll{Seat: d.Turn, Dice: dice, Sum: sum}
	d.Rolls = append(d.Rolls, rec)
	events := []rule.Event{{
		Type:  "Rolled",
		Scope: rule.ScopePublic,
		Payload: map[string]any{
			"Seat": rec.Seat,
			"Dice": rec.Dice,
			"Sum":  rec.Sum,
		},
	}}
	next := 0
	for i, seat := range d.Seats {
		if seat == rec.Seat && i+1 < len(d.Seats) {
			next = d.Seats[i+1]
			break
		}
	}
	if next != 0 {
		d.Turn = next
		return rule.State{
			Phase:    phaseRoll,
			Data:     rule.MustJSON(d),
			Deadline: rule.After(rule.TurnTimeout),
		}, events, nil
	}
	d.Turn = 0
	deltas := rule.Balance(d.Seats, winnersOf(d.Rolls))
	events = append(events, settleEvent(deltas))
	return rule.State{
		Phase:   phaseEnd,
		Data:    rule.MustJSON(d),
		Settled: true,
	}, events, nil
}

func winnersOf(rolls []roll) []int {
	if len(rolls) == 0 {
		return nil
	}
	best := rolls[0].Sum
	for _, item := range rolls[1:] {
		if item.Sum > best {
			best = item.Sum
		}
	}
	out := make([]int, 0, len(rolls))
	for _, item := range rolls {
		if item.Sum == best {
			out = append(out, item.Seat)
		}
	}
	return out
}

func parseCount(raw json.RawMessage) (int, error) {
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return 2, nil
	}
	var cfg struct {
		Count *int `json:"Count"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return 0, errcode.ErrBadPayload
	}
	if cfg.Count == nil {
		return 2, nil
	}
	n := *cfg.Count
	if n < 1 {
		n = 1
	}
	if n > 5 {
		n = 5
	}
	return n, nil
}

func decode(raw json.RawMessage) (data, error) {
	var d data
	if err := rule.Decode(raw, &d); err != nil {
		return data{}, errcode.ErrBadPayload
	}
	if d.Rolls == nil {
		d.Rolls = []roll{}
	}
	return d, nil
}

func emptyPayload(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || len(m) > 0 {
		return errcode.ErrBadPayload
	}
	return nil
}

func settleEvent(deltas []rule.Delta) rule.Event {
	return rule.Event{
		Type:    rule.EventSettle,
		Scope:   rule.ScopePublic,
		Payload: map[string]any{"Scores": deltas},
	}
}
