package room

import (
	"context"
	"encoding/json"
	"errors"

	"table/internal/actor"
	"table/internal/errcode"
	"table/internal/id"
	"table/internal/rule"
	"table/internal/store"
)

type Presence interface {
	SetOnline(ctx context.Context, roomID string, userID int64, online bool) error
}

type Service struct {
	repo     store.Store
	hub      *actor.Hub
	presence Presence
}

func New(repo store.Store, hub *actor.Hub, presence Presence) *Service {
	return &Service{repo: repo, hub: hub, presence: presence}
}

type CreateReq struct {
	GameCode string          `json:"GameCode"`
	Variant  string          `json:"Variant"`
	Config   json.RawMessage `json:"Config"`
	MaxSeats int             `json:"MaxSeats"`
}

type UpdateReq struct {
	RoomId   string          `json:"RoomId"`
	GameCode string          `json:"GameCode"`
	Variant  string          `json:"Variant"`
	Config   json.RawMessage `json:"Config"`
	MaxSeats int             `json:"MaxSeats"`
}

type RoomIdReq struct {
	RoomId string `json:"RoomId"`
}

type JoinReq struct {
	Code string `json:"Code"`
}

type KickReq struct {
	RoomId string `json:"RoomId"`
	UserId int64  `json:"UserId"`
}

type ReadyReq struct {
	RoomId string `json:"RoomId"`
	Ready  bool   `json:"Ready"`
}

type MemberView struct {
	Seat     int    `json:"Seat"`
	UserId   int64  `json:"UserId"`
	Nickname string `json:"Nickname"`
	Ready    bool   `json:"Ready"`
	Away     bool   `json:"Away"`
}

type RoomView struct {
	RoomId     string          `json:"RoomId"`
	Code       string          `json:"Code"`
	HostUserId int64           `json:"HostUserId"`
	GameCode   string          `json:"GameCode"`
	Variant    string          `json:"Variant"`
	Status     string          `json:"Status"`
	Config     json.RawMessage `json:"Config"`
	MaxSeats   int             `json:"MaxSeats"`
	PlayId     string          `json:"PlayId"`
	Members    []MemberView    `json:"Members"`
}

func (s *Service) Create(ctx context.Context, userID int64, req CreateReq) (RoomView, error) {
	spec, err := normalizeRoom(req.GameCode, req.Variant, req.Config, req.MaxSeats)
	if err != nil {
		return RoomView{}, err
	}
	user, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return RoomView{}, err
	}
	for i := 0; i < 5; i++ {
		room := store.Room{
			ID: id.New("r_"), Code: id.RoomCode(), HostUserID: userID,
			GameCode: spec.game, Variant: spec.variant, Status: store.RoomOpen,
			Config: spec.config, MaxSeats: spec.maxSeats,
		}
		err := s.repo.WithTx(ctx, func(ctx context.Context) error {
			if err := s.repo.CreateRoom(ctx, room); err != nil {
				return err
			}
			return s.repo.AddMember(ctx, store.Member{RoomID: room.ID, Seat: 1, UserID: userID, Nickname: user.Nickname})
		})
		if errors.Is(err, store.ErrDuplicate) {
			continue
		}
		if err != nil {
			return RoomView{}, err
		}
		s.mark(ctx, room.ID, userID, true)
		return s.view(ctx, room.ID)
	}
	return RoomView{}, errcode.WithDetail(errcode.ErrSystem, "分配房间号失败")
}

func (s *Service) Update(ctx context.Context, userID int64, req UpdateReq) (RoomView, error) {
	if req.RoomId == "" {
		return RoomView{}, errcode.ErrBadRoom
	}
	_, err := s.hub.Call(ctx, req.RoomId, func(ctx context.Context) (any, error) {
		return nil, s.repo.WithTx(ctx, func(ctx context.Context) error {
			room, err := s.repo.GetRoom(ctx, req.RoomId)
			if err != nil {
				return mapRoomErr(err)
			}
			if room.HostUserID != userID {
				return errcode.ErrNotHost
			}
			spec, err := normalizeRoom(req.GameCode, req.Variant, req.Config, req.MaxSeats)
			if err != nil {
				return err
			}
			members, err := s.repo.ListMembers(ctx, room.ID)
			if err != nil {
				return err
			}
			if len(members) > spec.maxSeats {
				return errcode.WithDetail(errcode.ErrBadRoom, "座位数小于当前人数")
			}
			room.GameCode = spec.game
			room.Variant = spec.variant
			room.Config = spec.config
			room.MaxSeats = spec.maxSeats
			if err := s.repo.UpdateRoom(ctx, room); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return errcode.ErrPlaying
				}
				return err
			}
			return nil
		})
	})
	if err != nil {
		return RoomView{}, err
	}
	return s.view(ctx, req.RoomId)
}

