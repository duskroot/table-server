package session

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

type Session struct {
	Token    string
	UserID   int64
	OpenID   string
	Nickname string
	Avatar   string
}

type Store interface {
	Put(ctx context.Context, sess Session, ttl time.Duration) error
	Get(ctx context.Context, token string) (Session, error)
}

var ErrNotFound = errors.New("session not found")

type Redis struct {
	rdb *redis.Client
}

func NewRedis(rdb *redis.Client) *Redis {
	return &Redis{rdb: rdb}
}

func (s *Redis) Put(ctx context.Context, sess Session, ttl time.Duration) error {
	raw, err := json.Marshal(sess)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, "sess:"+sess.Token, raw, ttl).Err()
}

func (s *Redis) Get(ctx context.Context, token string) (Session, error) {
	raw, err := s.rdb.Get(ctx, "sess:"+token).Bytes()
	if errors.Is(err, redis.Nil) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	var sess Session
	if err := json.Unmarshal(raw, &sess); err != nil {
		return Session{}, err
	}
	return sess, nil
}

func (s *Redis) Publish(ctx context.Context, roomID string, cursor int64) error {
	return s.rdb.Publish(ctx, "room:"+roomID, cursor).Err()
}

func (s *Redis) SetOnline(ctx context.Context, roomID string, userID int64, online bool) error {
	key := "room:" + roomID + ":online"
	var err error
	if online {
		err = s.rdb.SAdd(ctx, key, userID).Err()
	} else {
		err = s.rdb.SRem(ctx, key, userID).Err()
	}
	if err != nil {
		return err
	}
	return s.rdb.Expire(ctx, key, 24*time.Hour).Err()
}

// Memory is a session store for tests.
type Memory struct {
	items map[string]Session
}

func NewMemory() *Memory {
	return &Memory{items: map[string]Session{}}
}

func (m *Memory) Put(_ context.Context, sess Session, _ time.Duration) error {
	if m.items == nil {
		m.items = map[string]Session{}
	}
	m.items[sess.Token] = sess
	return nil
}

func (m *Memory) Get(_ context.Context, token string) (Session, error) {
	sess, ok := m.items[token]
	if !ok {
		return Session{}, ErrNotFound
	}
	return sess, nil
}
