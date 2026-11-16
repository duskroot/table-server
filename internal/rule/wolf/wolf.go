// Package wolf is the werewolf drinking rule.
package wolf

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"table/internal/errcode"
	"table/internal/rule"
)

const (
	phaseWolf  = "night_wolf"
	phaseSeer  = "night_seer"
	phaseWitch = "night_witch"
	phaseSpeak = "day_speak"
	phaseVote  = "day_vote"
	phaseEnd   = "end"

	roleWolf     = "wolf"
	roleSeer     = "seer"
	roleWitch    = "witch"
	roleVillager = "villager"

	actKnife  = "Knife"
	actCheck  = "Check"
	actPotion = "Potion"
	actSpeak  = "Speak"
	actVote   = "Vote"

	variantClassic = "classic"
	variantWitch   = "witch"
)

type game struct{}

func init() { rule.Register(game{}) }

func (game) Code() string  { return "wolf" }
func (game) MinSeats() int { return 6 }
func (game) MaxSeats() int { return 12 }

type player struct {
	Seat  int    `json:"Seat"`
	Role  string `json:"Role"`
	Alive bool   `json:"Alive"`
}

type speech struct {
	Seat int    `json:"Seat"`
	Text string `json:"Text"`
}

type vote struct {
	Seat       int `json:"Seat"`
	TargetSeat int `json:"TargetSeat"`
}

type knifeVote struct {
	Seat       int `json:"Seat"`
	TargetSeat int `json:"TargetSeat"`
}

type check struct {
	TargetSeat int  `json:"TargetSeat"`
	Wolf       bool `json:"Wolf"`
}

type wolfData struct {
	Variant    string      `json:"Variant"`
	Seats      []int       `json:"Seats"`
	Players    []player    `json:"Players"`
	Turn       int         `json:"Turn"`
	Speeches   []speech    `json:"Speeches"`
	Votes      []vote      `json:"Votes"`
	Knives     []knifeVote `json:"Knives"`
	Knifed     int         `json:"Knifed"`
	Saved      bool        `json:"Saved"`
	Poisoned   int         `json:"Poisoned"`
	SaveLeft   bool        `json:"SaveLeft"`
	PoisonLeft bool        `json:"PoisonLeft"`
	Checks     []check     `json:"Checks"`
}

type wolfPublic struct {
	Seats    []int    `json:"Seats"`
	Alive    []int    `json:"Alive"`
	Dead     []int    `json:"Dead"`
	Turn     int      `json:"Turn"`
	Speeches []speech `json:"Speeches"`
	Votes    []vote   `json:"Votes"`
}

type potionView struct {
	SaveLeft   bool `json:"SaveLeft"`
	PoisonLeft bool `json:"PoisonLeft"`
	Knifed     int  `json:"Knifed"`
}

type identity struct {
	Role string `json:"Role"`
}

type scoreRow struct {
	Seat   int `json:"Seat"`
	Delta  int `json:"Delta"`
	Detail any `json:"Detail,omitempty"`
}

func (game) Start(ctx rule.StartContext) (rule.State, []rule.Event, error) {
	seats := append([]int(nil), ctx.Seats...)
	sortInts(seats)
	if len(seats) < 6 || len(seats) > 12 || !uniqueSeats(seats) {
		return rule.State{}, nil, errcode.ErrBadPayload
	}
	variant := ctx.Variant
	if variant == "" {
		variant = variantClassic
	}
	if variant != variantClassic && variant != variantWitch {
		return rule.State{}, nil, errcode.ErrBadPayload
	}
	roles := []string{roleWolf, roleWolf, roleSeer}
	if variant == variantWitch {
		roles = append(roles, roleWitch)
	}
	for len(roles) < len(seats) {
		roles = append(roles, roleVillager)
	}
	villagers := 0
	for _, role := range roles {
		if role == roleVillager {
			villagers++
		}
	}
	if villagers < 1 {
		return rule.State{}, nil, errcode.ErrBadPayload
	}
	shuffle(len(roles), func(i, j int) { roles[i], roles[j] = roles[j], roles[i] })
	players := make([]player, len(seats))
	for i, seat := range seats {
		players[i] = player{Seat: seat, Role: roles[i], Alive: true}
	}
	data := wolfData{
		Variant: variant, Seats: seats, Players: players,
		Speeches: []speech{}, Votes: []vote{}, Knives: []knifeVote{}, Checks: []check{},
		SaveLeft: true, PoisonLeft: true,
	}
	wolves := livingWolves(&data)
	if len(wolves) == 0 {
		return rule.State{}, nil, errcode.ErrBadPayload
	}
	return waiting(&data, phaseWolf, wolves[0]), []rule.Event{{
		Type: "Start", Scope: rule.ScopePublic,
		Payload: map[string]any{"Seats": seats, "Variant": variant},
	}}, nil
}