func (s *Service) Join(ctx context.Context, userID int64, code string) (RoomView, error) {
	if len(code) != 6 {
		return RoomView{}, errcode.ErrRoomNotFound
	}
	room, err := s.repo.GetActiveRoomByCode(ctx, code)
	if err != nil {
		return RoomView{}, mapRoomErr(err)
	}
	user, err := s.repo.GetUser(ctx, userID)
	if err != nil {
		return RoomView{}, err
	}
	_, err = s.hub.Call(ctx, room.ID, func(ctx context.Context) (any, error) {
		return nil, s.repo.WithTx(ctx, func(ctx context.Context) error {
			current, err := s.repo.GetRoom(ctx, room.ID)
			if err != nil {
				return mapRoomErr(err)
			}
			if current.Status == store.RoomClosed {
				return errcode.ErrRoomClosed
			}
			members, err := s.repo.ListMembers(ctx, current.ID)
			if err != nil {
				return err
			}
			for _, member := range members {
				if member.UserID == userID {
					if member.Away {
						return s.repo.SetAway(ctx, current.ID, userID, false)
					}
					return nil
				}
			}
			if current.Status == store.RoomPlaying {
				return errcode.ErrPlaying
			}
			used := map[int]bool{}
			for _, member := range members {
				used[member.Seat] = true
			}
			seat := 0
			for n := 1; n <= current.MaxSeats; n++ {
				if !used[n] {
					seat = n
					break
				}
			}
			if seat == 0 {
				return errcode.ErrSeatFull
			}
			return s.repo.AddMember(ctx, store.Member{RoomID: current.ID, Seat: seat, UserID: userID, Nickname: user.Nickname})
		})
	})
	if err != nil {
		return RoomView{}, err
	}
	s.mark(ctx, room.ID, userID, true)
	return s.view(ctx, room.ID)
}

