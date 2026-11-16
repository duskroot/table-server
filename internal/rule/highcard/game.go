// Package highcard deals one public card per seat. Ace is high, then suit breaks ties.
package highcard

import (
	"encoding/json"
	"time"

	"table/internal/errcode"
	"table/internal/rule"
)

func init() {
	rule.Register(Game{})
}

// Game is the high-card rule.
type Game struct{}

func (Game) Code() string  { return "highcard" }
func (Game) MinSeats() int { return 2 }
func (Game) MaxSeats() int { return 8 }

const (
	phaseDraw = "draw"
	phaseEnd  = "end"
)

type drawn struct {
	Seat int       `json:"Seat"`
	Card rule.Card `json:"Card"`
}

type data struct {
	Seats []int       `json:"Seats"`
	Turn  int         `json:"Turn"`
	Deck  []rule.Card `json:"Deck"`
	Drawn []drawn     `json:"Drawn"`
}

type publicView struct {
	Turn  int     `json:"Turn"`
	Drawn []drawn `json:"Drawn"`
}

func (Game) Start(ctx rule.StartContext) (rule.State, []rule.Event, error) {
	d := data{
		Seats: append([]int(nil), ctx.Seats...),
		Deck:  rule.NewShuffledDeck(),
		Drawn: []drawn{},
	}
	if len(d.Seats) == 0 {
		return rule.State{}, nil, errcode.ErrBadAction
	}
	d.Turn = d.Seats[0]
	return rule.State{
		Phase:    phaseDraw,
		Data:     rule.MustJSON(d),
		Deadline: rule.After(rule.TurnTimeout),
	}, nil, nil
}

func (g Game) Apply(state rule.State, seat int, action rule.Action, _ time.Time) (rule.State, []rule.Event, error) {
	if state.Settled || state.Phase != phaseDraw {
		return state, nil, errcode.ErrBadAction
	}
	d, err := decode(state.Data)
	if err != nil {
		return state, nil, err
	}
	if action.Type != "Draw" {
		return state, nil, errcode.ErrBadAction
	}
	if err := emptyPayload(action.Payload); err != nil {
		return state, nil, err
	}
	if seat != d.Turn {
		return state, nil, errcode.ErrNotYourTurn
	}
	return g.draw(d)
}

func (g Game) OnTimeout(state rule.State, _ time.Time) (rule.State, []rule.Event, error) {
	if state.Settled || state.Phase != phaseDraw {
		return state, nil, errcode.ErrBadAction
	}
	d, err := decode(state.Data)
	if err != nil {
		return state, nil, err
	}
	return g.draw(d)
}

func (Game) View(state rule.State, seat int) (rule.Snapshot, error) {
	d, err := decode(state.Data)
	if err != nil {
		return rule.Snapshot{}, err
	}
	turn := 0
	if !state.Settled && state.Phase == phaseDraw {
		turn = d.Turn
	}
	legal := []rule.ActionSpec{}
	if turn != 0 && seat == turn {
		legal = append(legal, rule.ActionSpec{Type: "Draw", Hint: "抽牌"})
	}
	if d.Drawn == nil {
		d.Drawn = []drawn{}
	}
	return rule.Snapshot{
		Phase:    state.Phase,
		Public:   rule.MustJSON(publicView{Turn: turn, Drawn: d.Drawn}),
		Private:  json.RawMessage(`{}`),
		Settled:  state.Settled,
		Deadline: state.Deadline,
		Legal:    legal,
	}, nil
}

func (g Game) draw(d data) (rule.State, []rule.Event, error) {
	taken, rest, err := rule.Take(d.Deck, 1)
	if err != nil {
		return rule.State{}, nil, err
	}
	d.Deck = rest
	card := taken[0]
	rec := drawn{Seat: d.Turn, Card: card}
	d.Drawn = append(d.Drawn, rec)
	events := []rule.Event{{
		Type:  "Drawn",
		Scope: rule.ScopePublic,
		Payload: map[string]any{
			"Seat": rec.Seat,
			"Card": rec.Card,
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
			Phase:    phaseDraw,
			Data:     rule.MustJSON(d),
			Deadline: rule.After(rule.TurnTimeout),
		}, events, nil
	}
	d.Turn = 0
	events = append(events, settleEvent(rule.Balance(d.Seats, []int{winnerSeat(d.Drawn)})))
	return rule.State{
		Phase:   phaseEnd,
		Data:    rule.MustJSON(d),
		Settled: true,
	}, events, nil
}

func winnerSeat(cards []drawn) int {
	best := cards[0]
	for _, item := range cards[1:] {
		if beats(item.Card, best.Card) {
			best = item
		}
	}
	return best.Seat
}

func beats(a, b rule.Card) bool {
	ar, br := rankValue(a), rankValue(b)
	if ar != br {
		return ar > br
	}
	return a.Suit > b.Suit
}

func rankValue(c rule.Card) int {
	if c.Rank == 1 {
		return 14
	}
	return c.Rank
}

func decode(raw json.RawMessage) (data, error) {
	var d data
	if err := rule.Decode(raw, &d); err != nil {
		return data{}, errcode.ErrBadPayload
	}
	if d.Drawn == nil {
		d.Drawn = []drawn{}
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