func (g game) Apply(state rule.State, seat int, action rule.Action, _ time.Time) (rule.State, []rule.Event, error) {
	if state.Settled {
		return state, nil, errcode.ErrBadAction
	}
	var data wolfData
	if err := rule.Decode(state.Data, &data); err != nil {
		return state, nil, err
	}
	switch state.Phase {
	case phaseWolf:
		if action.Type != actKnife {
			return state, nil, errcode.ErrBadAction
		}
		if seat != data.Turn {
			return state, nil, errcode.ErrNotYourTurn
		}
		if !aliveWolf(&data, seat) {
			return state, nil, errcode.ErrBadAction
		}
		target, err := targetSeat(action.Payload)
		if err != nil {
			return state, nil, err
		}
		if !isAlive(&data, target) {
			return state, nil, errcode.ErrBadPayload
		}
		return castKnife(&data, seat, target)
	case phaseSeer:
		if action.Type != actCheck {
			return state, nil, errcode.ErrBadAction
		}
		if seat != data.Turn {
			return state, nil, errcode.ErrNotYourTurn
		}
		if livingRole(&data, roleSeer) != seat {
			return state, nil, errcode.ErrBadAction
		}
		target, err := targetSeat(action.Payload)
		if err != nil {
			return state, nil, err
		}
		if roleOf(&data, target) == "" {
			return state, nil, errcode.ErrBadPayload
		}
		return castCheck(&data, seat, target)
	case phaseWitch:
		if action.Type != actPotion {
			return state, nil, errcode.ErrBadAction
		}
		if seat != data.Turn {
			return state, nil, errcode.ErrNotYourTurn
		}
		if livingRole(&data, roleWitch) != seat {
			return state, nil, errcode.ErrBadAction
		}
		var body struct {
			Save   bool `json:"Save"`
			Poison int  `json:"Poison"`
		}
		if err := decodePayload(action.Payload, &body); err != nil {
			return state, nil, err
		}
		next, events, err := castPotion(&data, body.Save, body.Poison)
		if err != nil {
			return state, nil, err
		}
		return next, events, nil
	case phaseSpeak:
		if action.Type != actSpeak {
			return state, nil, errcode.ErrBadAction
		}
		if seat != data.Turn {
			return state, nil, errcode.ErrNotYourTurn
		}
		if !isAlive(&data, seat) {
			return state, nil, errcode.ErrBadAction
		}
		var body struct {
			Text string `json:"Text"`
		}
		if err := decodePayload(action.Payload, &body); err != nil {
			return state, nil, err
		}
		text, err := cleanText(body.Text)
		if err != nil {
			return state, nil, err
		}
		return castSpeak(&data, seat, text)
	case phaseVote:
		if action.Type != actVote {
			return state, nil, errcode.ErrBadAction
		}
		if seat != data.Turn {
			return state, nil, errcode.ErrNotYourTurn
		}
		if !isAlive(&data, seat) {
			return state, nil, errcode.ErrBadAction
		}
		target, err := targetSeat(action.Payload)
		if err != nil {
			return state, nil, err
		}
		if target == seat || !isAlive(&data, target) {
			return state, nil, errcode.ErrBadPayload
		}
		return castVote(&data, seat, target)
	default:
		return state, nil, errcode.ErrBadAction
	}
}

func (game) View(state rule.State, seat int) (rule.Snapshot, error) {
	var data wolfData
	if err := rule.Decode(state.Data, &data); err != nil {
		return rule.Snapshot{}, err
	}
	if data.Speeches == nil {
		data.Speeches = []speech{}
	}
	if data.Votes == nil {
		data.Votes = []vote{}
	}
	turn := data.Turn
	switch state.Phase {
	case phaseWolf, phaseSeer, phaseWitch:
		// Night actions would identify wolves, the seer, or the witch.
		turn = 0
	}
	legal := []rule.ActionSpec{}
	if !state.Settled && seat == data.Turn {
		switch state.Phase {
		case phaseWolf:
			legal = []rule.ActionSpec{{Type: actKnife, Hint: "选择刀人目标"}}
		case phaseSeer:
			legal = []rule.ActionSpec{{Type: actCheck, Hint: "查验一名玩家"}}
		case phaseWitch:
			legal = []rule.ActionSpec{{Type: actPotion, Hint: "使用解药或毒药"}}
		case phaseSpeak:
			legal = []rule.ActionSpec{{Type: actSpeak, Hint: "发言"}}
		case phaseVote:
			legal = []rule.ActionSpec{{Type: actVote, Hint: "投票"}}
		}
	}
	pub := wolfPublic{
		Seats: data.Seats, Alive: livingSeats(&data), Dead: deadSeats(&data),
		Turn: turn, Speeches: data.Speeches, Votes: data.Votes,
	}
	return rule.Snapshot{
		Phase: state.Phase, Public: rule.MustJSON(pub), Private: rule.MustJSON(buildPrivate(&data, seat)),
		Settled: state.Settled, Deadline: state.Deadline, Legal: legal,
	}, nil
}

