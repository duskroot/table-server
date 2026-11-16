package wolf

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"table/internal/errcode"
	"table/internal/rule"
)

func TestRegistered(t *testing.T) {
	g, ok := rule.Get("wolf")
	if !ok || g.Code() != "wolf" || g.MinSeats() != 6 || g.MaxSeats() != 12 {
		t.Fatalf("registry %+v %v", g, ok)
	}
}

func TestVillagerCannotSeeWolvesButWolfSeesMate(t *testing.T) {
	g := game{}
	data := sample(variantWitch)
	data.Turn = 1
	state := rule.State{Phase: phaseWolf, Data: rule.MustJSON(data), Deadline: rule.After(rule.TurnTimeout)}
	villager, err := g.View(state, 4)
	if err != nil {
		t.Fatal(err)
	}
	if string(villager.Private) != `{"Role":"villager"}` {
		t.Fatalf("villager private %s", villager.Private)
	}
	for _, wolfSeat := range []int{1, 2} {
		if bytes.Contains(villager.Private, []byte(strconv.Itoa(wolfSeat))) {
			t.Fatalf("villager saw wolf seat %d in %s", wolfSeat, villager.Private)
		}
	}
	assertNoRoleLeak(t, villager.Public)
	if villager.Phase != phaseWolf {
		t.Fatal(villager.Phase)
	}
	var pub struct {
		Turn int `json:"Turn"`
	}
	if err := json.Unmarshal(villager.Public, &pub); err != nil {
		t.Fatal(err)
	}
	if pub.Turn != 0 {
		t.Fatalf("night turn leaked %d", pub.Turn)
	}
	wolfView, err := g.View(state, 1)
	if err != nil {
		t.Fatal(err)
	}
	var wolfPriv struct {
		Role   string `json:"Role"`
		Wolves []int  `json:"Wolves"`
	}
	if err := json.Unmarshal(wolfView.Private, &wolfPriv); err != nil {
		t.Fatal(err)
	}
	if wolfPriv.Role != roleWolf || len(wolfPriv.Wolves) != 1 || wolfPriv.Wolves[0] != 2 {
		t.Fatalf("%+v", wolfPriv)
	}
	seer, err := g.View(state, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(seer.Private, []byte(`"Checks"`)) || bytes.Contains(seer.Private, []byte(`"Wolves"`)) {
		t.Fatal(string(seer.Private))
	}
	witch, err := g.View(state, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(witch.Private, []byte(`"SaveLeft":true`)) || !bytes.Contains(witch.Private, []byte(`"PoisonLeft":true`)) {
		t.Fatal(string(witch.Private))
	}
	if len(wolfView.Legal) != 1 || wolfView.Legal[0].Type != actKnife || len(villager.Legal) != 0 {
		t.Fatalf("legal wolf %v villager %v", wolfView.Legal, villager.Legal)
	}
}

func TestRoster(t *testing.T) {
	g := game{}
	classic, _, err := g.Start(rule.StartContext{Variant: variantClassic, Seats: []int{1, 2, 3, 4, 5, 6}})
	if err != nil {
		t.Fatal(err)
	}
	counts := countRoles(t, g, classic, 6)
	if counts[roleWolf] != 2 || counts[roleSeer] != 1 || counts[roleVillager] != 3 || counts[roleWitch] != 0 {
		t.Fatal(counts)
	}
	witch, _, err := g.Start(rule.StartContext{Variant: variantWitch, Seats: []int{1, 2, 3, 4, 5, 6}})
	if err != nil {
		t.Fatal(err)
	}
	counts = countRoles(t, g, witch, 6)
	if counts[roleWolf] != 2 || counts[roleSeer] != 1 || counts[roleWitch] != 1 || counts[roleVillager] != 2 {
		t.Fatal(counts)
	}
	for _, ctx := range []rule.StartContext{
		{Variant: variantClassic, Seats: []int{1, 2, 3, 4, 5}},
		{Variant: variantClassic, Seats: []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}},
		{Variant: "bad", Seats: []int{1, 2, 3, 4, 5, 6}},
	} {
		if _, _, err := g.Start(ctx); !errors.Is(err, errcode.ErrBadPayload) {
			t.Fatal(err)
		}
	}
}

func TestWolvesKnifeVillagersToWin(t *testing.T) {
	g := game{}
	data := sample(variantClassic)
	data.Turn = 1
	data.Players[2].Alive = false
	state := rule.State{Phase: phaseWolf, Data: rule.MustJSON(data), Deadline: rule.After(rule.TurnTimeout)}
	_, _, err := g.Apply(state, 1, knife(3), time.Now())
	if !errors.Is(err, errcode.ErrBadPayload) {
		t.Fatal(err)
	}
	state, events, err := g.Apply(state, 1, knife(4), time.Now())
	if err != nil || state.Settled || state.Phase != phaseWolf {
		t.Fatal(err, state.Phase, state.Settled)
	}
	var mid wolfData
	if err := rule.Decode(state.Data, &mid); err != nil {
		t.Fatal(err)
	}
	if mid.Turn != 2 {
		t.Fatal(mid.Turn)
	}
	state, events, err = g.Apply(state, 2, knife(4), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !state.Settled || state.Phase != phaseEnd || state.Deadline != nil {
		t.Fatalf("end phase %s settled %v", state.Phase, state.Settled)
	}
	var dead struct {
		Dead []int `json:"Dead"`
	}
	sawDawn := false
	for _, ev := range events {
		if ev.Type != "Dawn" {
			continue
		}
		sawDawn = true
		if ev.Scope != rule.ScopePublic {
			t.Fatal(ev.Scope)
		}
		raw, _ := json.Marshal(ev.Payload)
		if bytes.Contains(raw, []byte("Role")) || bytes.Contains(raw, []byte("wolf")) {
			t.Fatal(string(raw))
		}
		if err := json.Unmarshal(raw, &dead); err != nil {
			t.Fatal(err)
		}
	}
	if !sawDawn || len(dead.Dead) != 1 || dead.Dead[0] != 4 {
		t.Fatalf("dawn %+v", dead.Dead)
	}
	assertBalance(t, events, []int{1, 2, 3, 4, 5, 6}, []int{1, 2})
	view, err := g.View(state, 5)
	if err != nil {
		t.Fatal(err)
	}
	if string(view.Private) != `{"Role":"villager"}` || bytes.Contains(view.Public, []byte(`"Role"`)) || bytes.Contains(view.Public, []byte(`"Wolves"`)) {
		t.Fatalf("public %s private %s", view.Public, view.Private)
	}
}

func TestKnifeTieBreaksToSmallestSeat(t *testing.T) {
	g := game{}
	data := sample(variantClassic)
	data.Turn = 1
	data.Players[2].Alive = false
	state := rule.State{Phase: phaseWolf, Data: rule.MustJSON(data), Deadline: rule.After(rule.TurnTimeout)}
	state, _, err := g.Apply(state, 1, knife(5), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	state, events, err := g.Apply(state, 2, knife(4), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var dead struct {
		Dead []int `json:"Dead"`
	}
	for _, ev := range events {
		if ev.Type == "Dawn" {
			raw, _ := json.Marshal(ev.Payload)
			_ = json.Unmarshal(raw, &dead)
		}
	}
	if len(dead.Dead) != 1 || dead.Dead[0] != 4 {
		t.Fatalf("dead %+v", dead.Dead)
	}
	if !state.Settled {
		t.Fatal("expected wolf win")
	}
	assertBalance(t, events, []int{1, 2, 3, 4, 5, 6}, []int{1, 2})
}

func TestSeerCheckScopeIsPrivate(t *testing.T) {
	g := game{}
	data := sample(variantClassic)
	data.Turn = 3
	state := rule.State{Phase: phaseSeer, Data: rule.MustJSON(data), Deadline: rule.After(rule.TurnTimeout)}
	state, events, err := g.Apply(state, 3, rule.Action{
		Type: actCheck, Payload: rule.MustJSON(map[string]int{"TargetSeat": 1}),
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var checkEv *rule.Event
	for i := range events {
		raw, _ := json.Marshal(events[i].Payload)
		if events[i].Type == actCheck {
			checkEv = &events[i]
		}
		if events[i].Scope == rule.ScopePublic && bytes.Contains(raw, []byte(`"Wolf"`)) {
			t.Fatalf("public check %s", raw)
		}
	}
	if checkEv == nil || checkEv.Scope != 3 || checkEv.Scope == rule.ScopePublic {
		t.Fatalf("check event %+v", checkEv)
	}
	raw, _ := json.Marshal(checkEv.Payload)
	var body check
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if body.TargetSeat != 1 || !body.Wolf {
		t.Fatalf("%+v", body)
	}
	seer, err := g.View(state, 3)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(seer.Private, []byte(`"Wolf":true`)) {
		t.Fatal(string(seer.Private))
	}
	villager, err := g.View(state, 4)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(villager.Private, []byte("Wolf")) || bytes.Contains(villager.Public, []byte("Wolf")) || bytes.Contains(villager.Public, []byte(`"Role"`)) {
		t.Fatalf("leak pub %s priv %s", villager.Public, villager.Private)
	}
}

func TestNightTimeoutSkips(t *testing.T) {
	g := game{}
	data := sample(variantClassic)
	data.Turn = 1
	state := rule.State{Phase: phaseWolf, Data: rule.MustJSON(data), Deadline: rule.After(rule.TurnTimeout)}
	var err error
	state, _, err = g.OnTimeout(state, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	state, _, err = g.OnTimeout(state, time.Now())
	if err != nil || state.Phase != phaseSeer {
		t.Fatal(err, state.Phase)
	}
	mid := mustData(t, state)
	if mid.Knifed != 0 || mid.Turn != 3 {
		t.Fatalf("%+v", mid)
	}
	for _, p := range mid.Players {
		if !p.Alive {
			t.Fatal("somebody died")
		}
	}
	state, events, err := g.OnTimeout(state, time.Now())
	if err != nil || state.Phase != phaseSpeak {
		t.Fatal(err, state.Phase)
	}
	for _, ev := range events {
		if ev.Type == actCheck || ev.Scope != rule.ScopePublic {
			t.Fatalf("event %+v", ev)
		}
	}
	if alive := mustData(t, state); !alive.Players[0].Alive || alive.Knifed != 0 {
		t.Fatal("night killed")
	}
	state, _, err = g.OnTimeout(state, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	spoken := mustData(t, state)
	if len(spoken.Speeches) != 1 || spoken.Speeches[0].Text != "……" || spoken.Turn != 2 {
		t.Fatalf("%+v", spoken)
	}
}

func TestWitchPotions(t *testing.T) {
	g := game{}
	base := sample(variantWitch)
	base.Turn = 6
	base.Knifed = 4
	state := rule.State{Phase: phaseWitch, Data: rule.MustJSON(base), Deadline: rule.After(rule.TurnTimeout)}
	_, _, err := g.Apply(state, 6, potion(true, 5), time.Now())
	if !errors.Is(err, errcode.ErrBadPayload) {
		t.Fatal(err)
	}
	idle := sample(variantWitch)
	idle.Turn = 6
	idle.Knifed = 0
	idleState := rule.State{Phase: phaseWitch, Data: rule.MustJSON(idle), Deadline: rule.After(rule.TurnTimeout)}
	if _, _, err := g.Apply(idleState, 6, potion(true, -1), time.Now()); !errors.Is(err, errcode.ErrBadPayload) {
		t.Fatal(err)
	}
	saved, _, err := g.Apply(state, 6, potion(true, -1), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	got := mustData(t, saved)
	if !isAlive(&got, 4) || got.SaveLeft || !got.PoisonLeft || saved.Phase != phaseSpeak {
		t.Fatalf("alive %v save %v poison %v phase %s", isAlive(&got, 4), got.SaveLeft, got.PoisonLeft, saved.Phase)
	}
	poisonedBase := sample(variantWitch)
	poisonedBase.Turn = 6
	poisonedBase.Knifed = 0
	poisoned, _, err := g.Apply(rule.State{Phase: phaseWitch, Data: rule.MustJSON(poisonedBase), Deadline: rule.After(rule.TurnTimeout)}, 6, potion(false, 5), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	after := mustData(t, poisoned)
	if isAlive(&after, 5) || after.PoisonLeft || !after.SaveLeft {
		t.Fatalf("%+v alive5 %v", after.PoisonLeft, isAlive(&after, 5))
	}
	used := sample(variantWitch)
	used.Turn = 6
	used.Knifed = 4
	used.SaveLeft = false
	used.PoisonLeft = false
	usedState := rule.State{Phase: phaseWitch, Data: rule.MustJSON(used), Deadline: rule.After(rule.TurnTimeout)}
	if _, _, err := g.Apply(usedState, 6, potion(true, -1), time.Now()); !errors.Is(err, errcode.ErrBadPayload) {
		t.Fatal(err)
	}
	if _, _, err := g.Apply(usedState, 6, potion(false, 4), time.Now()); !errors.Is(err, errcode.ErrBadPayload) {
		t.Fatal(err)
	}
}

func TestVoteTieExilesNobody(t *testing.T) {
	g := game{}
	data := sample(variantClassic)
	data.Turn = 1
	data.Votes = []vote{}
	data.Speeches = []speech{}
	state := rule.State{Phase: phaseVote, Data: rule.MustJSON(data), Deadline: rule.After(rule.TurnTimeout)}
	var err error
	// 1 and 2 vote 4; 3 and 4 vote 5; 5 and 6 abstain by timeout. 4 and 5 tie.
	for _, step := range []struct {
		seat   int
		target int
		auto   bool
	}{
		{1, 4, false}, {2, 4, false}, {3, 5, false}, {4, 5, false}, {5, 0, true}, {6, 0, true},
	} {
		if step.auto {
			state, _, err = g.OnTimeout(state, time.Now())
		} else {
			state, _, err = g.Apply(state, step.seat, knifeTarget(actVote, step.target), time.Now())
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if state.Settled || state.Phase != phaseWolf {
		t.Fatalf("phase %s settled %v", state.Phase, state.Settled)
	}
	got := mustData(t, state)
	for _, p := range got.Players {
		if !p.Alive {
			t.Fatalf("seat %d died", p.Seat)
		}
	}
}

func sample(variant string) wolfData {
	return wolfData{
		Variant: variant,
		Seats:   []int{1, 2, 3, 4, 5, 6},
		Players: []player{
			{Seat: 1, Role: roleWolf, Alive: true},
			{Seat: 2, Role: roleWolf, Alive: true},
			{Seat: 3, Role: roleSeer, Alive: true},
			{Seat: 4, Role: roleVillager, Alive: true},
			{Seat: 5, Role: roleVillager, Alive: true},
			{Seat: 6, Role: roleWitch, Alive: true},
		},
		Speeches: []speech{}, Votes: []vote{}, Knives: []knifeVote{}, Checks: []check{},
		SaveLeft: true, PoisonLeft: true,
	}
}

func knife(target int) rule.Action {
	return knifeTarget(actKnife, target)
}

func knifeTarget(action string, target int) rule.Action {
	return rule.Action{Type: action, Payload: rule.MustJSON(map[string]int{"TargetSeat": target})}
}

func potion(save bool, poison int) rule.Action {
	return rule.Action{Type: actPotion, Payload: rule.MustJSON(map[string]any{"Save": save, "Poison": poison})}
}

func mustData(t *testing.T, state rule.State) wolfData {
	t.Helper()
	var data wolfData
	if err := rule.Decode(state.Data, &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func countRoles(t *testing.T, g game, state rule.State, n int) map[string]int {
	t.Helper()
	counts := map[string]int{}
	wolfSeats := []int{}
	views := make([]rule.Snapshot, n+1)
	for seat := 1; seat <= n; seat++ {
		view, err := g.View(state, seat)
		if err != nil {
			t.Fatal(err)
		}
		views[seat] = view
		assertNoRoleLeak(t, view.Public)
		var priv struct {
			Role   string `json:"Role"`
			Wolves []int  `json:"Wolves"`
		}
		if err := json.Unmarshal(view.Private, &priv); err != nil {
			t.Fatal(err)
		}
		counts[priv.Role]++
		if priv.Role == roleWolf {
			wolfSeats = append(wolfSeats, seat)
			if len(priv.Wolves) != 1 {
				t.Fatalf("seat %d wolves %+v", seat, priv.Wolves)
			}
		}
	}
	if len(wolfSeats) == 2 {
		left, err := g.View(state, wolfSeats[0])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(left.Private, []byte(strconv.Itoa(wolfSeats[1]))) {
			t.Fatalf("wolf %d cannot see %d in %s", wolfSeats[0], wolfSeats[1], left.Private)
		}
	}
	for seat := 1; seat <= n; seat++ {
		var priv struct {
			Role string `json:"Role"`
		}
		_ = json.Unmarshal(views[seat].Private, &priv)
		if priv.Role != roleVillager {
			continue
		}
		for _, wolfSeat := range wolfSeats {
			if bytes.Contains(views[seat].Private, []byte(strconv.Itoa(wolfSeat))) {
				t.Fatalf("villager %d saw wolf %d", seat, wolfSeat)
			}
		}
	}
	return counts
}

func assertNoRoleLeak(t *testing.T, raw json.RawMessage) {
	t.Helper()
	if bytes.Contains(raw, []byte(`"Role"`)) || bytes.Contains(raw, []byte(`"Wolves"`)) || bytes.Contains(raw, []byte(`"wolf"`)) {
		t.Fatalf("leak %s", raw)
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
