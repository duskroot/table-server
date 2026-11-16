package httpx

import (
	"encoding/json"
	"strconv"
	"time"

	"table/internal/errcode"
	"table/internal/rule"
)

func init() {
	rule.Register(fakeRule{})
}

type fakeRule struct{}

func (fakeRule) Code() string  { return "fake" }
func (fakeRule) MinSeats() int { return 2 }
func (fakeRule) MaxSeats() int { return 4 }

type fakeData struct {
	Turn  int   `json:"Turn"`
	Seats []int `json:"Seats"`
}

func (fakeRule) Start(ctx rule.StartContext) (rule.State, []rule.Event, error) {
	data := fakeData{Turn: ctx.Seats[0], Seats: append([]int(nil), ctx.Seats...)}
	return rule.State{
		Phase: "tap", Data: rule.MustJSON(data), Deadline: rule.After(rule.TurnTimeout),
	}, []rule.Event{{Type: "Started", Scope: rule.ScopePublic, Payload: map[string]any{"Seats": ctx.Seats}}}, nil
}

func (fakeRule) Apply(state rule.State, seat int, action rule.Action, _ time.Time) (rule.State, []rule.Event, error) {
	var data fakeData
	if err := rule.Decode(state.Data, &data); err != nil {
		return state, nil, err
	}
	if action.Type != "Tap" {
		return state, nil, errcode.ErrBadAction
	}
	if seat != data.Turn {
		return state, nil, errcode.ErrNotYourTurn
	}
	scores := make([]map[string]any, 0, len(data.Seats))
	for _, item := range data.Seats {
		delta := -1
		if item == seat {
			delta = len(data.Seats) - 1
		}
		scores = append(scores, map[string]any{"Seat": item, "Delta": delta})
	}
	state.Phase = "end"
	state.Settled = true
	state.Deadline = nil
	return state, []rule.Event{{Type: rule.EventSettle, Scope: rule.ScopePublic, Payload: map[string]any{"Scores": scores}}}, nil
}

func (fakeRule) View(state rule.State, seat int) (rule.Snapshot, error) {
	var data fakeData
	_ = rule.Decode(state.Data, &data)
	legal := []rule.ActionSpec{}
	if !state.Settled && seat == data.Turn {
		legal = []rule.ActionSpec{{Type: "Tap", Hint: "拍一下"}}
	}
	return rule.Snapshot{
		Phase: state.Phase, Public: state.Data, Private: json.RawMessage(`{"Seat":` + strconv.Itoa(seat) + `}`),
		Settled: state.Settled, Deadline: state.Deadline, Legal: legal,
	}, nil
}

func (f fakeRule) OnTimeout(state rule.State, now time.Time) (rule.State, []rule.Event, error) {
	var data fakeData
	if err := rule.Decode(state.Data, &data); err != nil {
		return state, nil, err
	}
	return f.Apply(state, data.Turn, rule.Action{Type: "Tap"}, now)
}
