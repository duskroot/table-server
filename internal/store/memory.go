package store

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

type memKey struct{}

// Memory is an in-process Store for tests. WithTx clones the world and
// commits only when fn returns nil.
type Memory struct {
	mu sync.Mutex
	w  *world
}

type world struct {
	nextUser    int64
	users       map[int64]User
	openIDs     map[string]int64
	rooms       map[string]Room
	members     map[string][]Member
	plays       map[string]Play
	events      map[string][]Event
	actions     map[string]struct{}
	settlements []Settlement
	nextSettle  int64
}

func NewMemory() *Memory {
	return &Memory{w: newWorld()}
}

func newWorld() *world {
	return &world{
		users:   map[int64]User{},
		openIDs: map[string]int64{},
		rooms:   map[string]Room{},
		members: map[string][]Member{},
		plays:   map[string]Play{},
		events:  map[string][]Event{},
		actions: map[string]struct{}{},
	}
}

func (m *Memory) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := m.w.clone()
	txCtx := context.WithValue(ctx, memKey{}, cp)
	if err := fn(txCtx); err != nil {
		return err
	}
	m.w = cp
	return nil
}

func (m *Memory) world(ctx context.Context) (*world, func()) {
	if w, ok := ctx.Value(memKey{}).(*world); ok {
		return w, func() {}
	}
	m.mu.Lock()
	return m.w, m.mu.Unlock
}

func (w *world) clone() *world {
	cp := newWorld()
	cp.nextUser = w.nextUser
	cp.nextSettle = w.nextSettle
	for k, v := range w.users {
		cp.users[k] = v
	}
	for k, v := range w.openIDs {
		cp.openIDs[k] = v
	}
	for k, v := range w.rooms {
		v.Config = append(json.RawMessage(nil), v.Config...)
		cp.rooms[k] = v
	}
	for k, members := range w.members {
		cp.members[k] = append([]Member(nil), members...)
	}
	for k, v := range w.plays {
		v.Snapshot = append(json.RawMessage(nil), v.Snapshot...)
		cp.plays[k] = v
	}
	for k, evs := range w.events {
		dst := make([]Event, len(evs))
		for i, ev := range evs {
			ev.Payload = append(json.RawMessage(nil), ev.Payload...)
			dst[i] = ev
		}
		cp.events[k] = dst
	}
	for k := range w.actions {
		cp.actions[k] = struct{}{}
	}
	cp.settlements = append([]Settlement(nil), w.settlements...)
	return cp
}

func (m *Memory) UpsertUser(ctx context.Context, openid, nickname, avatar string) (User, error) {
	w, unlock := m.world(ctx)
	defer unlock()
	if id, ok := w.openIDs[openid]; ok {
		u := w.users[id]
		if nickname != "" {
			u.Nickname = nickname
		}
		if avatar != "" {
			u.Avatar = avatar
		}
		w.users[id] = u
		return u, nil
	}
	w.nextUser++
	u := User{ID: w.nextUser, OpenID: openid, Nickname: nickname, Avatar: avatar}
	w.users[u.ID] = u
	w.openIDs[openid] = u.ID
	return u, nil
}

