package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

type txKey struct{}

type sqlConn interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type MySQL struct {
	db *sql.DB
}

func NewMySQL(db *sql.DB) *MySQL {
	return &MySQL{db: db}
}

func (r *MySQL) WithTx(ctx context.Context, fn func(ctx context.Context) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	txCtx := context.WithValue(ctx, txKey{}, tx)
	if err := fn(txCtx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (r *MySQL) conn(ctx context.Context) sqlConn {
	if tx, ok := ctx.Value(txKey{}).(*sql.Tx); ok {
		return tx
	}
	return r.db
}

func (r *MySQL) UpsertUser(ctx context.Context, openid, nickname, avatar string) (User, error) {
	now := time.Now().UTC()
	_, err := r.conn(ctx).ExecContext(ctx, `
INSERT INTO users (openid, nickname, avatar, created_at, updated_at)
VALUES (?, ?, ?, ?, ?) AS new
ON DUPLICATE KEY UPDATE
  nickname = IF(new.nickname = '', users.nickname, new.nickname),
  avatar = IF(new.avatar = '', users.avatar, new.avatar),
  updated_at = new.updated_at`, openid, nickname, avatar, now, now)
	if err != nil {
		return User{}, err
	}
	return r.userByOpenID(ctx, openid)
}

func (r *MySQL) userByOpenID(ctx context.Context, openid string) (User, error) {
	row := r.conn(ctx).QueryRowContext(ctx, `SELECT id, openid, nickname, avatar FROM users WHERE openid = ?`, openid)
	var u User
	err := row.Scan(&u.ID, &u.OpenID, &u.Nickname, &u.Avatar)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (r *MySQL) GetUser(ctx context.Context, id int64) (User, error) {
	row := r.conn(ctx).QueryRowContext(ctx, `SELECT id, openid, nickname, avatar FROM users WHERE id = ?`, id)
	var u User
	err := row.Scan(&u.ID, &u.OpenID, &u.Nickname, &u.Avatar)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (r *MySQL) CreateRoom(ctx context.Context, room Room) error {
	now := time.Now().UTC()
	_, err := r.conn(ctx).ExecContext(ctx, `
INSERT INTO rooms (id, code, active_code, host_user_id, game_code, variant, status, config_json, max_seats, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		room.ID, room.Code, room.Code, room.HostUserID, room.GameCode, room.Variant, room.Status,
		nonNullJSON(room.Config), room.MaxSeats, now, now)
	return mapDup(err)
}

func (r *MySQL) UpdateRoom(ctx context.Context, room Room) error {
	res, err := r.conn(ctx).ExecContext(ctx, `
UPDATE rooms SET game_code=?, variant=?, config_json=?, max_seats=?, updated_at=?
WHERE id=? AND status=?`,
		room.GameCode, room.Variant, nonNullJSON(room.Config), room.MaxSeats, time.Now().UTC(), room.ID, RoomOpen)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *MySQL) GetRoom(ctx context.Context, id string) (Room, error) {
	row := r.conn(ctx).QueryRowContext(ctx, roomSelect()+` WHERE id=?`, id)
	return scanRoom(row)
}

func (r *MySQL) GetActiveRoomByCode(ctx context.Context, code string) (Room, error) {
	row := r.conn(ctx).QueryRowContext(ctx, roomSelect()+` WHERE active_code=?`, code)
	return scanRoom(row)
}

func (r *MySQL) ListRoomsByUser(ctx context.Context, userID int64) ([]Room, error) {
	rows, err := r.conn(ctx).QueryContext(ctx, roomSelect()+`
 JOIN room_members m ON m.room_id = rooms.id
 WHERE m.user_id=? AND rooms.status<>?
 ORDER BY rooms.updated_at DESC`, userID, RoomClosed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Room
	for rows.Next() {
		room, err := scanRoom(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, room)
	}
	return out, rows.Err()
}

func (r *MySQL) SetRoomStatus(ctx context.Context, roomID, status, activeCode string) error {
	var code any
	if activeCode != "" {
		code = activeCode
	}
	_, err := r.conn(ctx).ExecContext(ctx, `UPDATE rooms SET status=?, active_code=?, updated_at=? WHERE id=?`,
		status, code, time.Now().UTC(), roomID)
	return err
}

func (r *MySQL) SetHost(ctx context.Context, roomID string, userID int64) error {
	_, err := r.conn(ctx).ExecContext(ctx, `UPDATE rooms SET host_user_id=?, updated_at=? WHERE id=?`, userID, time.Now().UTC(), roomID)
	return err
}

func (r *MySQL) AddMember(ctx context.Context, m Member) error {
	_, err := r.conn(ctx).ExecContext(ctx, `
INSERT INTO room_members (room_id, seat, user_id, nickname, ready, away) VALUES (?, ?, ?, ?, ?, ?)`,
		m.RoomID, m.Seat, m.UserID, m.Nickname, boolInt(m.Ready), boolInt(m.Away))
	return mapDup(err)
}

func (r *MySQL) RemoveMember(ctx context.Context, roomID string, userID int64) error {
	_, err := r.conn(ctx).ExecContext(ctx, `DELETE FROM room_members WHERE room_id=? AND user_id=?`, roomID, userID)
	return err
}

func (r *MySQL) ListMembers(ctx context.Context, roomID string) ([]Member, error) {
	rows, err := r.conn(ctx).QueryContext(ctx, `
SELECT room_id, seat, user_id, nickname, ready, away FROM room_members WHERE room_id=? ORDER BY seat`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		var ready, away int
		if err := rows.Scan(&m.RoomID, &m.Seat, &m.UserID, &m.Nickname, &ready, &away); err != nil {
			return nil, err
		}
		m.Ready = ready == 1
		m.Away = away == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *MySQL) SetReady(ctx context.Context, roomID string, userID int64, ready bool) error {
	_, err := r.conn(ctx).ExecContext(ctx, `UPDATE room_members SET ready=? WHERE room_id=? AND user_id=?`, boolInt(ready), roomID, userID)
	return err
}

func (r *MySQL) SetAway(ctx context.Context, roomID string, userID int64, away bool) error {
	_, err := r.conn(ctx).ExecContext(ctx, `UPDATE room_members SET away=?, ready=IF(?, 0, ready) WHERE room_id=? AND user_id=?`,
		boolInt(away), boolInt(away), roomID, userID)
	return err
}

func (r *MySQL) ResetReady(ctx context.Context, roomID string) error {
	_, err := r.conn(ctx).ExecContext(ctx, `UPDATE room_members SET ready=0 WHERE room_id=?`, roomID)
	return err
}

func (r *MySQL) CreatePlay(ctx context.Context, p Play) error {
	now := time.Now().UTC()
	_, err := r.conn(ctx).ExecContext(ctx, `
INSERT INTO plays (id, room_id, game_code, variant, status, snapshot_json, cursor_seq, deadline, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.RoomID, p.GameCode, p.Variant, p.Status, nonNullJSON(p.Snapshot), p.Cursor, nullTime(p.Deadline), now, now)
	return err
}

func (r *MySQL) GetPlay(ctx context.Context, id string) (Play, error) {
	return scanPlay(r.conn(ctx).QueryRowContext(ctx, playSelect()+` WHERE id=?`, id))
}

func (r *MySQL) LockPlay(ctx context.Context, id string) (Play, error) {
	return scanPlay(r.conn(ctx).QueryRowContext(ctx, playSelect()+` WHERE id=? FOR UPDATE`, id))
}

func (r *MySQL) GetRunningPlayByRoom(ctx context.Context, roomID string) (Play, error) {
	return scanPlay(r.conn(ctx).QueryRowContext(ctx, playSelect()+` WHERE room_id=? AND status=? ORDER BY created_at DESC LIMIT 1`, roomID, PlayRunning))
}

func (r *MySQL) SavePlay(ctx context.Context, p Play) error {
	_, err := r.conn(ctx).ExecContext(ctx, `
UPDATE plays SET status=?, snapshot_json=?, cursor_seq=?, deadline=?, updated_at=? WHERE id=?`,
		p.Status, nonNullJSON(p.Snapshot), p.Cursor, nullTime(p.Deadline), time.Now().UTC(), p.ID)
	return err
}

func (r *MySQL) ListDuePlays(ctx context.Context, now time.Time, limit int) ([]Play, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.conn(ctx).QueryContext(ctx, playSelect()+`
 WHERE status=? AND deadline IS NOT NULL AND deadline<=? ORDER BY deadline ASC LIMIT ?`, PlayRunning, now.UTC(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Play
	for rows.Next() {
		p, err := scanPlay(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *MySQL) AppendEvents(ctx context.Context, events []Event) error {
	for _, ev := range events {
		created := ev.CreatedAt
		if created.IsZero() {
			created = time.Now().UTC()
		}
		_, err := r.conn(ctx).ExecContext(ctx, `
INSERT INTO play_events (play_id, seq, scope, event_type, payload_json, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
			ev.PlayID, ev.Seq, ev.Scope, ev.Type, nonNullJSON(ev.Payload), created)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *MySQL) ListEvents(ctx context.Context, playID string, after int64, seat int, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.conn(ctx).QueryContext(ctx, `
SELECT play_id, seq, scope, event_type, payload_json, created_at
FROM play_events WHERE play_id=? AND seq>? AND (scope=-1 OR scope=?) ORDER BY seq ASC LIMIT ?`,
		playID, after, seat, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func (r *MySQL) HasAction(ctx context.Context, playID string, seat int, clientSeq int64) (bool, error) {
	var n int
	err := r.conn(ctx).QueryRowContext(ctx, `SELECT 1 FROM play_actions WHERE play_id=? AND seat=? AND client_seq=?`, playID, seat, clientSeq).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (r *MySQL) SaveAction(ctx context.Context, playID string, seat int, clientSeq int64, actionType string) error {
	_, err := r.conn(ctx).ExecContext(ctx, `
INSERT INTO play_actions (play_id, seat, client_seq, action_type, created_at) VALUES (?, ?, ?, ?, ?)`,
		playID, seat, clientSeq, actionType, time.Now().UTC())
	return mapDup(err)
}

func (r *MySQL) InsertSettlements(ctx context.Context, rowsIn []Settlement) error {
	now := time.Now().UTC()
	for _, row := range rowsIn {
		_, err := r.conn(ctx).ExecContext(ctx, `
INSERT INTO settlements (play_id, room_id, user_id, seat, delta, detail_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			row.PlayID, row.RoomID, row.UserID, row.Seat, row.Delta, nonNullJSON(row.Detail), now)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *MySQL) ListSettlementsByUser(ctx context.Context, userID int64, gameCode string, page, pageSize int) ([]Settlement, error) {
	offset := (page - 1) * pageSize
	rows, err := r.conn(ctx).QueryContext(ctx, `
SELECT s.id, s.play_id, s.room_id, s.user_id, s.seat, s.delta, s.detail_json, s.created_at, p.game_code, p.variant
FROM settlements s JOIN plays p ON p.id = s.play_id
WHERE s.user_id=? AND (?='' OR p.game_code=?)
ORDER BY s.id DESC LIMIT ? OFFSET ?`, userID, gameCode, gameCode, pageSize, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSettlements(rows)
}

func (r *MySQL) ListSettlementsByPlay(ctx context.Context, playID string) ([]Settlement, error) {
	rows, err := r.conn(ctx).QueryContext(ctx, `
SELECT s.id, s.play_id, s.room_id, s.user_id, s.seat, s.delta, s.detail_json, s.created_at, p.game_code, p.variant
FROM settlements s JOIN plays p ON p.id = s.play_id
WHERE s.play_id=? ORDER BY s.seat`, playID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSettlements(rows)
}

func (r *MySQL) UserInPlay(ctx context.Context, playID string, userID int64) (bool, error) {
	var n int
	err := r.conn(ctx).QueryRowContext(ctx, `SELECT 1 FROM settlements WHERE play_id=? AND user_id=? LIMIT 1`, playID, userID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func roomSelect() string {
	return `SELECT rooms.id, rooms.code, rooms.host_user_id, rooms.game_code, rooms.variant, rooms.status, rooms.config_json, rooms.max_seats, rooms.created_at, rooms.updated_at FROM rooms`
}

func playSelect() string {
	return `SELECT id, room_id, game_code, variant, status, snapshot_json, cursor_seq, deadline, created_at, updated_at FROM plays`
}

type rower interface {
	Scan(dest ...any) error
}

func scanRoom(row rower) (Room, error) {
	var room Room
	var cfg []byte
	err := row.Scan(&room.ID, &room.Code, &room.HostUserID, &room.GameCode, &room.Variant, &room.Status, &cfg, &room.MaxSeats, &room.CreatedAt, &room.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Room{}, ErrNotFound
	}
	if err != nil {
		return Room{}, err
	}
	room.Config = json.RawMessage(cfg)
	room.CreatedAt = room.CreatedAt.UTC()
	room.UpdatedAt = room.UpdatedAt.UTC()
	return room, nil
}

func scanPlay(row rower) (Play, error) {
	var p Play
	var snap []byte
	var deadline sql.NullTime
	err := row.Scan(&p.ID, &p.RoomID, &p.GameCode, &p.Variant, &p.Status, &snap, &p.Cursor, &deadline, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Play{}, ErrNotFound
	}
	if err != nil {
		return Play{}, err
	}
	p.Snapshot = json.RawMessage(snap)
	p.CreatedAt = p.CreatedAt.UTC()
	p.UpdatedAt = p.UpdatedAt.UTC()
	if deadline.Valid {
		t := deadline.Time.UTC()
		p.Deadline = &t
	}
	return p, nil
}

func scanEvents(rows *sql.Rows) ([]Event, error) {
	var out []Event
	for rows.Next() {
		var ev Event
		var payload []byte
		if err := rows.Scan(&ev.PlayID, &ev.Seq, &ev.Scope, &ev.Type, &payload, &ev.CreatedAt); err != nil {
			return nil, err
		}
		ev.Payload = json.RawMessage(payload)
		ev.CreatedAt = ev.CreatedAt.UTC()
		out = append(out, ev)
	}
	return out, rows.Err()
}

func scanSettlements(rows *sql.Rows) ([]Settlement, error) {
	var out []Settlement
	for rows.Next() {
		var s Settlement
		var detail []byte
		if err := rows.Scan(&s.ID, &s.PlayID, &s.RoomID, &s.UserID, &s.Seat, &s.Delta, &detail, &s.CreatedAt, &s.GameCode, &s.Variant); err != nil {
			return nil, err
		}
		s.Detail = json.RawMessage(detail)
		s.CreatedAt = s.CreatedAt.UTC()
		out = append(out, s)
	}
	return out, rows.Err()
}

func nonNullJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 || string(raw) == "null" {
		return []byte(`{}`)
	}
	return raw
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func mapDup(err error) error {
	var me *mysqldriver.MySQLError
	if errors.As(err, &me) && me.Number == 1062 {
		return ErrDuplicate
	}
	return err
}

// ErrDuplicate is a unique-key collision.
var ErrDuplicate = errors.New("duplicate")
