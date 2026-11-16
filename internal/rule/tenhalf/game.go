// Package tenhalf is ten-and-a-half. Seat 0 is the dealer; each other seat plays against the dealer.
package tenhalf

import (
	"encoding/json"
	"time"

	"table/internal/errcode"
	"table/internal/rule"
)

func init() {
	rule.Register(Game{})
}

// Game is the ten-and-a-half rule.
type Game struct{}

func (Game) Code() string  { return "tenhalf" }
func (Game) MinSeats() int { return 2 }
func (Game) MaxSeats() int { return 6 }

const (
	phasePlay   = "play"
	phaseDealer = "dealer"
	phaseEnd    = "end"
)

type hand struct {
	Seat  int         `json:"Seat"`
	Cards []rule.Card `json:"Cards"`
	Sum   int         `json:"Sum"`
	Bust  bool        `json:"Bust"`
	Done  bool        `json:"Done"`
}

type data struct {
	Seats  []int       `json:"Seats"`
	Dealer int         `json:"Dealer"`
	Turn   int         `json:"Turn"`
	Deck   []rule.Card `json:"Deck"`
	Hands  []hand      `json:"Hands"`
}

type seatView struct {
	Seat  int         `json:"Seat"`
	Cards []rule.Card `json:"Cards"`
	Sum   int         `json:"Sum"`
	Bust  bool        `json:"Bust"`
	Done  bool        `json:"Done"`
}

type publicView struct {
	Dealer int        `json:"Dealer"`
	Turn   int        `json:"Turn"`
	Hands  []seatView `json:"Hands"`
}

func (Game) Start(ctx rule.StartContext) (rule.State, []rule.Event, error) {
	seats := append([]int(nil), ctx.Seats...)
	if len(seats) < 2 {
		return rule.State{}, nil, errcode.ErrBadAction
	}
	deck := rule.NewShuffledDeck()
	hands := make([]hand, 0, len(seats))
	for _, seat := range seats {
		taken, rest, err := rule.Take(deck, 1)
		if err != nil {
			return rule.State{}, nil, err
		}
		deck = rest
		card := taken[0]
		hands = append(hands, hand{
			Seat:  seat,
			Cards: []rule.Card{card},
			Sum:   pip(card),
		})
	}
	d := data{
		Seats:  seats,
		Dealer: seats[0],
		Turn:   seats[1],
		Deck:   deck,
		Hands:  hands,
	}
	return rule.State{
			Phase:    phasePlay,
			Data:     rule.MustJSON(d),
			Deadline: rule.After(rule.TurnTimeout),
		}, []rule.Event{{
			Type:    "Dealt",
			Scope:   rule.ScopePublic,
			Payload: map[string]any{"Dealer": d.Dealer, "Hands": viewHands(d)},
		}}, nil
}

func (g Game) Apply(state rule.State, seat int, action rule.Action, _ time.Time) (rule.State, []rule.Event, error) {
	if state.Settled || (state.Phase != phasePlay && state.Phase != phaseDealer) {
		return state, nil, errcode.ErrBadAction
	}
	d, err := decode(state.Data)
	if err != nil {
		return state, nil, err
	}
	if state.Phase == phaseDealer {
		if seat != d.Turn {
			return state, nil, errcode.ErrNotYourTurn
		}
		return state, nil, errcode.ErrBadAction
	}
	if action.Type != "Hit" && action.Type != "Stand" {
		return state, nil, errcode.ErrBadAction
	}
	if err := emptyPayload(action.Payload); err != nil {
		return state, nil, err
	}
	if seat != d.Turn {
		return state, nil, errcode.ErrNotYourTurn
	}
	switch action.Type {
	case "Hit":
		return g.hit(d)
	default:
		return g.stand(d)
	}
}

func (g Game) OnTimeout(state rule.State, _ time.Time) (rule.State, []rule.Event, error) {
	if state.Settled {
		return state, nil, errcode.ErrBadAction
	}
	d, err := decode(state.Data)
	if err != nil {
		return state, nil, err
	}
	if state.Phase == phaseDealer || d.Turn == d.Dealer {
		return g.finishDealer(d)
	}
	if state.Phase != phasePlay {
		return state, nil, errcode.ErrBadAction
	}
	return g.stand(d)
}

func (Game) View(state rule.State, seat int) (rule.Snapshot, error) {
	d, err := decode(state.Data)
	if err != nil {
		return rule.Snapshot{}, err
	}
	turn := 0
	if !state.Settled {
		turn = d.Turn
	}
	legal := []rule.ActionSpec{}
	if state.Phase == phasePlay && turn != 0 && seat == turn {
		legal = append(legal,
			rule.ActionSpec{Type: "Hit", Hint: "要牌"},
			rule.ActionSpec{Type: "Stand", Hint: "停牌"},
		)
	}
	return rule.Snapshot{
		Phase:    state.Phase,
		Public:   rule.MustJSON(publicView{Dealer: d.Dealer, Turn: turn, Hands: viewHands(d)}),
		Private:  json.RawMessage(`{}`),
		Settled:  state.Settled,
		Deadline: state.Deadline,
		Legal:    legal,
	}, nil
}

