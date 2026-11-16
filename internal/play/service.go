package play

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"table/internal/actor"
	"table/internal/errcode"
	"table/internal/id"
	"table/internal/rule"
	"table/internal/store"
)

type Publisher interface {
	Publish(ctx context.Context, roomID string, cursor int64) error
}

type Service struct {
	repo store.Store
	hub  *actor.Hub
	bus  Publisher
}

func New(repo store.Store, hub *actor.Hub, bus Publisher) *Service {
	return &Service{repo: repo, hub: hub, bus: bus}
}

type StartReq struct {
	RoomId string `json:"RoomId"`
}

type ActionBody struct {
	Type    string          `json:"Type"`
	Payload json.RawMessage `json:"Payload"`
}

type SubmitReq struct {
	RoomId    string     `json:"RoomId"`
	PlayId    string     `json:"PlayId"`
	Seat      int        `json:"Seat"`
	ClientSeq int64      `json:"ClientSeq"`
	Action    ActionBody `json:"Action"`
}

type PlayReq struct {
	RoomId string `json:"RoomId"`
	PlayId string `json:"PlayId"`
	Cursor int64  `json:"Cursor"`
	Limit  int    `json:"Limit"`
}

type SubmitResult struct {
	PlayId     string `json:"PlayId"`
	Cursor     int64  `json:"Cursor"`
	Idempotent bool   `json:"Idempotent"`
}

type EventView struct {
	Seq     int64           `json:"Seq"`
	Type    string          `json:"Type"`
	Payload json.RawMessage `json:"Payload"`
}

type PullResult struct {
	PlayId string      `json:"PlayId"`
	Cursor int64       `json:"Cursor"`
	Events []EventView `json:"Events"`
}

type SnapshotResult struct {
	PlayId   string            `json:"PlayId"`
	RoomId   string            `json:"RoomId"`
	GameCode string            `json:"GameCode"`
	Variant  string            `json:"Variant"`
	Seat     int               `json:"Seat"`
	Cursor   int64             `json:"Cursor"`
	Phase    string            `json:"Phase"`
	Settled  bool              `json:"Settled"`
	Deadline *time.Time        `json:"Deadline,omitempty"`
	Public   json.RawMessage   `json:"Public"`
	Private  json.RawMessage   `json:"Private"`
	Legal    []rule.ActionSpec `json:"Legal"`
}

type LegalResult struct {
	PlayId string            `json:"PlayId"`
	Seat   int               `json:"Seat"`
	Legal  []rule.ActionSpec `json:"Legal"`
}

func (s *Service) Start(ctx context.Context, userID int64, req StartReq) (SnapshotResult, error) {
	if req.RoomId == "" {
		return SnapshotResult{}, errcode.ErrBadRoom
	}
	var out SnapshotResult
	_, err := s.hub.Call(ctx, req.RoomId, func(ctx context.Context) (any, error) {
		err := s.repo.WithTx(ctx, func(ctx context.Context) error {
			snap, err := s.startLocked(ctx, userID, req.RoomId)
			out = snap
			return err
		})
		return nil, err
	})
	if err != nil {
		return SnapshotResult{}, err
	}
	s.publish(ctx, req.RoomId, out.Cursor)
	return out, nil
}

func (s *Service) Submit(ctx context.Context, userID int64, req SubmitReq) (SubmitResult, error) {
	if req.RoomId == "" || req.PlayId == "" || req.ClientSeq <= 0 || req.Action.Type == "" {
		return SubmitResult{}, errcode.ErrBadPayload
	}
	var out SubmitResult
	_, err := s.hub.Call(ctx, req.RoomId, func(ctx context.Context) (any, error) {
		err := s.repo.WithTx(ctx, func(ctx context.Context) error {
			result, err := s.submitLocked(ctx, userID, req)
			out = result
			return err
		})
		return nil, err
	})
	if err != nil {
		return SubmitResult{}, err
	}
	if !out.Idempotent {
		s.publish(ctx, req.RoomId, out.Cursor)
	}
	return out, nil
}

func (s *Service) Describe(ctx context.Context, userID int64, req PlayReq) (SnapshotResult, error) {
	play, seat, err := s.visiblePlay(ctx, userID, req)
	if err != nil {
		return SnapshotResult{}, err
	}
	return s.snapshot(play, seat)
}

func (s *Service) Legal(ctx context.Context, userID int64, req PlayReq) (LegalResult, error) {
	snap, err := s.Describe(ctx, userID, req)
	if err != nil {
		return LegalResult{}, err
	}
	return LegalResult{PlayId: snap.PlayId, Seat: snap.Seat, Legal: snap.Legal}, nil
}

