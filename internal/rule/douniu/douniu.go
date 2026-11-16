// Package douniu is the 斗牛 rule. Every seat grabs a multiple, then the
// hand settles at once. Hole cards stay in the private view.
package douniu

import (
	"encoding/json"
	"time"

	"table/internal/errcode"
	"table/internal/rule"
)

func init() {
	rule.Register(game{})
}

type game struct{}

func (game) Code() string  { return "douniu" }
func (game) MinSeats() int { return 2 }
func (game) MaxSeats() int { return 6 }

type player struct {
	Seat     int         `json:"Seat"`
	Cards    []rule.Card `json:"Cards"`
	Grabbed  bool        `json:"Grabbed"`
	Multiple int         `json:"Multiple"`
}

type data struct {
	Seats        []int    `json:"Seats"`
	Players      []player `json:"Players"`
	Banker       int      `json:"Banker,omitempty"`
	BankMultiple int      `json:"BankMultiple,omitempty"`
}

type grabView struct {
	Seat     int `json:"Seat"`
	Multiple int `json:"Multiple"`
}

type publicView struct {
	Grabs    []grabView `json:"Grabs"`
	Banker   int        `json:"Banker,omitempty"`
	Multiple int        `json:"Multiple,omitempty"`
}

type privateView struct {
	Cards []rule.Card `json:"Cards,omitempty"`
}

type openHand struct {
	Seat  int         `json:"Seat"`
	Cards []rule.Card `json:"Cards"`
	Niu   int         `json:"Niu"`
}

func (g game) Start(ctx rule.StartContext) (rule.State, []rule.Event, error) {
	if len(ctx.Seats) < g.MinSeats() || len(ctx.Seats) > g.MaxSeats() {
		return rule.State{}, nil, errcode.ErrBadPayload
	}
	seats := append([]int(nil), ctx.Seats...)
	seen := map[int]bool{}
	for _, seatNo := range seats {
		if seatNo <= 0 || seen[seatNo] {
			return rule.State{}, nil, errcode.ErrBadPayload
		}
		seen[seatNo] = true
	}
	deck := rule.NewShuffledDeck()
	players := make([]player, 0, len(seats))
	for _, seatNo := range seats {
		hand, rest, err := rule.Take(deck, 5)
		if err != nil {
			return rule.State{}, nil, err
		}
		deck = rest
		players = append(players, player{Seat: seatNo, Cards: hand})
	}
	d := data{Seats: seats, Players: players}
	return rule.State{
			Phase: "grab", Data: rule.MustJSON(d), Deadline: rule.After(rule.TurnTimeout),
		}, []rule.Event{{
			Type: "Deal", Scope: rule.ScopePublic, Payload: map[string]any{"Seats": seats},
		}}, nil
}

func (g game) Apply(state rule.State, seatNo int, action rule.Action, _ time.Time) (rule.State, []rule.Event, error) {
	if state.Settled || state.Phase != "grab" {
		return state, nil, errcode.ErrBadAction
	}
	if action.Type != "Grab" {
		return state, nil, errcode.ErrBadAction
	}
	d, err := decode(state)
	if err != nil {
		return state, nil, err
	}
	actor := d.player(seatNo)
	if actor == nil {
		return state, nil, errcode.ErrNotYourTurn
	}
	if actor.Grabbed {
		return state, nil, errcode.ErrBadAction
	}
	var body struct {
		Multiple *int `json:"Multiple"`
	}
	if err := bind(action.Payload, &body); err != nil {
		return state, nil, err
	}
	if body.Multiple == nil {
		return state, nil, errcode.ErrBadPayload
	}
	if *body.Multiple < 0 || *body.Multiple > 3 {
		return state, nil, errcode.ErrBadAction
	}
	actor.Grabbed = true
	actor.Multiple = *body.Multiple
	if d.allGrabbed() {
		return g.finish(state, d)
	}
	state.Data = rule.MustJSON(d)
	state.Deadline = rule.After(rule.TurnTimeout)
	return state, []rule.Event{{
		Type: "Grab", Scope: rule.ScopePublic,
		Payload: map[string]any{"Seat": seatNo, "Multiple": *body.Multiple},
	}}, nil
}