func (g Game) hit(d data) (rule.State, []rule.Event, error) {
	h := d.hand(d.Turn)
	if h == nil || h.Done {
		return rule.State{}, nil, errcode.ErrBadAction
	}
	taken, rest, err := rule.Take(d.Deck, 1)
	if err != nil {
		return rule.State{}, nil, err
	}
	d.Deck = rest
	card := taken[0]
	h.Cards = append(h.Cards, card)
	h.Sum += pip(card)
	if h.Sum > 21 {
		h.Bust = true
		h.Done = true
	}
	events := []rule.Event{{
		Type:  "Hit",
		Scope: rule.ScopePublic,
		Payload: map[string]any{
			"Seat": h.Seat,
			"Card": card,
			"Sum":  h.Sum,
			"Bust": h.Bust,
		},
	}}
	if !h.Done {
		return rule.State{
			Phase:    phasePlay,
			Data:     rule.MustJSON(d),
			Deadline: rule.After(rule.TurnTimeout),
		}, events, nil
	}
	return g.advance(d, events)
}

func (g Game) stand(d data) (rule.State, []rule.Event, error) {
	h := d.hand(d.Turn)
	if h == nil || h.Done {
		return rule.State{}, nil, errcode.ErrBadAction
	}
	h.Done = true
	events := []rule.Event{{
		Type:    "Stand",
		Scope:   rule.ScopePublic,
		Payload: map[string]any{"Seat": h.Seat},
	}}
	return g.advance(d, events)
}

func (g Game) advance(d data, events []rule.Event) (rule.State, []rule.Event, error) {
	if next, ok := d.nextPlayer(d.Turn); ok {
		d.Turn = next
		return rule.State{
			Phase:    phasePlay,
			Data:     rule.MustJSON(d),
			Deadline: rule.After(rule.TurnTimeout),
		}, events, nil
	}
	d.Turn = d.Dealer
	return rule.State{
		Phase:    phaseDealer,
		Data:     rule.MustJSON(d),
		Deadline: rule.After(rule.TurnTimeout),
	}, events, nil
}

func (g Game) finishDealer(d data) (rule.State, []rule.Event, error) {
	d.Turn = d.Dealer
	h := d.hand(d.Dealer)
	if h == nil {
		return rule.State{}, nil, errcode.ErrBadAction
	}
	var events []rule.Event
	for h.Sum <= 14 && !h.Bust {
		taken, rest, err := rule.Take(d.Deck, 1)
		if err != nil {
			return rule.State{}, nil, err
		}
		d.Deck = rest
		card := taken[0]
		h.Cards = append(h.Cards, card)
		h.Sum += pip(card)
		if h.Sum > 21 {
			h.Bust = true
		}
		events = append(events, rule.Event{
			Type:  "Hit",
			Scope: rule.ScopePublic,
			Payload: map[string]any{
				"Seat": h.Seat,
				"Card": card,
				"Sum":  h.Sum,
				"Bust": h.Bust,
			},
		})
	}
	h.Done = true
	d.Turn = 0
	events = append(events, settleEvent(settleScores(d)))
	return rule.State{
		Phase:   phaseEnd,
		Data:    rule.MustJSON(d),
		Settled: true,
	}, events, nil
}

// settleScores pays each non-dealer against the dealer. A push stays at zero
// and is never handed to Balance, which would charge that seat as a loser.
func settleScores(d data) []rule.Delta {
	totals := make(map[int]int, len(d.Seats))
	for _, seat := range d.Seats {
		totals[seat] = 0
	}
	dealer := d.hand(d.Dealer)
	for _, seat := range d.Seats {
		if seat == d.Dealer {
			continue
		}
		player := d.hand(seat)
		switch compare(player, dealer) {
		case 0:
			continue
		case 1:
			addPair(totals, rule.Balance([]int{d.Dealer, seat}, []int{seat}))
		default:
			addPair(totals, rule.Balance([]int{d.Dealer, seat}, []int{d.Dealer}))
		}
	}
	out := make([]rule.Delta, 0, len(d.Seats))
	for _, seat := range d.Seats {
		out = append(out, rule.Delta{Seat: seat, Delta: totals[seat]})
	}
	return out
}

func addPair(totals map[int]int, deltas []rule.Delta) {
	for _, row := range deltas {
		totals[row.Seat] += row.Delta
	}
}

func compare(player, dealer *hand) int {
	if player == nil || player.Bust {
		return -1
	}
	if dealer == nil || dealer.Bust {
		return 1
	}
	switch {
	case player.Sum > dealer.Sum:
		return 1
	case player.Sum < dealer.Sum:
		return -1
	default:
		return 0
	}
}

func (d *data) hand(seat int) *hand {
	for i := range d.Hands {
		if d.Hands[i].Seat == seat {
			return &d.Hands[i]
		}
	}
	return nil
}

func (d data) nextPlayer(from int) (int, bool) {
	start := 0
	for i, seat := range d.Seats {
		if seat == from {
			start = i
			break
		}
	}
	for step := 1; step < len(d.Seats); step++ {
		seat := d.Seats[(start+step)%len(d.Seats)]
		if seat == d.Dealer {
			continue
		}
		h := d.hand(seat)
		if h != nil && !h.Done {
			return seat, true
		}
	}
	return 0, false
}

func viewHands(d data) []seatView {
	out := make([]seatView, 0, len(d.Hands))
	for _, h := range d.Hands {
		cards := h.Cards
		if cards == nil {
			cards = []rule.Card{}
		}
		out = append(out, seatView{
			Seat: h.Seat, Cards: cards, Sum: h.Sum, Bust: h.Bust, Done: h.Done,
		})
	}
	return out
}

func pip(c rule.Card) int {
	switch {
	case c.Rank == 1:
		return 2
	case c.Rank >= 2 && c.Rank <= 10:
		return c.Rank * 2
	case c.Rank >= 11 && c.Rank <= 13:
		return 1
	default:
		return 0
	}
}

func decode(raw json.RawMessage) (data, error) {
	var d data
	if err := rule.Decode(raw, &d); err != nil {
		return data{}, errcode.ErrBadPayload
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
