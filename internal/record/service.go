package record

import (
	"context"
	"encoding/json"
	"time"

	"table/internal/errcode"
	"table/internal/rule"
	"table/internal/store"
)

type Service struct {
	repo store.Store
}

func New(repo store.Store) *Service {
	return &Service{repo: repo}
}

type ListReq struct {
	GameCode string `json:"GameCode"`
	Page     int    `json:"Page"`
	PageSize int    `json:"PageSize"`
}

type DescribeReq struct {
	PlayId string `json:"PlayId"`
}

type Item struct {
	PlayId    string          `json:"PlayId"`
	RoomId    string          `json:"RoomId"`
	GameCode  string          `json:"GameCode"`
	Variant   string          `json:"Variant"`
	Seat      int             `json:"Seat"`
	Delta     int             `json:"Delta"`
	Detail    json.RawMessage `json:"Detail"`
	CreatedAt time.Time       `json:"CreatedAt"`
}

type ListResult struct {
	Page     int    `json:"Page"`
	PageSize int    `json:"PageSize"`
	Items    []Item `json:"Items"`
}

type DescribeResult struct {
	PlayId   string `json:"PlayId"`
	RoomId   string `json:"RoomId"`
	GameCode string `json:"GameCode"`
	Variant  string `json:"Variant"`
	Items    []Item `json:"Items"`
}

func (s *Service) List(ctx context.Context, userID int64, req ListReq) (ListResult, error) {
	page, size := req.Page, req.PageSize
	if page < 1 {
		page = 1
	}
	if size <= 0 || size > 50 {
		size = 20
	}
	rows, err := s.repo.ListSettlementsByUser(ctx, userID, req.GameCode, page, size)
	if err != nil {
		return ListResult{}, err
	}
	return ListResult{Page: page, PageSize: size, Items: toItems(rows)}, nil
}

func (s *Service) Describe(ctx context.Context, userID int64, req DescribeReq) (DescribeResult, error) {
	if req.PlayId == "" {
		return DescribeResult{}, errcode.ErrNoRecord
	}
	ok, err := s.repo.UserInPlay(ctx, req.PlayId, userID)
	if err != nil {
		return DescribeResult{}, err
	}
	if !ok {
		return DescribeResult{}, errcode.ErrNoRecord
	}
	rows, err := s.repo.ListSettlementsByPlay(ctx, req.PlayId)
	if err != nil {
		return DescribeResult{}, err
	}
	items := toItems(rows)
	out := DescribeResult{PlayId: req.PlayId, Items: items}
	if len(rows) > 0 {
		out.RoomId = rows[0].RoomID
		out.GameCode = rows[0].GameCode
		out.Variant = rows[0].Variant
	}
	return out, nil
}

func toItems(rows []store.Settlement) []Item {
	items := make([]Item, 0, len(rows))
	for _, row := range rows {
		items = append(items, Item{
			PlayId: row.PlayID, RoomId: row.RoomID, GameCode: row.GameCode, Variant: row.Variant,
			Seat: row.Seat, Delta: row.Delta, Detail: rule.Normalize(row.Detail), CreatedAt: row.CreatedAt,
		})
	}
	return items
}
