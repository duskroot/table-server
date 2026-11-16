// Package spy is the who-is-the-spy drinking rule.
package spy

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"table/internal/errcode"
	"table/internal/rule"
)

const (
	phaseDescribe = "describe"
	phaseVote     = "vote"
	phaseEnd      = "end"

	roleCivilian = "civilian"
	roleSpy      = "spy"
	roleBlank    = "blank"

	actSpeak = "Speak"
	actVote  = "Vote"
)

var wordPairs = [][2]string{
	{"牛奶", "豆浆"},
	{"苹果", "香蕉"},
	{"微信", "支付宝"},
}

type game struct{}

func init() { rule.Register(game{}) }

func (game) Code() string  { return "spy" }
func (game) MinSeats() int { return 4 }
func (game) MaxSeats() int { return 10 }

type spyConfig struct {
	CivilianWord string `json:"CivilianWord"`
	SpyWord      string `json:"SpyWord"`
	SpyCount     *int   `json:"SpyCount"`
	BlankCount   *int   `json:"BlankCount"`
}

type spyPlayer struct {
	Seat int    `json:"Seat"`
	Role string `json:"Role"`
	Word string `json:"Word"`
}

type spySpeech struct {
	Seat int    `json:"Seat"`
	Text string `json:"Text"`
}

type spyVote struct {
	Seat       int `json:"Seat"`
	TargetSeat int `json:"TargetSeat"`
}

type spyData struct {
	Seats    []int       `json:"Seats"`
	Players  []spyPlayer `json:"Players"`
	Turn     int         `json:"Turn"`
	Speeches []spySpeech `json:"Speeches"`
	Votes    []spyVote   `json:"Votes"`
}

type spyPublic struct {
	Turn     int         `json:"Turn"`
	Speeches []spySpeech `json:"Speeches"`
	Votes    []spyVote   `json:"Votes"`
}

type spyPrivate struct {
	Role string `json:"Role"`
	Word string `json:"Word"`
}

type identity struct {
	Role string `json:"Role"`
	Word string `json:"Word"`
}

type scoreRow struct {
	Seat   int `json:"Seat"`
	Delta  int `json:"Delta"`
	Detail any `json:"Detail,omitempty"`
}

func (game) Start(ctx rule.StartContext) (rule.State, []rule.Event, error) {
	seats := append([]int(nil), ctx.Seats...)
	sortInts(seats)
	if len(seats) < 4 || len(seats) > 10 || !uniqueSeats(seats) {
		return rule.State{}, nil, errcode.ErrBadPayload
	}
	var cfg spyConfig
	if err := rule.Decode(ctx.Config, &cfg); err != nil {
		return rule.State{}, nil, errcode.ErrBadPayload
	}
	spyN, blankN := 1, 0
	if cfg.SpyCount != nil {
		spyN = *cfg.SpyCount
	}
	if cfg.BlankCount != nil {
		blankN = *cfg.BlankCount
	}
	if spyN < 0 || blankN < 0 {
		return rule.State{}, nil, errcode.ErrBadPayload
	}
	civilians := len(seats) - spyN - blankN
	if spyN+blankN >= len(seats) || civilians < 2 {
		return rule.State{}, nil, errcode.ErrBadPayload
	}
	civilianWord := strings.TrimSpace(cfg.CivilianWord)
	spyWord := strings.TrimSpace(cfg.SpyWord)
	if civilianWord == "" && spyWord == "" {
		pair := wordPairs[rule.Intn(len(wordPairs))]
		civilianWord, spyWord = pair[0], pair[1]
	} else if civilianWord == "" || spyWord == "" {
		return rule.State{}, nil, errcode.ErrBadPayload
	}
	roles := make([]string, 0, len(seats))
	for i := 0; i < spyN; i++ {
		roles = append(roles, roleSpy)
	}
	for i := 0; i < blankN; i++ {
		roles = append(roles, roleBlank)
	}
	for len(roles) < len(seats) {
		roles = append(roles, roleCivilian)
	}
	shuffle(len(roles), func(i, j int) { roles[i], roles[j] = roles[j], roles[i] })
	players := make([]spyPlayer, len(seats))
	for i, seat := range seats {
		word := civilianWord
		switch roles[i] {
		case roleSpy:
			word = spyWord
		case roleBlank:
			word = ""
		}
		players[i] = spyPlayer{Seat: seat, Role: roles[i], Word: word}
	}
	data := spyData{
		Seats: seats, Players: players, Turn: seats[0],
		Speeches: []spySpeech{}, Votes: []spyVote{},
	}
	return waiting(&data, phaseDescribe, seats[0]), []rule.Event{{
		Type: "Start", Scope: rule.ScopePublic, Payload: map[string]any{"Seats": seats},
	}}, nil
}