func (s *Service) Pull(ctx context.Context, userID int64, req PlayReq) (PullResult, error) {
	play, seat, err := s.visiblePlay(ctx, userID, req)
	if err != nil {
		return PullResult{}, err
	}
	limit := req.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	events, err := s.repo.ListEvents(ctx, play.ID, req.Cursor, seat, limit)
	if err != nil {
		return PullResult{}, err
	}
	out := PullResult{PlayId: play.ID, Cursor: req.Cursor, Events: make([]EventView, 0, len(events))}
	for _, ev := range events {
		out.Events = append(out.Events, EventView{Seq: ev.Seq, Type: ev.Type, Payload: rule.Normalize(ev.Payload)})
		out.Cursor = ev.Seq
	}
	return out, nil
}

func (s *Service) RunTicker(ctx context.Context) {
	tk := time.NewTicker(time.Second)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tk.C:
			plays, err := s.repo.ListDuePlays(ctx, now.UTC(), 50)
			if err != nil {
				slog.Error("list due plays", "err", err)
				continue
			}
			for _, play := range plays {
				_, err := s.hub.Call(ctx, play.RoomID, func(ctx context.Context) (any, error) {
					return nil, s.repo.WithTx(ctx, func(ctx context.Context) error {
						return s.timeoutLocked(ctx, play.ID, now.UTC())
					})
				})
				if err != nil && ctx.Err() == nil {
					slog.Error("play timeout", "play", play.ID, "err", err)
				}
			}
		}
	}
}

func (s *Service) startLocked(ctx context.Context, userID int64, roomID string) (SnapshotResult, error) {
	room, err := s.repo.GetRoom(ctx, roomID)
	if err != nil {
		return SnapshotResult{}, mapMissing(err)
	}
	if room.HostUserID != userID {
		return SnapshotResult{}, errcode.ErrNotHost
	}
	if room.Status != store.RoomOpen {
		return SnapshotResult{}, errcode.ErrPlaying
	}
	if _, err := s.repo.GetRunningPlayByRoom(ctx, roomID); err == nil {
		return SnapshotResult{}, errcode.ErrPlaying
	} else if !errors.Is(err, store.ErrNotFound) {
		return SnapshotResult{}, err
	}
	members, err := s.repo.ListMembers(ctx, roomID)
	if err != nil {
		return SnapshotResult{}, err
	}
	var actors []store.Member
	var seats []int
	for _, member := range members {
		if member.Away {
			continue
		}
		if !member.Ready {
			return SnapshotResult{}, errcode.ErrNotReady
		}
		actors = append(actors, member)
		seats = append(seats, member.Seat)
	}
	engine, ok := rule.Get(room.GameCode)
	if !ok {
		return SnapshotResult{}, errcode.ErrBadGame
	}
	if len(seats) < engine.MinSeats() {
		return SnapshotResult{}, errcode.ErrTooFew
	}
	playID := id.New("p_")
	state, events, err := engine.Start(rule.StartContext{
		PlayID: playID, RoomID: room.ID, GameCode: room.GameCode,
		Variant: room.Variant, Config: rule.Normalize(room.Config), Seats: seats,
	})
	if err != nil {
		return SnapshotResult{}, err
	}
	state.Data = rule.Normalize(state.Data)
	play := store.Play{
		ID: playID, RoomID: room.ID, GameCode: room.GameCode, Variant: room.Variant,
		Status: store.PlayRunning, Deadline: state.Deadline,
	}
	if err := s.commitNew(ctx, play, actors, state, events); err != nil {
		return SnapshotResult{}, err
	}
	seat := seatOf(actors, userID)
	saved, err := s.repo.GetPlay(ctx, playID)
	if err != nil {
		return SnapshotResult{}, err
	}
	return s.snapshot(saved, seat)
}

