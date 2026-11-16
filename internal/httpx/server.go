package httpx

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"table/internal/errcode"
	"table/internal/id"
	"table/internal/play"
	"table/internal/record"
	"table/internal/room"
	"table/internal/session"
)

type Deps struct {
	Session *session.Service
	Room    *room.Service
	Play    *play.Service
	Record  *record.Service
	Dev     bool
	Addr    string
}

type handler func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error)

func New(deps Deps) *server.Hertz {
	addr := deps.Addr
	if addr == "" {
		addr = ":8080"
	}
	h := server.New(server.WithHostPorts(addr), server.WithMaxRequestBodySize(1<<20))
	routes := map[string]handler{
		"session.LoginByWechat": func(ctx context.Context, _ *session.Session, body json.RawMessage) (any, error) {
			var req session.LoginReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Session.Login(ctx, req)
		},
		"session.DescribeSession": func(ctx context.Context, sess *session.Session, _ json.RawMessage) (any, error) {
			return deps.Session.Describe(ctx, sess.UserID)
		},
		"session.DescribeHealth": func(context.Context, *session.Session, json.RawMessage) (any, error) {
			return map[string]string{"Status": "ok"}, nil
		},
		"room.CreateRoom": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req room.CreateReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Room.Create(ctx, sess.UserID, req)
		}),
		"room.UpdateRoom": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req room.UpdateReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Room.Update(ctx, sess.UserID, req)
		}),
		"room.ListRoom": authed(func(ctx context.Context, sess *session.Session, _ json.RawMessage) (any, error) {
			return deps.Room.List(ctx, sess.UserID)
		}),
		"room.DescribeRoom": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req room.RoomIdReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Room.Describe(ctx, sess.UserID, req.RoomId)
		}),
		"room.DeleteRoom": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req room.RoomIdReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Room.Delete(ctx, sess.UserID, req.RoomId)
		}),
		"room.JoinRoom": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req room.JoinReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Room.Join(ctx, sess.UserID, req.Code)
		}),
		"room.LeaveRoom": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req room.RoomIdReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Room.Leave(ctx, sess.UserID, req.RoomId)
		}),
		"room.KickMember": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req room.KickReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Room.Kick(ctx, sess.UserID, req)
		}),
		"room.TransferHost": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req room.KickReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Room.TransferHost(ctx, sess.UserID, req)
		}),
		"room.SetReady": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req room.ReadyReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Room.SetReady(ctx, sess.UserID, req)
		}),
		"play.StartPlay": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req play.StartReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Play.Start(ctx, sess.UserID, req)
		}),
		"play.SubmitAction": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req play.SubmitReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Play.Submit(ctx, sess.UserID, req)
		}),
		"play.DescribeSnapshot": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req play.PlayReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Play.Describe(ctx, sess.UserID, req)
		}),
		"play.PullEvent": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req play.PlayReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Play.Pull(ctx, sess.UserID, req)
		}),
		"play.ListLegalAction": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req play.PlayReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Play.Legal(ctx, sess.UserID, req)
		}),
		"record.ListRecord": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req record.ListReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Record.List(ctx, sess.UserID, req)
		}),
		"record.DescribeRecord": authed(func(ctx context.Context, sess *session.Session, body json.RawMessage) (any, error) {
			var req record.DescribeReq
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			return deps.Record.Describe(ctx, sess.UserID, req)
		}),
	}
	public := map[string]bool{
		"session.LoginByWechat":  true,
		"session.DescribeHealth": true,
	}
	h.POST("/table/v1/:service/:action", func(ctx context.Context, c *app.RequestContext) {
		started := time.Now()
		service := c.Param("service")
		action := c.Param("action")
		meta := metadata{
			RequestId: id.RequestID(),
			Action:    action,
			Version:   "v1",
			Service:   pascal(service),
		}
		key := service + "." + action
		fn, ok := routes[key]
		if !ok {
			writeErr(c, meta, errcode.ErrNoAction, deps.Dev)
			return
		}
		raw, err := c.Body()
		if err != nil {
			writeErr(c, meta, errcode.WithDetail(errcode.ErrBadJSON, err.Error()), deps.Dev)
			return
		}
		var sess *session.Session
		if !public[key] {
			token := bearer(c)
			if token == "" {
				writeErr(c, meta, errcode.ErrUnauthenticated, deps.Dev)
				return
			}
			found, err := deps.Session.Authenticate(ctx, token)
			if err != nil {
				writeErr(c, meta, errcode.ErrUnauthenticated, deps.Dev)
				return
			}
			sess = &found
		}
		result, err := fn(ctx, sess, raw)
		if err != nil {
			business, ok := errcode.From(err)
			if !ok {
				business = errcode.WithDetail(errcode.ErrSystem, err.Error())
			}
			slog.Info("action", "request_id", meta.RequestId, "service", service, "action", action, "code", business.Code, "ms", time.Since(started).Milliseconds())
			writeErr(c, meta, business, deps.Dev)
			return
		}
		slog.Info("action", "request_id", meta.RequestId, "service", service, "action", action, "code", "ok", "ms", time.Since(started).Milliseconds())
		writeOK(c, meta, result)
	})
	return h
}

func authed(fn handler) handler { return fn }

type metadata struct {
	RequestId string `json:"RequestId"`
	Action    string `json:"Action"`
	Version   string `json:"Version"`
	Service   string `json:"Service"`
}

func writeOK(c *app.RequestContext, meta metadata, result any) {
	if result == nil {
		result = ""
	}
	c.JSON(consts.StatusOK, map[string]any{"ResponseMetadata": meta, "Result": result})
}

func writeErr(c *app.RequestContext, meta metadata, e *errcode.Error, dev bool) {
	body := map[string]any{"Code": e.Code, "Message": e.Message}
	if e.Detail != "" && (dev || e.Code != "100001") {
		body["Detail"] = e.Detail
	}
	c.JSON(consts.StatusOK, map[string]any{"ResponseMetadata": meta, "Error": body})
}

func decode(body json.RawMessage, dest any) error {
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, dest); err != nil {
		return errcode.ErrBadJSON
	}
	return nil
}

func bearer(c *app.RequestContext) string {
	header := string(c.GetHeader("Authorization"))
	if len(header) >= 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return strings.TrimSpace(string(c.GetHeader("X-Token")))
}

func pascal(service string) string {
	if service == "" {
		return service
	}
	return strings.ToUpper(service[:1]) + service[1:]
}