func (g game) Apply(state rule.State, seat int, action rule.Action, _ time.Time) (rule.State, []rule.Event, error) {
	if state.Settled {
		return state, nil, errcode.ErrBadAction
	}
	var data spyData
	if err := rule.Decode(state.Data, &data); err != nil {
		return state, nil, err
	}
	switch state.Phase {
	case phaseDescribe:
		if action.Type != actSpeak {
			return state, nil, errcode.ErrBadAction
		}
		if seat != data.Turn {
			return state, nil, errcode.ErrNotYourTurn
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
		return speak(&data, seat, text)
	case phaseVote:
		if action.Type != actVote {
			return state, nil, errcode.ErrBadAction
		}
		if seat != data.Turn {
			return state, nil, errcode.ErrNotYourTurn
		}
		target, err := targetSeat(action.Payload)
		if err != nil {
			return state, nil, err
		}
		if target == seat || !contains(data.Seats, target) {
			return state, nil, errcode.ErrBadPayload
		}
		return vote(&data, seat, target)
	default:
		return state, nil, errcode.ErrBadAction
	}
}

func (game) View(state rule.State, seat int) (rule.Snapshot, error) {
	var data spyData
	if err := rule.Decode(state.Data, &data); err != nil {
		return rule.Snapshot{}, err
	}
	if data.Speeches == nil {
		data.Speeches = []spySpeech{}
	}
	if data.Votes == nil {
		data.Votes = []spyVote{}
	}
	priv := spyPrivate{}
	for _, p := range data.Players {
		if p.Seat == seat {
			priv = spyPrivate{Role: p.Role, Word: p.Word}
			break
		}
	}
	legal := []rule.ActionSpec{}
	if !state.Settled && seat == data.Turn {
		switch state.Phase {
		case phaseDescribe:
			legal = []rule.ActionSpec{{Type: actSpeak, Hint: "描述你的词语"}}
		case phaseVote:
			legal = []rule.ActionSpec{{Type: actVote, Hint: "投票"}}
		}
	}
	return rule.Snapshot{
		Phase:    state.Phase,
		Public:   rule.MustJSON(spyPublic{Turn: data.Turn, Speeches: data.Speeches, Votes: data.Votes}),
		Private:  rule.MustJSON(priv),
		Settled:  state.Settled,
		Deadline: state.Deadline,
		Legal:    legal,
	}, nil
}

func (g game) OnTimeout(state rule.State, now time.Time) (rule.State, []rule.Event, error) {
	if state.Settled {
		return state, nil, nil
	}
	var data spyData
	if err := rule.Decode(state.Data, &data); err != nil {
		return state, nil, err
	}
	switch state.Phase {
	case phaseDescribe:
		return g.Apply(state, data.Turn, rule.Action{
			Type: actSpeak, Payload: rule.MustJSON(map[string]string{"Text": "……"}),
		}, now)
	case phaseVote:
		return g.Apply(state, data.Turn, rule.Action{
			Type: actVote, Payload: rule.MustJSON(map[string]int{"TargetSeat": nextOther(data.Seats, data.Turn)}),
		}, now)
	default:
		return state, nil, nil
	}
}

func speak(data *spyData, seat int, text string) (rule.State, []rule.Event, error) {
	data.Speeches = append(data.Speeches, spySpeech{Seat: seat, Text: text})
	ev := rule.Event{Type: actSpeak, Scope: rule.ScopePublic, Payload: spySpeech{Seat: seat, Text: text}}
	if next, ok := nextAfter(data.Seats, seat); ok {
		return waiting(data, phaseDescribe, next), []rule.Event{ev}, nil
	}
	data.Votes = []spyVote{}
	return waiting(data, phaseVote, data.Seats[0]), []rule.Event{ev}, nil
}

func vote(data *spyData, seat, target int) (rule.State, []rule.Event, error) {
	data.Votes = append(data.Votes, spyVote{Seat: seat, TargetSeat: target})
	ev := rule.Event{Type: actVote, Scope: rule.ScopePublic, Payload: spyVote{Seat: seat, TargetSeat: target}}
	if next, ok := nextAfter(data.Seats, seat); ok {
		return waiting(data, phaseVote, next), []rule.Event{ev}, nil
	}
	return settle(data, ev)
}

func settle(data *spyData, prior rule.Event) (rule.State, []rule.Event, error) {
	counts := map[int]int{}
	for _, v := range data.Votes {
		counts[v.TargetSeat]++
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
	spyWin := true
	if !tie && n > 0 && roleOf(data, best) == roleSpy {
		spyWin = false
	}
	winners := make([]int, 0, len(data.Seats))
	for _, p := range data.Players {
		if spyWin && p.Role == roleSpy {
			winners = append(winners, p.Seat)
		}
		if !spyWin && p.Role != roleSpy {
			winners = append(winners, p.Seat)
		}
	}
	rows := scores(data.Seats, winners, func(seat int) any {
		for _, p := range data.Players {
			if p.Seat == seat {
				return identity{Role: p.Role, Word: p.Word}
			}
		}
		return nil
	})
	ev := rule.Event{Type: rule.EventSettle, Scope: rule.ScopePublic, Payload: map[string]any{"Scores": rows}}
	return rule.State{Phase: phaseEnd, Data: rule.MustJSON(*data), Settled: true}, []rule.Event{prior, ev}, nil
}

func waiting(data *spyData, phase string, turn int) rule.State {
	data.Turn = turn
	return rule.State{Phase: phase, Data: rule.MustJSON(*data), Deadline: rule.After(rule.TurnTimeout)}
}

func scores(seats, winners []int, detail func(int) any) []scoreRow {
	rows := make([]scoreRow, 0, len(seats))
	for _, d := range rule.Balance(seats, winners) {
		rows = append(rows, scoreRow{Seat: d.Seat, Delta: d.Delta, Detail: detail(d.Seat)})
	}
	return rows
}

func roleOf(data *spyData, seat int) string {
	for _, p := range data.Players {
		if p.Seat == seat {
			return p.Role
		}
	}
	return ""
}

func nextAfter(seats []int, current int) (int, bool) {
	for i, seat := range seats {
		if seat == current && i+1 < len(seats) {
			return seats[i+1], true
		}
	}
	return 0, false
}

func nextOther(seats []int, self int) int {
	if len(seats) == 0 {
		return 0
	}
	idx := 0
	for i, seat := range seats {
		if seat == self {
			idx = i
			break
		}
	}
	for step := 1; step <= len(seats); step++ {
		seat := seats[(idx+step)%len(seats)]
		if seat != self {
			return seat
		}
	}
	return seats[0]
}

func contains(seats []int, seat int) bool {
	for _, item := range seats {
		if item == seat {
			return true
		}
	}
	return false
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