func (g game) OnTimeout(state rule.State, now time.Time) (rule.State, []rule.Event, error) {
	if state.Settled {
		return state, nil, nil
	}
	var data wolfData
	if err := rule.Decode(state.Data, &data); err != nil {
		return state, nil, err
	}
	switch state.Phase {
	case phaseWolf:
		return castKnife(&data, data.Turn, 0)
	case phaseSeer:
		return afterSeer(&data)
	case phaseWitch:
		return dawn(&data)
	case phaseSpeak:
		return g.Apply(state, data.Turn, rule.Action{
			Type: actSpeak, Payload: rule.MustJSON(map[string]string{"Text": "……"}),
		}, now)
	case phaseVote:
		return castVote(&data, data.Turn, 0)
	default:
		return state, nil, nil
	}
}

func castKnife(data *wolfData, seat, target int) (rule.State, []rule.Event, error) {
	data.Knives = append(data.Knives, knifeVote{Seat: seat, TargetSeat: target})
	if next, ok := nextAfter(livingWolves(data), seat); ok {
		return waiting(data, phaseWolf, next), nil, nil
	}
	return afterKnives(data)
}

func afterKnives(data *wolfData) (rule.State, []rule.Event, error) {
	data.Knifed = majoritySeat(data.Knives)
	if seer := livingRole(data, roleSeer); seer > 0 {
		return waiting(data, phaseSeer, seer), nil, nil
	}
	return afterSeer(data)
}

func castCheck(data *wolfData, seat, target int) (rule.State, []rule.Event, error) {
	rec := check{TargetSeat: target, Wolf: roleOf(data, target) == roleWolf}
	data.Checks = append(data.Checks, rec)
	ev := rule.Event{Type: actCheck, Scope: seat, Payload: rec}
	state, events, err := afterSeer(data)
	if err != nil {
		return state, nil, err
	}
	return state, append([]rule.Event{ev}, events...), nil
}

func afterSeer(data *wolfData) (rule.State, []rule.Event, error) {
	if data.Variant == variantWitch {
		if witch := livingRole(data, roleWitch); witch > 0 {
			return waiting(data, phaseWitch, witch), nil, nil
		}
	}
	return dawn(data)
}

func castPotion(data *wolfData, save bool, poison int) (rule.State, []rule.Event, error) {
	if save && poison != -1 {
		return rule.State{}, nil, errcode.ErrBadPayload
	}
	if save {
		if !data.SaveLeft || data.Knifed == 0 {
			return rule.State{}, nil, errcode.ErrBadPayload
		}
		data.SaveLeft = false
		data.Saved = true
	}
	if poison != -1 {
		if !data.PoisonLeft || !isAlive(data, poison) {
			return rule.State{}, nil, errcode.ErrBadPayload
		}
		data.PoisonLeft = false
		data.Poisoned = poison
	}
	return dawn(data)
}

func dawn(data *wolfData) (rule.State, []rule.Event, error) {
	dying := map[int]bool{}
	if data.Knifed > 0 && !data.Saved {
		dying[data.Knifed] = true
	}
	if data.Poisoned > 0 {
		dying[data.Poisoned] = true
	}
	dead := []int{}
	for _, p := range data.Players {
		if dying[p.Seat] && kill(data, p.Seat) {
			dead = append(dead, p.Seat)
		}
	}
	sortInts(dead)
	events := []rule.Event{{
		Type: "Dawn", Scope: rule.ScopePublic, Payload: map[string]any{"Dead": dead},
	}}
	if done, wolfWin := outcome(data); done {
		state, ev := finish(data, wolfWin)
		return state, append(events, ev), nil
	}
	return beginSpeak(data), events, nil
}

