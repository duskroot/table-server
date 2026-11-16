// Package zhajinhua is the 炸金花 rule. Cards stay hidden until Look, and
// only the acting seat may bet, compare, or fold.
package zhajinhua

import (
	"encoding/json"
	"sort"
	"time"

	"table/internal/errcode"
	"table/internal/rule"
)

func init() {
	rule.Register(game{})
}

type game struct{}

func (game) Code() string  { return "zhajinhua" }
func (game) MinSeats() int { return 2 }
func (game) MaxSeats() int { return 6 }

type config struct {
	Ante  int `json:"Ante"`
	Chips int `json:"Chips"`
}

type seat struct {
	Seat   int         `json:"Seat"`
	Chips  int         `json:"Chips"`
	Folded bool        `json:"Folded"`
	Looked bool        `json:"Looked"`
	Cards  []rule.Card `json:"Cards"`
}

type data struct {
	Seats   []int  `json:"Seats"`
	Ante    int    `json:"Ante"`
	Base    int    `json:"Base"`
	Pot     int    `json:"Pot"`
	Stake   int    `json:"Stake"`
	Turn    int    `json:"Turn"`
	Players []seat `json:"Players"`
}

type chipView struct {
	Seat  int `json:"Seat"`
	Chips int `json:"Chips"`
}

type publicView struct {
	Pot    int        `json:"Pot"`
	Stake  int        `json:"Stake"`
	Turn   int        `json:"Turn"`
	Folded []int      `json:"Folded"`
	Looked []int      `json:"Looked"`
	Chips  []chipView `json:"Chips"`
}

type privateView struct {
	Cards []rule.Card `json:"Cards,omitempty"`
}

