package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	RoomOpen    = "open"
	RoomPlaying = "playing"
	RoomClosed  = "closed"
	PlayRunning = "running"
	PlaySettled = "settled"
	PlayAborted = "aborted"
)

var ErrNotFound = errors.New("not found")

type User struct {
	ID       int64
	OpenID   string
	Nickname string
	Avatar   string
}

type Room struct {
	ID         string
	Code       string
	HostUserID int64
	GameCode   string
	Variant    string
	Status     string
	Config     json.RawMessage
	MaxSeats   int
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type Member struct {
	RoomID   string
	Seat     int
	UserID   int64
	Nickname string
	Ready    bool
	Away     bool
}

type Play struct {
	ID        string
	RoomID    string
	GameCode  string
	Variant   string
	Status    string
	Snapshot  json.RawMessage
	Cursor    int64
	Deadline  *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Event struct {
	PlayID    string
	Seq       int64
	Scope     int
	Type      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

type Settlement struct {
	ID        int64
	PlayID    string
	RoomID    string
	UserID    int64
	Seat      int
	Delta     int
	Detail    json.RawMessage
	CreatedAt time.Time
	GameCode  string
	Variant   string
}

// Store is the durable game record. MySQL is the implementation used in
// production. Memory is for tests. WithTx runs fn in one transaction;
// nested WithTx is not supported.
type Store interface {
	WithTx(ctx context.Context, fn func(ctx context.Context) error) error

	UpsertUser(ctx context.Context, openid, nickname, avatar string) (User, error)
	GetUser(ctx context.Context, id int64) (User, error)

	CreateRoom(ctx context.Context, room Room) error
	UpdateRoom(ctx context.Context, room Room) error
	GetRoom(ctx context.Context, id string) (Room, error)
	GetActiveRoomByCode(ctx context.Context, code string) (Room, error)
	ListRoomsByUser(ctx context.Context, userID int64) ([]Room, error)
	// SetRoomStatus writes status. activeCode empty stores NULL and releases the join code.
	SetRoomStatus(ctx context.Context, roomID, status, activeCode string) error
	SetHost(ctx context.Context, roomID string, userID int64) error

	AddMember(ctx context.Context, m Member) error
	RemoveMember(ctx context.Context, roomID string, userID int64) error
	ListMembers(ctx context.Context, roomID string) ([]Member, error)
	SetReady(ctx context.Context, roomID string, userID int64, ready bool) error
	SetAway(ctx context.Context, roomID string, userID int64, away bool) error
	ResetReady(ctx context.Context, roomID string) error

	CreatePlay(ctx context.Context, p Play) error
	GetPlay(ctx context.Context, id string) (Play, error)
	LockPlay(ctx context.Context, id string) (Play, error)
	GetRunningPlayByRoom(ctx context.Context, roomID string) (Play, error)
	SavePlay(ctx context.Context, p Play) error
	ListDuePlays(ctx context.Context, now time.Time, limit int) ([]Play, error)

	AppendEvents(ctx context.Context, events []Event) error
	ListEvents(ctx context.Context, playID string, after int64, seat int, limit int) ([]Event, error)

	HasAction(ctx context.Context, playID string, seat int, clientSeq int64) (bool, error)
	SaveAction(ctx context.Context, playID string, seat int, clientSeq int64, actionType string) error

	InsertSettlements(ctx context.Context, rows []Settlement) error
	ListSettlementsByUser(ctx context.Context, userID int64, gameCode string, page, pageSize int) ([]Settlement, error)
	ListSettlementsByPlay(ctx context.Context, playID string) ([]Settlement, error)
	UserInPlay(ctx context.Context, playID string, userID int64) (bool, error)
}