func (g game) finish(state rule.State, d data) (rule.State, []rule.Event, error) {
	if len(d.Seats) == 0 {
		return state, nil, errcode.ErrBadAction
	}
	points := make(map[int]int, len(d.Players))
	for i := range d.Players {
		points[d.Players[i].Seat] = niuPoint(d.Players[i].Cards)
	}
	banker := d.Seats[0]
	bankerMult := 0
	if actor := d.player(banker); actor != nil {
		bankerMult = actor.Multiple
	}
	for _, seatNo := range d.Seats {
		actor := d.player(seatNo)
		if actor == nil {
			continue
		}
		if actor.Multiple > bankerMult || (actor.Multiple == bankerMult && seatNo < banker) {
			banker = seatNo
			bankerMult = actor.Multiple
		}
	}
	if bankerMult < 1 {
		bankerMult = 1
	}
	d.Banker = banker
	d.BankMultiple = bankerMult
	deltas := map[int]int{}
	for _, seatNo := range d.Seats {
		deltas[seatNo] = 0
	}
	bankerPoint := points[banker]
	for _, seatNo := range d.Seats {
		if seatNo == banker {
			continue
		}
		if points[seatNo] > bankerPoint {
			deltas[seatNo] += bankerMult
			deltas[banker] -= bankerMult
			continue
		}
		deltas[seatNo] -= bankerMult
		deltas[banker] += bankerMult
	}
	scores := make([]rule.Delta, 0, len(d.Seats))
	hands := make([]openHand, 0, len(d.Seats))
	for _, seatNo := range d.Seats {
		scores = append(scores, rule.Delta{Seat: seatNo, Delta: deltas[seatNo]})
		actor := d.player(seatNo)
		hand := openHand{Seat: seatNo, Niu: points[seatNo]}
		if actor != nil {
			hand.Cards = append([]rule.Card(nil), actor.Cards...)
		}
		hands = append(hands, hand)
	}
	state.Data = rule.MustJSON(d)
	state.Phase = "end"
	state.Settled = true
	state.Deadline = nil
	return state, []rule.Event{{
		Type: rule.EventSettle, Scope: rule.ScopePublic,
		Payload: map[string]any{"Scores": scores, "Hands": hands},
	}}, nil
}

func (g game) View(state rule.State, seatNo int) (rule.Snapshot, error) {
	d, err := decode(state)
	if err != nil {
		return rule.Snapshot{}, err
	}
	grabs := make([]grabView, 0)
	var own []rule.Card
	legal := make([]rule.ActionSpec, 0)
	for i := range d.Players {
		player := d.Players[i]
		if player.Grabbed {
			grabs = append(grabs, grabView{Seat: player.Seat, Multiple: player.Multiple})
		}
		if player.Seat != seatNo {
			continue
		}
		own = append([]rule.Card(nil), player.Cards...)
		if !state.Settled && !player.Grabbed {
			legal = append(legal, rule.ActionSpec{Type: "Grab", Hint: "抢庄"})
		}
	}
	pub := publicView{Grabs: grabs}
	if state.Settled {
		pub.Banker = d.Banker
		pub.Multiple = d.BankMultiple
	}
	return rule.Snapshot{
		Phase: state.Phase, Public: rule.MustJSON(pub), Private: rule.MustJSON(privateView{Cards: own}),
		Settled: state.Settled, Deadline: state.Deadline, Legal: legal,
	}, nil
}

func (g game) OnTimeout(state rule.State, _ time.Time) (rule.State, []rule.Event, error) {
	if state.Settled {
		return state, nil, nil
	}
	d, err := decode(state)
	if err != nil {
		return state, nil, err
	}
	for i := range d.Players {
		if d.Players[i].Grabbed {
			continue
		}
		d.Players[i].Grabbed = true
		d.Players[i].Multiple = 0
	}
	return g.finish(state, d)
}

func (d data) player(seatNo int) *player {
	for i := range d.Players {
		if d.Players[i].Seat == seatNo {
			return &d.Players[i]
		}
	}
	return nil
}

func (d data) allGrabbed() bool {
	if len(d.Players) == 0 {
		return false
	}
	for i := range d.Players {
		if !d.Players[i].Grabbed {
			return false
		}
	}
	return true
}

func decode(state rule.State) (data, error) {
	var d data
	if err := rule.Decode(state.Data, &d); err != nil {
		return data{}, err
	}
	return d, nil
}

func bind(raw json.RawMessage, dest any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return errcode.ErrBadPayload
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return errcode.ErrBadPayload
	}
	return nil
}

func pip(rank int) int {
	if rank <= 1 {
		return 1
	}
	if rank >= 10 {
		return 10
	}
	return rank
}

// niuPoint returns 10 for 牛牛, 1..9 for 牛一 to 牛九, and 0 for 没牛.
// Among several valid triples, the higher remainder wins.
func niuPoint(cards []rule.Card) int {
	if len(cards) != 5 {
		return 0
	}
	p := make([]int, 5)
	for i, card := range cards {
		p[i] = pip(card.Rank)
	}
	best := -1
	for i := 0; i < 5; i++ {
		for j := i + 1; j < 5; j++ {
			for k := j + 1; k < 5; k++ {
				if (p[i]+p[j]+p[k])%10 != 0 {
					continue
				}
				rest := 0
				for t := 0; t < 5; t++ {
					if t == i || t == j || t == k {
						continue
					}
					rest += p[t]
				}
				point := rest % 10
				if point == 0 {
					point = 10
				}
				if point > best {
					best = point
				}
			}
		}
	}
	if best < 0 {
		return 0
	}
	return best
}
