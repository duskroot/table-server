package spy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"table/internal/errcode"
	"table/internal/rule"
)

func TestRegistered(t *testing.T) {
	g, ok := rule.Get("spy")
	if !ok || g.Code() != "spy" || g.MinSeats() != 4 || g.MaxSeats() != 10 {
		t.Fatalf("registry %+v %v", g, ok)
	}
}

func TestCivilianViewHidesSpyWord(t *testing.T) {
	g := game{}
	state, events, err := g.Start(rule.StartContext{
		Seats:  []int{4, 2, 1, 3},
		Config: rule.MustJSON(map[string]any{"CivilianWord": "牛奶", "SpyWord": "豆浆", "SpyCount": 1, "BlankCount": 1}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if state.Phase != phaseDescribe || state.Deadline == nil {
		t.Fatalf("start %+v", state.Phase)
	}
	for _, ev := range events {
		raw, _ := json.Marshal(ev.Payload)
		if ev.Scope != rule.ScopePublic || strings.Contains(string(raw), "豆浆") || strings.Contains(string(raw), "牛奶") {
			t.Fatalf("start event %+v %s", ev, raw)
		}
	}
	spies, blanks, civilians := 0, 0, 0
	for _, seat := range []int{1, 2, 3, 4} {
		view, err := g.View(state, seat)
		if err != nil {
			t.Fatal(err)
		}
		pub := string(view.Public)
		if strings.Contains(pub, "豆浆") || strings.Contains(pub, "牛奶") || strings.Contains(pub, "Word") || strings.Contains(pub, "Role") {
			t.Fatalf("public leak seat %d %s", seat, pub)
		}
		var priv spyPrivate
		if err := json.Unmarshal(view.Private, &priv); err != nil {
			t.Fatal(err)
		}
		switch priv.Role {
		case roleCivilian:
			civilians++
			if priv.Word != "牛奶" || strings.Contains(string(view.Private), "豆浆") {
				t.Fatalf("civilian %+v", priv)
			}
		case roleSpy:
			spies++
			if priv.Word != "豆浆" {
				t.Fatalf("spy %+v", priv)
			}
		case roleBlank:
			blanks++
			if priv.Word != "" || strings.Contains(string(view.Private), "豆浆") || strings.Contains(string(view.Private), "牛奶") {
				t.Fatalf("blank %+v", priv)
			}
		default:
			t.Fatalf("role %s", priv.Role)
		}
	}
	if spies != 1 || blanks != 1 || civilians != 2 {
		t.Fatalf("roles spy %d blank %d civilian %d", spies, blanks, civilians)
	}
}

func TestDefaultWords(t *testing.T) {
	g := game{}
	state, _, err := g.Start(rule.StartContext{Seats: []int{1, 2, 3, 4}, Config: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	mate := map[string]string{"牛奶": "豆浆", "苹果": "香蕉", "微信": "支付宝"}
	var civilianWord, spyWord string
	spies := 0
	for _, seat := range []int{1, 2, 3, 4} {
		view, err := g.View(state, seat)
		if err != nil {
			t.Fatal(err)
		}
		var priv spyPrivate
		if err := json.Unmarshal(view.Private, &priv); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(view.Public), priv.Word) && priv.Word != "" {
			t.Fatalf("word leaked %s", view.Public)
		}
		switch priv.Role {
		case roleSpy:
			spies++
			spyWord = priv.Word
		case roleCivilian:
			if civilianWord == "" {
				civilianWord = priv.Word
			} else if priv.Word != civilianWord {
				t.Fatalf("civilian words %s %s", civilianWord, priv.Word)
			}
			if strings.Contains(string(view.Private), spyWord) && spyWord != "" && spyWord != priv.Word {
				t.Fatal(view.Private)
			}
		default:
			t.Fatalf("role %s", priv.Role)
		}
	}
	if spies != 1 || mate[civilianWord] != spyWord {
		t.Fatalf("words %s/%s spies %d", civilianWord, spyWord, spies)
	}
}

func TestVoteOutSpyCiviliansWin(t *testing.T) {
	g := game{}
	data := spyData{
		Seats: []int{1, 2, 3, 4},
		Players: []spyPlayer{
			{Seat: 1, Role: roleCivilian, Word: "牛奶"},
			{Seat: 2, Role: roleBlank, Word: ""},
			{Seat: 3, Role: roleCivilian, Word: "牛奶"},
			{Seat: 4, Role: roleSpy, Word: "豆浆"},
		},
		Turn: 1, Speeches: []spySpeech{}, Votes: []spyVote{},
	}
	state := rule.State{Phase: phaseVote, Data: rule.MustJSON(data), Deadline: rule.After(rule.TurnTimeout)}
	view, err := g.View(state, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(view.Public), "豆浆") || strings.Contains(string(view.Private), "豆浆") {
		t.Fatalf("civilian saw spy word %s %s", view.Public, view.Private)
	}
	targets := []int{4, 4, 4, 1}
	var events []rule.Event
	for i, seat := range []int{1, 2, 3, 4} {
		state, events, err = g.Apply(state, seat, rule.Action{
			Type: actVote, Payload: rule.MustJSON(map[string]int{"TargetSeat": targets[i]}),
		}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if i < 3 && state.Settled {
			t.Fatal("settled early")
		}
	}
	if !state.Settled || state.Deadline != nil || state.Phase != phaseEnd {
		t.Fatalf("end %+v", state)
	}
	after, err := g.View(state, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after.Public)+string(after.Private), "豆浆") {
		t.Fatal("spy word visible after settle view")
	}
	assertBalance(t, events, []int{1, 2, 3, 4}, []int{1, 2, 3})
	for _, ev := range events {
		if ev.Type == rule.EventSettle {
			continue
		}
		raw, _ := json.Marshal(ev.Payload)
		if strings.Contains(string(raw), "豆浆") || strings.Contains(string(raw), "牛奶") {
			t.Fatal(string(raw))
		}
	}
}

func TestSpyTimeoutAndBadActions(t *testing.T) {
	g := game{}
	data := spyData{
		Seats: []int{1, 2, 3, 4},
		Players: []spyPlayer{
			{Seat: 1, Role: roleCivilian, Word: "牛奶"},
			{Seat: 2, Role: roleCivilian, Word: "牛奶"},
			{Seat: 3, Role: roleCivilian, Word: "牛奶"},
			{Seat: 4, Role: roleSpy, Word: "豆浆"},
		},
		Turn: 1, Speeches: []spySpeech{}, Votes: []spyVote{},
	}
	state := rule.State{Phase: phaseDescribe, Data: rule.MustJSON(data), Deadline: rule.After(rule.TurnTimeout)}
	_, _, err := g.Apply(state, 2, rule.Action{Type: actSpeak, Payload: rule.MustJSON(map[string]string{"Text": "圆的"})}, time.Now())
	if !errors.Is(err, errcode.ErrNotYourTurn) {
		t.Fatal(err)
	}
	_, _, err = g.Apply(state, 1, rule.Action{Type: actVote, Payload: rule.MustJSON(map[string]int{"TargetSeat": 2})}, time.Now())
	if !errors.Is(err, errcode.ErrBadAction) {
		t.Fatal(err)
	}
	_, _, err = g.Apply(state, 1, rule.Action{Type: actSpeak, Payload: rule.MustJSON(map[string]string{"Text": "   "})}, time.Now())
	if !errors.Is(err, errcode.ErrBadPayload) {
		t.Fatal(err)
	}
	_, _, err = g.Apply(state, 1, rule.Action{Type: actSpeak, Payload: rule.MustJSON(map[string]string{"Text": strings.Repeat("字", 41)})}, time.Now())
	if !errors.Is(err, errcode.ErrBadPayload) {
		t.Fatal(err)
	}
	state, _, err = g.Apply(state, 1, rule.Action{Type: actSpeak, Payload: rule.MustJSON(map[string]string{"Text": strings.Repeat("字", 40)})}, time.Now())
	if err != nil || state.Phase != phaseDescribe {
		t.Fatal(err, state.Phase)
	}
	state = rule.State{Phase: phaseDescribe, Data: rule.MustJSON(data), Deadline: rule.After(rule.TurnTimeout)}
	state, _, err = g.OnTimeout(state, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var spoken spyData
	if err := rule.Decode(state.Data, &spoken); err != nil {
		t.Fatal(err)
	}
	if spoken.Turn != 2 || len(spoken.Speeches) != 1 || spoken.Speeches[0].Text != "……" {
		t.Fatalf("%+v", spoken)
	}

	voteState := rule.State{Phase: phaseVote, Data: rule.MustJSON(data), Deadline: rule.After(rule.TurnTimeout)}
	_, _, err = g.Apply(voteState, 1, rule.Action{Type: actVote, Payload: rule.MustJSON(map[string]int{"TargetSeat": 1})}, time.Now())
	if !errors.Is(err, errcode.ErrBadPayload) {
		t.Fatal(err)
	}
	_, _, err = g.Apply(voteState, 1, rule.Action{Type: actVote, Payload: rule.MustJSON(map[string]int{"TargetSeat": 9})}, time.Now())
	if !errors.Is(err, errcode.ErrBadPayload) {
		t.Fatal(err)
	}
	var events []rule.Event
	state = voteState
	for i := 0; i < 4; i++ {
		state, events, err = g.OnTimeout(state, time.Now())
		if err != nil {
			t.Fatal(err)
		}
	}
	if !state.Settled {
		t.Fatal("timeout did not finish")
	}
	var final spyData
	if err := rule.Decode(state.Data, &final); err != nil {
		t.Fatal(err)
	}
	want := map[int]int{1: 2, 2: 3, 3: 4, 4: 1}
	if len(final.Votes) != 4 {
		t.Fatal(final.Votes)
	}
	for _, v := range final.Votes {
		if want[v.Seat] != v.TargetSeat {
			t.Fatalf("vote %+v", v)
		}
	}
	assertBalance(t, events, []int{1, 2, 3, 4}, []int{4})
}

func TestSpyConfigRejected(t *testing.T) {
	g := game{}
	cases := []rule.StartContext{
		{Seats: []int{1, 2, 3}},
		{Seats: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}},
		{Seats: []int{1, 2, 3, 4}, Config: rule.MustJSON(map[string]any{"SpyCount": 3})},
		{Seats: []int{1, 2, 3, 4}, Config: rule.MustJSON(map[string]any{"SpyCount": 2, "BlankCount": 1})},
		{Seats: []int{1, 2, 3, 4}, Config: rule.MustJSON(map[string]any{"CivilianWord": "牛奶"})},
		{Seats: []int{1, 1, 2, 3}},
	}
	for _, ctx := range cases {
		_, _, err := g.Start(ctx)
		if !errors.Is(err, errcode.ErrBadPayload) {
			t.Fatalf("%+v err %v", ctx.Seats, err)
		}
	}
}

func assertBalance(t *testing.T, events []rule.Event, seats, winners []int) {
	t.Helper()
	got := settleDeltas(t, events)
	if len(got) != len(seats) {
		t.Fatalf("coverage %+v", got)
	}
	for _, d := range rule.Balance(seats, winners) {
		if got[d.Seat] != d.Delta {
			t.Fatalf("seat %d got %d want %d (%+v)", d.Seat, got[d.Seat], d.Delta, got)
		}
	}
}

func settleDeltas(t *testing.T, events []rule.Event) map[int]int {
	t.Helper()
	var found *rule.Event
	n := 0
	for i := range events {
		if events[i].Type != rule.EventSettle {
			continue
		}
		n++
		if events[i].Scope != rule.ScopePublic {
			t.Fatalf("scope %d", events[i].Scope)
		}
		found = &events[i]
	}
	if n != 1 || found == nil {
		t.Fatalf("settle count %d", n)
	}
	raw, err := json.Marshal(found.Payload)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Scores []struct {
			Seat  int `json:"Seat"`
			Delta int `json:"Delta"`
		} `json:"Scores"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	out := map[int]int{}
	sum := 0
	for _, row := range body.Scores {
		if _, ok := out[row.Seat]; ok {
			t.Fatalf("dup %d", row.Seat)
		}
		out[row.Seat] = row.Delta
		sum += row.Delta
	}
	if sum != 0 {
		t.Fatalf("sum %d %+v", sum, out)
	}
	return out
}
