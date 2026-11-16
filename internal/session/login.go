package session

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"table/internal/id"
	"table/internal/store"
	"table/internal/wechat"
)

type Service struct {
	users store.Store
	sess  Store
	wx    wechat.Client
	ttl   time.Duration
}

func New(users store.Store, sess Store, wx wechat.Client, ttl time.Duration) *Service {
	if ttl <= 0 {
		ttl = 168 * time.Hour
	}
	return &Service{users: users, sess: sess, wx: wx, ttl: ttl}
}

type LoginReq struct {
	Code     string `json:"Code"`
	Nickname string `json:"Nickname"`
	Avatar   string `json:"Avatar"`
}

type LoginResult struct {
	Token    string `json:"Token"`
	UserId   int64  `json:"UserId"`
	Nickname string `json:"Nickname"`
	OpenID   string `json:"OpenId"`
}

type Profile struct {
	UserId   int64  `json:"UserId"`
	Nickname string `json:"Nickname"`
	Avatar   string `json:"Avatar"`
	OpenID   string `json:"OpenId"`
}

func (s *Service) Login(ctx context.Context, req LoginReq) (LoginResult, error) {
	ident, err := s.wx.Exchange(ctx, strings.TrimSpace(req.Code))
	if err != nil {
		return LoginResult{}, err
	}
	user, err := s.users.UpsertUser(ctx, ident.OpenID, clean(req.Nickname), clean(req.Avatar))
	if err != nil {
		return LoginResult{}, err
	}
	token := id.New("")
	if err := s.sess.Put(ctx, Session{
		Token: token, UserID: user.ID, OpenID: user.OpenID, Nickname: user.Nickname, Avatar: user.Avatar,
	}, s.ttl); err != nil {
		return LoginResult{}, err
	}
	return LoginResult{Token: token, UserId: user.ID, Nickname: user.Nickname, OpenID: user.OpenID}, nil
}

func (s *Service) Describe(ctx context.Context, userID int64) (Profile, error) {
	user, err := s.users.GetUser(ctx, userID)
	if err != nil {
		return Profile{}, err
	}
	return Profile{UserId: user.ID, Nickname: user.Nickname, Avatar: user.Avatar, OpenID: user.OpenID}, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Session, error) {
	return s.sess.Get(ctx, token)
}

func clean(v string) string {
	v = strings.TrimSpace(v)
	out := make([]rune, 0, utf8.RuneCountInString(v))
	for _, r := range v {
		if r < 32 {
			continue
		}
		out = append(out, r)
		if len(out) >= 32 {
			break
		}
	}
	return string(out)
}