func (g game) Start(ctx rule.StartContext) (rule.State, []rule.Event, error) {
	var cfg config
	if err := rule.Decode(ctx.Config, &cfg); err != nil {
		return rule.State{}, nil, errcode.ErrBadPayload
	}
	if cfg.Ante <= 0 {
		cfg.Ante = 1
	}
	if cfg.Chips <= 0 {
		cfg.Chips = 100
	}
	if cfg.Chips < cfg.Ante || len(ctx.Seats) < g.MinSeats() || len(ctx.Seats) > g.MaxSeats() {
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
	players := make([]seat, 0, len(seats))
	for _, seatNo := range seats {
		hand, rest, err := rule.Take(deck, 3)
		if err != nil {
			return rule.State{}, nil, err
		}
		deck = rest
		players = append(players, seat{Seat: seatNo, Chips: cfg.Chips - cfg.Ante, Cards: hand})
	}
	d := data{
		Seats: seats, Ante: cfg.Ante, Base: cfg.Chips,
		Pot: cfg.Ante * len(seats), Stake: cfg.Ante, Turn: seats[0], Players: players,
	}
	return rule.State{
			Phase: "play", Data: rule.MustJSON(d), Deadline: rule.After(rule.TurnTimeout),
		}, []rule.Event{{
			Type: "Deal", Scope: rule.ScopePublic,
			Payload: map[string]any{"Pot": d.Pot, "Stake": d.Stake, "Turn": d.Turn},
		}}, nil
}

func (g game) Apply(state rule.State, seatNo int, action rule.Action, now time.Time) (rule.State, []rule.Event, error) {
	if state.Settled {
		return state, nil, errcode.ErrBadAction
	}
	d, err := decode(state)
	if err != nil {
		return state, nil, err
	}
	actor := d.seat(seatNo)
	if actor == nil {
		return state, nil, errcode.ErrNotYourTurn
	}
	switch action.Type {
	case "Look":
		return g.look(state, d, actor)
	case "Bet", "Compare", "Fold":
		if seatNo != d.Turn {
			return state, nil, errcode.ErrNotYourTurn
		}
		if actor.Folded {
			return state, nil, errcode.ErrBadAction
		}
		switch action.Type {
		case "Bet":
			return g.bet(state, d, actor, action.Payload)
		case "Compare":
			return g.compare(state, d, actor, action.Payload)
		default:
			return g.fold(state, d, actor)
		}
	default:
		return state, nil, errcode.ErrBadAction
	}
}

func (g game) look(state rule.State, d data, actor *seat) (rule.State, []rule.Event, error) {
	if actor.Folded || actor.Looked {
		return state, nil, errcode.ErrBadAction
	}
	actor.Looked = true
	state.Data = rule.MustJSON(d)
	return state, []rule.Event{{
		Type: "Look", Scope: rule.ScopePublic, Payload: map[string]any{"Seat": actor.Seat},
	}}, nil
}

func (g game) bet(state rule.State, d data, actor *seat, payload json.RawMessage) (rule.State, []rule.Event, error) {
	var body struct {
		Amount *int `json:"Amount"`
	}
	if err := bind(payload, &body); err != nil {
		return state, nil, err
	}
	if body.Amount == nil {
		return state, nil, errcode.ErrBadPayload
	}
	amount := *body.Amount
	if amount <= 0 || amount < d.Stake {
		return state, nil, errcode.ErrBadAction
	}
	pay := amount
	if actor.Looked {
		pay *= 2
	}
	if actor.Chips < pay {
		return state, nil, errcode.ErrBadAction
	}
	actor.Chips -= pay
	d.Pot += pay
	d.Stake = amount
	d.Turn = d.next(actor.Seat)
	state.Data = rule.MustJSON(d)
	state.Deadline = rule.After(rule.TurnTimeout)
	return state, []rule.Event{{
		Type: "Bet", Scope: rule.ScopePublic,
		Payload: map[string]any{"Seat": actor.Seat, "Amount": amount, "Paid": pay},
	}}, nil
}

func (g game) compare(state rule.State, d data, actor *seat, payload json.RawMessage) (rule.State, []rule.Event, error) {
	var body struct {
		TargetSeat *int `json:"TargetSeat"`
	}
	if err := bind(payload, &body); err != nil {
		return state, nil, err
	}
	if body.TargetSeat == nil {
		return state, nil, errcode.ErrBadPayload
	}
	target := d.seat(*body.TargetSeat)
	if target == nil || target.Seat == actor.Seat || target.Folded {
		return state, nil, errcode.ErrBadAction
	}
	pay := d.Stake
	if actor.Looked {
		pay *= 2
	}
	if actor.Chips < pay {
		return state, nil, errcode.ErrBadAction
	}
	actor.Chips -= pay
	d.Pot += pay
	loser := target.Seat
	if cmpHands(actor.Cards, target.Cards) <= 0 {
		loser = actor.Seat
	}
	if who := d.seat(loser); who != nil {
		who.Folded = true
	}
	if len(d.alive()) <= 1 {
		return g.finish(state, d)
	}
	d.Turn = d.next(actor.Seat)
	state.Data = rule.MustJSON(d)
	state.Deadline = rule.After(rule.TurnTimeout)
	return state, []rule.Event{{
		Type: "Compare", Scope: rule.ScopePublic,
		Payload: map[string]any{"Seat": actor.Seat, "TargetSeat": target.Seat, "Loser": loser},
	}}, nil
}

func (g game) fold(state rule.State, d data, actor *seat) (rule.State, []rule.Event, error) {
	actor.Folded = true
	if len(d.alive()) <= 1 {
		return g.finish(state, d)
	}
	d.Turn = d.next(actor.Seat)
	state.Data = rule.MustJSON(d)
	state.Deadline = rule.After(rule.TurnTimeout)
	return state, []rule.Event{{
		Type: "Fold", Scope: rule.ScopePublic, Payload: map[string]any{"Seat": actor.Seat},
	}}, nil
}

func (g game) finish(state rule.State, d data) (rule.State, []rule.Event, error) {
	alive := d.alive()
	if len(alive) != 1 {
		return state, nil, errcode.ErrBadAction
	}
	winner := d.seat(alive[0])
	if winner == nil {
		return state, nil, errcode.ErrBadAction
	}
	winner.Chips += d.Pot
	d.Pot = 0
	d.Turn = 0
	scores := make([]rule.Delta, 0, len(d.Seats))
	for _, seatNo := range d.Seats {
		player := d.seat(seatNo)
		chips := 0
		if player != nil {
			chips = player.Chips
		}
		scores = append(scores, rule.Delta{Seat: seatNo, Delta: chips - d.Base})
	}
	state.Data = rule.MustJSON(d)
	state.Phase = "end"
	state.Settled = true
	state.Deadline = nil
	return state, []rule.Event{{
		Type: rule.EventSettle, Scope: rule.ScopePublic, Payload: map[string]any{"Scores": scores},
	}}, nil
}

func (g game) View(state rule.State, seatNo int) (rule.Snapshot, error) {
	d, err := decode(state)
	if err != nil {
		return rule.Snapshot{}, err
	}
	folded := make([]int, 0)
	looked := make([]int, 0)
	chips := make([]chipView, 0, len(d.Players))
	for _, player := range d.Players {
		chips = append(chips, chipView{Seat: player.Seat, Chips: player.Chips})
		if player.Folded {
			folded = append(folded, player.Seat)
		}
		if player.Looked {
			looked = append(looked, player.Seat)
		}
	}
	pub := publicView{Pot: d.Pot, Stake: d.Stake, Turn: d.Turn, Folded: folded, Looked: looked, Chips: chips}
	priv := privateView{}
	legal := make([]rule.ActionSpec, 0)
	if actor := d.seat(seatNo); actor != nil {
		if actor.Looked {
			priv.Cards = append([]rule.Card(nil), actor.Cards...)
		}
		if !state.Settled && !actor.Folded {
			if !actor.Looked {
				legal = append(legal, rule.ActionSpec{Type: "Look", Hint: "看牌"})
			}
			if seatNo == d.Turn {
				legal = append(legal,
					rule.ActionSpec{Type: "Bet", Hint: "下注"},
					rule.ActionSpec{Type: "Compare", Hint: "比牌"},
					rule.ActionSpec{Type: "Fold", Hint: "弃牌"},
				)
			}
		}
	}
	return rule.Snapshot{
		Phase: state.Phase, Public: rule.MustJSON(pub), Private: rule.MustJSON(priv),
		Settled: state.Settled, Deadline: state.Deadline, Legal: legal,
	}, nil
}

func (g game) OnTimeout(state rule.State, now time.Time) (rule.State, []rule.Event, error) {
	if state.Settled {
		return state, nil, nil
	}
	d, err := decode(state)
	if err != nil {
		return state, nil, err
	}
	if d.seat(d.Turn) == nil {
		return state, nil, errcode.ErrBadAction
	}
	return g.Apply(state, d.Turn, rule.Action{Type: "Fold"}, now)
}

func (d data) seat(seatNo int) *seat {
	for i := range d.Players {
		if d.Players[i].Seat == seatNo {
			return &d.Players[i]
		}
	}
	return nil
}

func (d data) alive() []int {
	out := make([]int, 0, len(d.Seats))
	for _, seatNo := range d.Seats {
		player := d.seat(seatNo)
		if player != nil && !player.Folded {
			out = append(out, seatNo)
		}
	}
	return out
}

func (d data) next(from int) int {
	if len(d.Seats) == 0 {
		return 0
	}
	idx := 0
	for i, seatNo := range d.Seats {
		if seatNo == from {
			idx = i
			break
		}
	}
	for step := 1; step <= len(d.Seats); step++ {
		seatNo := d.Seats[(idx+step)%len(d.Seats)]
		player := d.seat(seatNo)
		if player != nil && !player.Folded {
			return seatNo
		}
	}
	return 0
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

// face treats Ace as 14. Suits are never ordered.
func face(rank int) int {
	if rank == 1 {
		return 14
	}
	return rank
}

type handRank struct {
	kind int
	kick []int
}

const (
	kindHigh = iota + 1
	kindPair
	kindStraight
	kindFlush
	kindStraightFlush
	kindTrips
)

func scoreHand(cards []rule.Card) handRank {
	if len(cards) != 3 {
		return handRank{}
	}
	vals := []int{face(cards[0].Rank), face(cards[1].Rank), face(cards[2].Rank)}
	sort.Slice(vals, func(i, j int) bool { return vals[i] > vals[j] })
	flush := cards[0].Suit == cards[1].Suit && cards[1].Suit == cards[2].Suit
	straight, high := straightHigh(vals)
	switch {
	case vals[0] == vals[1] && vals[1] == vals[2]:
		return handRank{kind: kindTrips, kick: []int{vals[0]}}
	case straight && flush:
		return handRank{kind: kindStraightFlush, kick: []int{high}}
	case flush:
		return handRank{kind: kindFlush, kick: append([]int(nil), vals...)}
	case straight:
		return handRank{kind: kindStraight, kick: []int{high}}
	case vals[0] == vals[1]:
		return handRank{kind: kindPair, kick: []int{vals[0], vals[2]}}
	case vals[1] == vals[2]:
		return handRank{kind: kindPair, kick: []int{vals[1], vals[0]}}
	default:
		return handRank{kind: kindHigh, kick: append([]int(nil), vals...)}
	}
}

// straightHigh ranks A23 below 234 and QKA as the top straight.
func straightHigh(desc []int) (bool, int) {
	a, b, c := desc[0], desc[1], desc[2]
	if a == 14 && b == 3 && c == 2 {
		return true, 3
	}
	if b == a-1 && c == b-1 {
		return true, a
	}
	return false, 0
}

func cmpRank(a, b handRank) int {
	if a.kind != b.kind {
		if a.kind > b.kind {
			return 1
		}
		return -1
	}
	n := len(a.kick)
	if len(b.kick) < n {
		n = len(b.kick)
	}
	for i := 0; i < n; i++ {
		if a.kick[i] == b.kick[i] {
			continue
		}
		if a.kick[i] > b.kick[i] {
			return 1
		}
		return -1
	}
	return 0
}

// cmpHands is positive when a is strictly better than b.
func cmpHands(a, b []rule.Card) int {
	return cmpRank(scoreHand(a), scoreHand(b))
}