func (s *Service) Leave(ctx context.Context, userID int64, roomID string) (any, error) {
	res, err := s.mutate(ctx, roomID, func(ctx context.Context) error {
		room, member, members, err := s.loadMember(ctx, roomID, userID)
		if err != nil {
			return err
		}
		if room.Status == store.RoomPlaying {
			if member.Away {
				return nil
			}
			return s.repo.SetAway(ctx, roomID, userID, true)
		}
		if err := s.repo.RemoveMember(ctx, roomID, userID); err != nil {
			return err
		}
		left := make([]store.Member, 0, len(members))
		for _, item := range members {
			if item.UserID != userID {
				left = append(left, item)
			}
		}
		if len(left) == 0 {
			return s.repo.SetRoomStatus(ctx, roomID, store.RoomClosed, "")
		}
		if room.HostUserID == userID {
			return s.repo.SetHost(ctx, roomID, left[0].UserID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.mark(ctx, roomID, userID, false)
	return res, nil
}

func (s *Service) Kick(ctx context.Context, userID int64, req KickReq) (any, error) {
	if req.UserId == 0 || req.UserId == userID {
		return nil, errcode.ErrBadRoom
	}
	return s.mutate(ctx, req.RoomId, func(ctx context.Context) error {
		room, _, _, err := s.loadMember(ctx, req.RoomId, userID)
		if err != nil {
			return err
		}
		if room.HostUserID != userID {
			return errcode.ErrNotHost
		}
		if room.Status == store.RoomPlaying {
			return errcode.ErrPlaying
		}
		members, err := s.repo.ListMembers(ctx, room.ID)
		if err != nil {
			return err
		}
		found := false
		for _, member := range members {
			if member.UserID == req.UserId {
				found = true
				break
			}
		}
		if !found {
			return errcode.ErrNotMember
		}
		return s.repo.RemoveMember(ctx, room.ID, req.UserId)
	})
}

func (s *Service) SetReady(ctx context.Context, userID int64, req ReadyReq) (RoomView, error) {
	_, err := s.mutate(ctx, req.RoomId, func(ctx context.Context) error {
		room, member, _, err := s.loadMember(ctx, req.RoomId, userID)
		if err != nil {
			return err
		}
		if room.Status != store.RoomOpen {
			return errcode.ErrPlaying
		}
		if member.Away {
			return errcode.ErrAway
		}
		return s.repo.SetReady(ctx, room.ID, userID, req.Ready)
	})
	if err != nil {
		return RoomView{}, err
	}
	return s.view(ctx, req.RoomId)
}

func (s *Service) TransferHost(ctx context.Context, userID int64, req KickReq) (RoomView, error) {
	_, err := s.mutate(ctx, req.RoomId, func(ctx context.Context) error {
		room, _, members, err := s.loadMember(ctx, req.RoomId, userID)
		if err != nil {
			return err
		}
		if room.HostUserID != userID {
			return errcode.ErrNotHost
		}
		if room.Status == store.RoomPlaying {
			return errcode.ErrPlaying
		}
		for _, member := range members {
			if member.UserID == req.UserId {
				return s.repo.SetHost(ctx, room.ID, req.UserId)
			}
		}
		return errcode.ErrNotMember
	})
	if err != nil {
		return RoomView{}, err
	}
	return s.view(ctx, req.RoomId)
}

func (s *Service) Delete(ctx context.Context, userID int64, roomID string) (any, error) {
	return s.mutate(ctx, roomID, func(ctx context.Context) error {
		room, err := s.repo.GetRoom(ctx, roomID)
		if err != nil {
			return mapRoomErr(err)
		}
		if room.HostUserID != userID {
			return errcode.ErrNotHost
		}
		if play, err := s.repo.GetRunningPlayByRoom(ctx, roomID); err == nil {
			play.Status = store.PlayAborted
			play.Deadline = nil
			if err := s.repo.SavePlay(ctx, play); err != nil {
				return err
			}
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		return s.repo.SetRoomStatus(ctx, roomID, store.RoomClosed, "")
	})
}

func (s *Service) Describe(ctx context.Context, userID int64, roomID string) (RoomView, error) {
	if _, _, _, err := s.loadMember(ctx, roomID, userID); err != nil {
		return RoomView{}, err
	}
	return s.view(ctx, roomID)
}

func (s *Service) List(ctx context.Context, userID int64) ([]RoomView, error) {
	rooms, err := s.repo.ListRoomsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]RoomView, 0, len(rooms))
	for _, room := range rooms {
		view, err := s.view(ctx, room.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, view)
	}
	return out, nil
}

func (s *Service) mutate(ctx context.Context, roomID string, fn func(context.Context) error) (any, error) {
	if roomID == "" {
		return nil, errcode.ErrBadRoom
	}
	_, err := s.hub.Call(ctx, roomID, func(ctx context.Context) (any, error) {
		err := s.repo.WithTx(ctx, fn)
		return nil, err
	})
	if err != nil {
		return nil, err
	}
	return "", nil
}

func (s *Service) loadMember(ctx context.Context, roomID string, userID int64) (store.Room, store.Member, []store.Member, error) {
	room, err := s.repo.GetRoom(ctx, roomID)
	if err != nil {
		return store.Room{}, store.Member{}, nil, mapRoomErr(err)
	}
	members, err := s.repo.ListMembers(ctx, roomID)
	if err != nil {
		return store.Room{}, store.Member{}, nil, err
	}
	for _, member := range members {
		if member.UserID == userID {
			return room, member, members, nil
		}
	}
	return store.Room{}, store.Member{}, nil, errcode.ErrNotMember
}

func (s *Service) view(ctx context.Context, roomID string) (RoomView, error) {
	room, err := s.repo.GetRoom(ctx, roomID)
	if err != nil {
		return RoomView{}, mapRoomErr(err)
	}
	members, err := s.repo.ListMembers(ctx, roomID)
	if err != nil {
		return RoomView{}, err
	}
	view := RoomView{
		RoomId: room.ID, Code: room.Code, HostUserId: room.HostUserID,
		GameCode: room.GameCode, Variant: room.Variant, Status: room.Status,
		Config: rule.Normalize(room.Config), MaxSeats: room.MaxSeats,
		Members: make([]MemberView, 0, len(members)),
	}
	for _, member := range members {
		view.Members = append(view.Members, MemberView{
			Seat: member.Seat, UserId: member.UserID, Nickname: member.Nickname,
			Ready: member.Ready, Away: member.Away,
		})
	}
	if play, err := s.repo.GetRunningPlayByRoom(ctx, roomID); err == nil {
		view.PlayId = play.ID
	} else if !errors.Is(err, store.ErrNotFound) {
		return RoomView{}, err
	}
	return view, nil
}

func (s *Service) mark(ctx context.Context, roomID string, userID int64, online bool) {
	if s.presence == nil {
		return
	}
	_ = s.presence.SetOnline(ctx, roomID, userID, online)
}

type roomSpec struct {
	game     string
	variant  string
	config   json.RawMessage
	maxSeats int
}

func normalizeRoom(game, variant string, config json.RawMessage, maxSeats int) (roomSpec, error) {
	r, ok := rule.Get(game)
	if !ok {
		return roomSpec{}, errcode.ErrBadGame
	}
	if variant == "" {
		variant = "classic"
	}
	if len(variant) > 32 || len(game) > 32 {
		return roomSpec{}, errcode.ErrBadRoom
	}
	cfg := rule.Normalize(config)
	if !json.Valid(cfg) {
		return roomSpec{}, errcode.ErrBadRoom
	}
	if maxSeats == 0 {
		maxSeats = r.MaxSeats()
	}
	if maxSeats < r.MinSeats() || maxSeats > r.MaxSeats() {
		return roomSpec{}, errcode.WithDetail(errcode.ErrBadRoom, "座位数超出玩法范围")
	}
	return roomSpec{game: game, variant: variant, config: cfg, maxSeats: maxSeats}, nil
}

func mapRoomErr(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return errcode.ErrRoomNotFound
	}
	return err
}