func (m *Memory) GetUser(ctx context.Context, id int64) (User, error) {
	w, unlock := m.world(ctx)
	defer unlock()
	u, ok := w.users[id]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

func (m *Memory) CreateRoom(ctx context.Context, room Room) error {
	w, unlock := m.world(ctx)
	defer unlock()
	for _, existing := range w.rooms {
		if existing.Code == room.Code && existing.Status != RoomClosed {
			return ErrDuplicate
		}
	}
	room.Config = append(json.RawMessage(nil), nonNullJSON(room.Config)...)
	now := time.Now().UTC()
	room.CreatedAt, room.UpdatedAt = now, now
	w.rooms[room.ID] = room
	return nil
}

func (m *Memory) UpdateRoom(ctx context.Context, room Room) error {
	w, unlock := m.world(ctx)
	defer unlock()
	cur, ok := w.rooms[room.ID]
	if !ok || cur.Status != RoomOpen {
		return ErrNotFound
	}
	cur.GameCode = room.GameCode
	cur.Variant = room.Variant
	cur.Config = append(json.RawMessage(nil), nonNullJSON(room.Config)...)
	cur.MaxSeats = room.MaxSeats
	cur.UpdatedAt = time.Now().UTC()
	w.rooms[room.ID] = cur
	return nil
}

func (m *Memory) GetRoom(ctx context.Context, id string) (Room, error) {
	w, unlock := m.world(ctx)
	defer unlock()
	room, ok := w.rooms[id]
	if !ok {
		return Room{}, ErrNotFound
	}
	return room, nil
}

func (m *Memory) GetActiveRoomByCode(ctx context.Context, code string) (Room, error) {
	w, unlock := m.world(ctx)
	defer unlock()
	for _, room := range w.rooms {
		if room.Code == code && room.Status != RoomClosed {
			return room, nil
		}
	}
	return Room{}, ErrNotFound
}

func (m *Memory) ListRoomsByUser(ctx context.Context, userID int64) ([]Room, error) {
	w, unlock := m.world(ctx)
	defer unlock()
	var out []Room
	for roomID, members := range w.members {
		for _, member := range members {
			if member.UserID != userID {
				continue
			}
			room := w.rooms[roomID]
			if room.Status != RoomClosed {
				out = append(out, room)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (m *Memory) SetRoomStatus(ctx context.Context, roomID, status, activeCode string) error {
	w, unlock := m.world(ctx)
	defer unlock()
	room, ok := w.rooms[roomID]
	if !ok {
		return ErrNotFound
	}
	room.Status = status
	if activeCode != "" {
		room.Code = activeCode
	}
	if status == RoomClosed {
		// Code stays for display. Active lookup skips closed rooms.
	}
	room.UpdatedAt = time.Now().UTC()
	w.rooms[roomID] = room
	return nil
}

func (m *Memory) SetHost(ctx context.Context, roomID string, userID int64) error {
	w, unlock := m.world(ctx)
	defer unlock()
	room, ok := w.rooms[roomID]
	if !ok {
		return ErrNotFound
	}
	room.HostUserID = userID
	room.UpdatedAt = time.Now().UTC()
	w.rooms[roomID] = room
	return nil
}

func (m *Memory) AddMember(ctx context.Context, member Member) error {
	w, unlock := m.world(ctx)
	defer unlock()
	for _, existing := range w.members[member.RoomID] {
		if existing.UserID == member.UserID || existing.Seat == member.Seat {
			return ErrDuplicate
		}
	}
	w.members[member.RoomID] = append(w.members[member.RoomID], member)
	sort.Slice(w.members[member.RoomID], func(i, j int) bool {
		return w.members[member.RoomID][i].Seat < w.members[member.RoomID][j].Seat
	})
	return nil
}

func (m *Memory) RemoveMember(ctx context.Context, roomID string, userID int64) error {
	w, unlock := m.world(ctx)
	defer unlock()
	members := w.members[roomID]
	dst := members[:0]
	for _, member := range members {
		if member.UserID != userID {
			dst = append(dst, member)
		}
	}
	w.members[roomID] = append([]Member(nil), dst...)
	return nil
}

func (m *Memory) ListMembers(ctx context.Context, roomID string) ([]Member, error) {
	w, unlock := m.world(ctx)
	defer unlock()
	return append([]Member(nil), w.members[roomID]...), nil
}

func (m *Memory) SetReady(ctx context.Context, roomID string, userID int64, ready bool) error {
	return m.updateMember(ctx, roomID, userID, func(m *Member) { m.Ready = ready })
}

func (m *Memory) SetAway(ctx context.Context, roomID string, userID int64, away bool) error {
	return m.updateMember(ctx, roomID, userID, func(member *Member) {
		member.Away = away
		if away {
			member.Ready = false
		}
	})
}

func (m *Memory) updateMember(ctx context.Context, roomID string, userID int64, fn func(*Member)) error {
	w, unlock := m.world(ctx)
	defer unlock()
	members := w.members[roomID]
	for i := range members {
		if members[i].UserID == userID {
			fn(&members[i])
			w.members[roomID] = members
			return nil
		}
	}
	return ErrNotFound
}

func (m *Memory) ResetReady(ctx context.Context, roomID string) error {
	w, unlock := m.world(ctx)
	defer unlock()
	members := w.members[roomID]
	for i := range members {
		members[i].Ready = false
	}
	w.members[roomID] = members
	return nil
}

func (m *Memory) CreatePlay(ctx context.Context, p Play) error {
	w, unlock := m.world(ctx)
	defer unlock()
	now := time.Now().UTC()
	p.CreatedAt, p.UpdatedAt = now, now
	p.Snapshot = append(json.RawMessage(nil), nonNullJSON(p.Snapshot)...)
	w.plays[p.ID] = p
	return nil
}

func (m *Memory) GetPlay(ctx context.Context, id string) (Play, error) {
	w, unlock := m.world(ctx)
	defer unlock()
	p, ok := w.plays[id]
	if !ok {
		return Play{}, ErrNotFound
	}
	return p, nil
}

func (m *Memory) LockPlay(ctx context.Context, id string) (Play, error) {
	return m.GetPlay(ctx, id)
}

func (m *Memory) GetRunningPlayByRoom(ctx context.Context, roomID string) (Play, error) {
	w, unlock := m.world(ctx)
	defer unlock()
	var found Play
	ok := false
	for _, p := range w.plays {
		if p.RoomID == roomID && p.Status == PlayRunning && (!ok || p.CreatedAt.After(found.CreatedAt)) {
			found = p
			ok = true
		}
	}
	if !ok {
		return Play{}, ErrNotFound
	}
	return found, nil
}

func (m *Memory) SavePlay(ctx context.Context, p Play) error {
	w, unlock := m.world(ctx)
	defer unlock()
	cur, ok := w.plays[p.ID]
	if !ok {
		return ErrNotFound
	}
	p.CreatedAt = cur.CreatedAt
	p.UpdatedAt = time.Now().UTC()
	p.Snapshot = append(json.RawMessage(nil), nonNullJSON(p.Snapshot)...)
	w.plays[p.ID] = p
	return nil
}

func (m *Memory) ListDuePlays(ctx context.Context, now time.Time, limit int) ([]Play, error) {
	w, unlock := m.world(ctx)
	defer unlock()
	var out []Play
	for _, p := range w.plays {
		if p.Status == PlayRunning && p.Deadline != nil && !p.Deadline.After(now) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Deadline.Before(*out[j].Deadline) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *Memory) AppendEvents(ctx context.Context, events []Event) error {
	w, unlock := m.world(ctx)
	defer unlock()
	for _, ev := range events {
		if ev.CreatedAt.IsZero() {
			ev.CreatedAt = time.Now().UTC()
		}
		ev.Payload = append(json.RawMessage(nil), nonNullJSON(ev.Payload)...)
		w.events[ev.PlayID] = append(w.events[ev.PlayID], ev)
	}
	return nil
}

func (m *Memory) ListEvents(ctx context.Context, playID string, after int64, seat int, limit int) ([]Event, error) {
	w, unlock := m.world(ctx)
	defer unlock()
	var out []Event
	for _, ev := range w.events[playID] {
		if ev.Seq <= after {
			continue
		}
		if ev.Scope != -1 && ev.Scope != seat {
			continue
		}
		out = append(out, ev)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func actionKey(playID string, seat int, clientSeq int64) string {
	return playID + "|" + itoa(seat) + "|" + itoa64(clientSeq)
}

func (m *Memory) HasAction(ctx context.Context, playID string, seat int, clientSeq int64) (bool, error) {
	w, unlock := m.world(ctx)
	defer unlock()
	_, ok := w.actions[actionKey(playID, seat, clientSeq)]
	return ok, nil
}

func (m *Memory) SaveAction(ctx context.Context, playID string, seat int, clientSeq int64, _ string) error {
	w, unlock := m.world(ctx)
	defer unlock()
	key := actionKey(playID, seat, clientSeq)
	if _, ok := w.actions[key]; ok {
		return ErrDuplicate
	}
	w.actions[key] = struct{}{}
	return nil
}

func (m *Memory) InsertSettlements(ctx context.Context, rows []Settlement) error {
	w, unlock := m.world(ctx)
	defer unlock()
	now := time.Now().UTC()
	for _, row := range rows {
		w.nextSettle++
		row.ID = w.nextSettle
		row.CreatedAt = now
		row.Detail = append(json.RawMessage(nil), nonNullJSON(row.Detail)...)
		w.settlements = append(w.settlements, row)
	}
	return nil
}

func (m *Memory) ListSettlementsByUser(ctx context.Context, userID int64, gameCode string, page, pageSize int) ([]Settlement, error) {
	w, unlock := m.world(ctx)
	defer unlock()
	var matched []Settlement
	for i := len(w.settlements) - 1; i >= 0; i-- {
		row := w.settlements[i]
		if row.UserID != userID {
			continue
		}
		play := w.plays[row.PlayID]
		row.GameCode = play.GameCode
		row.Variant = play.Variant
		if gameCode != "" && play.GameCode != gameCode {
			continue
		}
		matched = append(matched, row)
	}
	start := (page - 1) * pageSize
	if start >= len(matched) {
		return []Settlement{}, nil
	}
	end := start + pageSize
	if end > len(matched) {
		end = len(matched)
	}
	return matched[start:end], nil
}

func (m *Memory) ListSettlementsByPlay(ctx context.Context, playID string) ([]Settlement, error) {
	w, unlock := m.world(ctx)
	defer unlock()
	var out []Settlement
	for _, row := range w.settlements {
		if row.PlayID == playID {
			play := w.plays[row.PlayID]
			row.GameCode = play.GameCode
			row.Variant = play.Variant
			out = append(out, row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seat < out[j].Seat })
	return out, nil
}

func (m *Memory) UserInPlay(ctx context.Context, playID string, userID int64) (bool, error) {
	rows, err := m.ListSettlementsByPlay(ctx, playID)
	if err != nil {
		return false, err
	}
	for _, row := range rows {
		if row.UserID == userID {
			return true, nil
		}
	}
	return false, nil
}

func itoa(n int) string {
	return itoa64(int64(n))
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