func castSpeak(data *wolfData, seat int, text string) (rule.State, []rule.Event, error) {
	data.Speeches = append(data.Speeches, speech{Seat: seat, Text: text})
	ev := rule.Event{Type: actSpeak, Scope: rule.ScopePublic, Payload: speech{Seat: seat, Text: text}}
	if next, ok := nextAfter(livingSeats(data), seat); ok {
		return waiting(data, phaseSpeak, next), []rule.Event{ev}, nil
	}
	return beginVote(data), []rule.Event{ev}, nil
}

func beginSpeak(data *wolfData) rule.State {
	data.Speeches = []speech{}
	data.Votes = []vote{}
	living := livingSeats(data)
	if len(living) == 0 {
		state, _ := finish(data, false)
		return state
	}
	return waiting(data, phaseSpeak, living[0])
}

func beginVote(data *wolfData) rule.State {
	data.Votes = []vote{}
	living := livingSeats(data)
	if len(living) == 0 {
		state, _ := finish(data, false)
		return state
	}
	return waiting(data, phaseVote, living[0])
}

func castVote(data *wolfData, seat, target int) (rule.State, []rule.Event, error) {
	data.Votes = append(data.Votes, vote{Seat: seat, TargetSeat: target})
	ev := rule.Event{Type: actVote, Scope: rule.ScopePublic, Payload: vote{Seat: seat, TargetSeat: target}}
	if next, ok := nextAfter(livingSeats(data), seat); ok {
		return waiting(data, phaseVote, next), []rule.Event{ev}, nil
	}
	state, events, err := finishVote(data)
	if err != nil {
		return state, nil, err
	}
	return state, append([]rule.Event{ev}, events...), nil
}

func finishVote(data *wolfData) (rule.State, []rule.Event, error) {
	out := tally(data.Votes)
	events := []rule.Event{{
		Type: "VoteResult", Scope: rule.ScopePublic, Payload: map[string]any{"Out": out},
	}}
	if out > 0 {
		kill(data, out)
	}
	if done, wolfWin := outcome(data); done {
		state, ev := finish(data, wolfWin)
		return state, append(events, ev), nil
	}
	state, extra, err := beginNight(data)
	if err != nil {
		return state, nil, err
	}
	return state, append(events, extra...), nil
}

func beginNight(data *wolfData) (rule.State, []rule.Event, error) {
	data.Knives = []knifeVote{}
	data.Knifed = 0
	data.Saved = false
	data.Poisoned = 0
	data.Speeches = []speech{}
	data.Votes = []vote{}
	if done, wolfWin := outcome(data); done {
		state, ev := finish(data, wolfWin)
		return state, []rule.Event{ev}, nil
	}
	wolves := livingWolves(data)
	return waiting(data, phaseWolf, wolves[0]), nil, nil
}

func finish(data *wolfData, wolfWin bool) (rule.State, rule.Event) {
	winners := make([]int, 0, len(data.Seats))
	for _, p := range data.Players {
		if (p.Role == roleWolf) == wolfWin {
			winners = append(winners, p.Seat)
		}
	}
	rows := scores(data.Seats, winners, func(seat int) any {
		return identity{Role: roleOf(data, seat)}
	})
	ev := rule.Event{Type: rule.EventSettle, Scope: rule.ScopePublic, Payload: map[string]any{"Scores": rows}}
	return rule.State{Phase: phaseEnd, Data: rule.MustJSON(*data), Settled: true}, ev
}

func waiting(data *wolfData, phase string, turn int) rule.State {
	data.Turn = turn
	return rule.State{Phase: phase, Data: rule.MustJSON(*data), Deadline: rule.After(rule.TurnTimeout)}
}

func buildPrivate(data *wolfData, seat int) any {
	role := roleOf(data, seat)
	switch role {
	case roleWolf:
		others := []int{}
		for _, p := range data.Players {
			if p.Role == roleWolf && p.Seat != seat {
				others = append(others, p.Seat)
			}
		}
		sortInts(others)
		return struct {
			Role   string `json:"Role"`
			Wolves []int  `json:"Wolves"`
		}{Role: roleWolf, Wolves: others}
	case roleSeer:
		checks := data.Checks
		if checks == nil {
			checks = []check{}
		}
		return struct {
			Role   string  `json:"Role"`
			Checks []check `json:"Checks"`
		}{Role: roleSeer, Checks: checks}
	case roleWitch:
		return struct {
			Role    string     `json:"Role"`
			Potions potionView `json:"Potions"`
		}{Role: roleWitch, Potions: potionView{SaveLeft: data.SaveLeft, PoisonLeft: data.PoisonLeft, Knifed: data.Knifed}}
	case roleVillager:
		return struct {
			Role string `json:"Role"`
		}{Role: roleVillager}
	default:
		return struct{}{}
	}
}