func (s *Service) submitLocked(ctx context.Context, userID int64, req SubmitReq) (SubmitResult, error) {
	play, err := s.repo.LockPlay(ctx, req.PlayId)
	if err != nil {
		return SubmitResult{}, mapMissing(err)
	}
	if play.RoomID != req.RoomId {
		return SubmitResult{}, errcode.ErrNotPlaying
	}
	_, member, err := memberOf(ctx, s.repo, play.RoomID, userID)
	if err != nil {
		return SubmitResult{}, err
	}
	if req.Seat != 0 && req.Seat != member.Seat {
		return SubmitResult{}, errcode.ErrSeatMismatch
	}
	done, err := s.repo.HasAction(ctx, play.ID, member.Seat, req.ClientSeq)
	if err != nil {
		return SubmitResult{}, err
	}
	if done {
		return SubmitResult{PlayId: play.ID, Cursor: play.Cursor, Idempotent: true}, nil
	}
	if member.Away {
		return SubmitResult{}, errcode.ErrAway
	}
	if play.Status != store.PlayRunning {
		return SubmitResult{}, errcode.ErrNotPlaying
	}
	engine, ok := rule.Get(play.GameCode)
	if !ok {
		return SubmitResult{}, errcode.ErrBadGame
	}
	var state rule.State
	if err := json.Unmarshal(play.Snapshot, &state); err != nil {
		return SubmitResult{}, err
	}
	next, events, err := engine.Apply(state, member.Seat, rule.Action{Type: req.Action.Type, Payload: req.Action.Payload}, time.Now().UTC())
	if err != nil {
		return SubmitResult{}, err
	}
	next.Data = rule.Normalize(next.Data)
	actors, err := s.repo.ListMembers(ctx, play.RoomID)
	if err != nil {
		return SubmitResult{}, err
	}
	if err := s.commitExisting(ctx, play, actors, next, events, member.Seat, req.ClientSeq, req.Action.Type); err != nil {
		return SubmitResult{}, err
	}
	return SubmitResult{PlayId: play.ID, Cursor: play.Cursor + int64(len(events))}, nil
}

func (s *Service) timeoutLocked(ctx context.Context, playID string, now time.Time) error {
	play, err := s.repo.LockPlay(ctx, playID)
	if err != nil {
		return err
	}
	if play.Status != store.PlayRunning || play.Deadline == nil || play.Deadline.After(now) {
		return nil
	}
	engine, ok := rule.Get(play.GameCode)
	if !ok {
		return errcode.ErrBadGame
	}
	var state rule.State
	if err := json.Unmarshal(play.Snapshot, &state); err != nil {
		return err
	}
	next, events, err := engine.OnTimeout(state, now)
	if err != nil {
		return err
	}
	prevRaw, _ := json.Marshal(state)
	next.Data = rule.Normalize(next.Data)
	nextRaw, _ := json.Marshal(next)
	if len(events) == 0 && bytes.Equal(prevRaw, nextRaw) {
		pushed := now.Add(rule.TurnTimeout)
		next.Deadline = &pushed
	}
	actors, err := s.repo.ListMembers(ctx, play.RoomID)
	if err != nil {
		return err
	}
	return s.commitExisting(ctx, play, actors, next, events, 0, 0, "")
}

func (s *Service) commitNew(ctx context.Context, play store.Play, actors []store.Member, state rule.State, events []rule.Event) error {
	rows, cursor, settle, err := packEvents(play.ID, 0, events)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	play.Snapshot = raw
	play.Cursor = cursor
	play.Deadline = state.Deadline
	if state.Settled {
		play.Status = store.PlaySettled
		play.Deadline = nil
		if settle == nil {
			return errcode.ErrSettleMissing
		}
	}
	if err := s.repo.CreatePlay(ctx, play); err != nil {
		return err
	}
	if err := s.repo.AppendEvents(ctx, rows); err != nil {
		return err
	}
	if err := s.repo.ResetReady(ctx, play.RoomID); err != nil {
		return err
	}
	if state.Settled {
		return writeSettle(ctx, s.repo, play, actors, settle)
	}
	room, err := s.repo.GetRoom(ctx, play.RoomID)
	if err != nil {
		return err
	}
	return s.repo.SetRoomStatus(ctx, play.RoomID, store.RoomPlaying, room.Code)
}

func (s *Service) commitExisting(ctx context.Context, play store.Play, actors []store.Member, state rule.State, events []rule.Event, seat int, clientSeq int64, actionType string) error {
	rows, cursor, settle, err := packEvents(play.ID, play.Cursor, events)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	play.Snapshot = raw
	play.Cursor = cursor
	play.Deadline = state.Deadline
	if state.Settled {
		play.Status = store.PlaySettled
		play.Deadline = nil
		if settle == nil {
			return errcode.ErrSettleMissing
		}
	}
	if err := s.repo.AppendEvents(ctx, rows); err != nil {
		return err
	}
	if err := s.repo.SavePlay(ctx, play); err != nil {
		return err
	}
	if clientSeq > 0 {
		if err := s.repo.SaveAction(ctx, play.ID, seat, clientSeq, actionType); err != nil {
			return err
		}
	}
	if !state.Settled {
		return nil
	}
	if err := writeSettle(ctx, s.repo, play, actors, settle); err != nil {
		return err
	}
	room, err := s.repo.GetRoom(ctx, play.RoomID)
	if err != nil {
		return err
	}
	if err := s.repo.SetRoomStatus(ctx, play.RoomID, store.RoomOpen, room.Code); err != nil {
		return err
	}
	return s.repo.ResetReady(ctx, play.RoomID)
}