func scores(seats, winners []int, detail func(int) any) []scoreRow {
	rows := make([]scoreRow, 0, len(seats))
	for _, d := range rule.Balance(seats, winners) {
		rows = append(rows, scoreRow{Seat: d.Seat, Delta: d.Delta, Detail: detail(d.Seat)})
	}
	return rows
}

func outcome(data *wolfData) (bool, bool) {
	wolves, others := 0, 0
	for _, p := range data.Players {
		if !p.Alive {
			continue
		}
		if p.Role == roleWolf {
			wolves++
		} else {
			others++
		}
	}
	if wolves == 0 {
		return true, false
	}
	if wolves >= others {
		return true, true
	}
	return false, false
}

func majoritySeat(votes []knifeVote) int {
	counts := map[int]int{}
	for _, v := range votes {
		if v.TargetSeat > 0 {
			counts[v.TargetSeat]++
		}
	}
	best, n := 0, 0
	for seat, c := range counts {
		if c > n || (c == n && seat < best) {
			best, n = seat, c
		}
	}
	return best
}

func tally(votes []vote) int {
	counts := map[int]int{}
	for _, v := range votes {
		if v.TargetSeat > 0 {
			counts[v.TargetSeat]++
		}
	}
	best, n := 0, 0
	tie := false
	for seat, c := range counts {
		if c > n {
			best, n, tie = seat, c, false
		} else if c == n {
			tie = true
		}
	}
	if tie || n == 0 {
		return 0
	}
	return best
}

func livingSeats(data *wolfData) []int {
	out := []int{}
	for _, p := range data.Players {
		if p.Alive {
			out = append(out, p.Seat)
		}
	}
	sortInts(out)
	return out
}

func deadSeats(data *wolfData) []int {
	out := []int{}
	for _, p := range data.Players {
		if !p.Alive {
			out = append(out, p.Seat)
		}
	}
	sortInts(out)
	return out
}

func livingWolves(data *wolfData) []int {
	out := []int{}
	for _, p := range data.Players {
		if p.Alive && p.Role == roleWolf {
			out = append(out, p.Seat)
		}
	}
	sortInts(out)
	return out
}

func livingRole(data *wolfData, role string) int {
	for _, p := range data.Players {
		if p.Alive && p.Role == role {
			return p.Seat
		}
	}
	return 0
}

func aliveWolf(data *wolfData, seat int) bool {
	return isAlive(data, seat) && roleOf(data, seat) == roleWolf
}

func isAlive(data *wolfData, seat int) bool {
	for _, p := range data.Players {
		if p.Seat == seat {
			return p.Alive
		}
	}
	return false
}

func roleOf(data *wolfData, seat int) string {
	for _, p := range data.Players {
		if p.Seat == seat {
			return p.Role
		}
	}
	return ""
}

func kill(data *wolfData, seat int) bool {
	for i := range data.Players {
		if data.Players[i].Seat == seat && data.Players[i].Alive {
			data.Players[i].Alive = false
			return true
		}
	}
	return false
}

func nextAfter(seats []int, current int) (int, bool) {
	for i, seat := range seats {
		if seat == current && i+1 < len(seats) {
			return seats[i+1], true
		}
	}
	return 0, false
}

func uniqueSeats(seats []int) bool {
	for i, seat := range seats {
		if seat < 1 {
			return false
		}
		if i > 0 && seats[i] == seats[i-1] {
			return false
		}
	}
	return true
}

func cleanText(raw string) (string, error) {
	text := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(text)
	if n < 1 || n > 40 {
		return "", errcode.ErrBadPayload
	}
	return text, nil
}

func decodePayload(raw json.RawMessage, dest any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return errcode.ErrBadPayload
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return errcode.ErrBadPayload
	}
	return nil
}

func targetSeat(raw json.RawMessage) (int, error) {
	var body struct {
		TargetSeat int `json:"TargetSeat"`
	}
	if err := decodePayload(raw, &body); err != nil {
		return 0, err
	}
	return body.TargetSeat, nil
}

func shuffle(n int, swap func(i, j int)) {
	for i := n - 1; i > 0; i-- {
		j := rule.Intn(i + 1)
		swap(i, j)
	}
}

func sortInts(vals []int) {
	for i := 0; i < len(vals); i++ {
		for j := i + 1; j < len(vals); j++ {
			if vals[j] < vals[i] {
				vals[i], vals[j] = vals[j], vals[i]
			}
		}
	}
}