func (s *Service) visiblePlay(ctx context.Context, userID int64, req PlayReq) (store.Play, int, error) {
	if req.PlayId == "" {
		return store.Play{}, 0, errcode.ErrNotPlaying
	}
	play, err := s.repo.GetPlay(ctx, req.PlayId)
	if err != nil {
		return store.Play{}, 0, mapMissing(err)
	}
	if req.RoomId != "" && req.RoomId != play.RoomID {
		return store.Play{}, 0, errcode.ErrNotPlaying
	}
	_, member, err := memberOf(ctx, s.repo, play.RoomID, userID)
	if err != nil {
		return store.Play{}, 0, err
	}
	return play, member.Seat, nil
}

func (s *Service) snapshot(play store.Play, seat int) (SnapshotResult, error) {
	engine, ok := rule.Get(play.GameCode)
	if !ok {
		return SnapshotResult{}, errcode.ErrBadGame
	}
	var state rule.State
	if err := json.Unmarshal(play.Snapshot, &state); err != nil {
		return SnapshotResult{}, err
	}
	view, err := engine.View(state, seat)
	if err != nil {
		return SnapshotResult{}, err
	}
	if view.Legal == nil {
		view.Legal = []rule.ActionSpec{}
	}
	return SnapshotResult{
		PlayId: play.ID, RoomId: play.RoomID, GameCode: play.GameCode, Variant: play.Variant,
		Seat: seat, Cursor: play.Cursor, Phase: view.Phase, Settled: view.Settled || state.Settled,
		Deadline: view.Deadline, Public: rule.Normalize(view.Public), Private: rule.Normalize(view.Private),
		Legal: view.Legal,
	}, nil
}

func (s *Service) publish(ctx context.Context, roomID string, cursor int64) {
	if s.bus == nil {
		return
	}
	if err := s.bus.Publish(ctx, roomID, cursor); err != nil {
		slog.Error("publish room", "room", roomID, "err", err)
	}
}

func packEvents(playID string, cursor int64, events []rule.Event) ([]store.Event, int64, json.RawMessage, error) {
	rows := make([]store.Event, 0, len(events))
	var settle json.RawMessage
	for _, ev := range events {
		cursor++
		payload, err := rule.MarshalPayload(ev.Payload)
		if err != nil {
			return nil, cursor, nil, err
		}
		scope := ev.Scope
		if scope == 0 {
			scope = rule.ScopePublic
		}
		if ev.Type == rule.EventSettle {
			settle = payload
			scope = rule.ScopePublic
		}
		rows = append(rows, store.Event{PlayID: playID, Seq: cursor, Scope: scope, Type: ev.Type, Payload: payload})
	}
	return rows, cursor, settle, nil
}

type scoreLine struct {
	Seat   int             `json:"Seat"`
	Delta  int             `json:"Delta"`
	Detail json.RawMessage `json:"Detail"`
}

func writeSettle(ctx context.Context, repo store.Store, play store.Play, actors []store.Member, raw json.RawMessage) error {
	var body struct {
		Scores []scoreLine `json:"Scores"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return errcode.WithDetail(errcode.ErrSettleMissing, err.Error())
	}
	bySeat := map[int]scoreLine{}
	for _, line := range body.Scores {
		bySeat[line.Seat] = line
	}
	rows := make([]store.Settlement, 0, len(actors))
	seen := map[int64]bool{}
	for _, member := range actors {
		line, ok := bySeat[member.Seat]
		if !ok {
			if member.Away {
				continue
			}
			return errcode.WithDetail(errcode.ErrSettleMissing, "缺少座位结算")
		}
		if seen[member.UserID] {
			continue
		}
		seen[member.UserID] = true
		rows = append(rows, store.Settlement{
			PlayID: play.ID, RoomID: play.RoomID, UserID: member.UserID,
			Seat: member.Seat, Delta: line.Delta, Detail: rule.Normalize(line.Detail),
		})
	}
	if len(rows) == 0 {
		return errcode.ErrSettleMissing
	}
	return repo.InsertSettlements(ctx, rows)
}

func memberOf(ctx context.Context, repo store.Store, roomID string, userID int64) (store.Room, store.Member, error) {
	room, err := repo.GetRoom(ctx, roomID)
	if err != nil {
		return store.Room{}, store.Member{}, mapMissing(err)
	}
	members, err := repo.ListMembers(ctx, roomID)
	if err != nil {
		return store.Room{}, store.Member{}, err
	}
	for _, member := range members {
		if member.UserID == userID {
			return room, member, nil
		}
	}
	return store.Room{}, store.Member{}, errcode.ErrNotMember
}

func seatOf(members []store.Member, userID int64) int {
	for _, member := range members {
		if member.UserID == userID {
			return member.Seat
		}
	}
	return 0
}

func mapMissing(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return errcode.ErrNotPlaying
	}
	return err
}
