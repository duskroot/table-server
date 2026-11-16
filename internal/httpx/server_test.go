package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/cloudwego/hertz/pkg/route"

	"table/internal/actor"
	"table/internal/errcode"
	"table/internal/play"
	"table/internal/record"
	"table/internal/room"
	"table/internal/session"
	"table/internal/store"
	"table/internal/wechat"
)

func TestEnvelopeAndHand(t *testing.T) {
	repo := store.NewMemory()
	hub := actor.New()
	sessions := session.New(repo, session.NewMemory(), wechat.Client{}, time.Hour)
	engine := New(Deps{
		Session: sessions,
		Room:    room.New(repo, hub, nil),
		Play:    play.New(repo, hub, nil),
		Record:  record.New(repo),
		Dev:     true,
		Addr:    "127.0.0.1:0",
	}).Engine

	health := call(t, engine, "/table/v1/session/DescribeHealth", "", `{}`)
	if health.Err != nil || health.Meta.Service != "Session" || health.Meta.Action != "DescribeHealth" || health.Meta.Version != "v1" || health.Meta.RequestId == "" {
		t.Fatalf("health metadata: %+v err=%v", health.Meta, health.Err)
	}

	missing := call(t, engine, "/table/v1/room/ListRoom", "", `{}`)
	if missing.Err == nil || missing.Err.Code != errcode.ErrUnauthenticated.Code {
		t.Fatalf("missing token: %+v", missing.Err)
	}
	unknown := call(t, engine, "/table/v1/room/NoSuch", "x", `{}`)
	if unknown.Err == nil || unknown.Err.Code != "100003" {
		t.Fatalf("unknown action: %+v", unknown.Err)
	}
	bad := call(t, engine, "/table/v1/session/LoginByWechat", "", `{`)
	if bad.Err == nil || bad.Err.Code != "100002" {
		t.Fatalf("bad json: %+v", bad.Err)
	}

	a := login(t, engine, "code-a", "甲")
	b := login(t, engine, "code-b", "乙")
	created := call(t, engine, "/table/v1/room/CreateRoom", a.Token, `{"GameCode":"fake","MaxSeats":2}`)
	var table room.RoomView
	created.ResultInto(t, &table)
	if table.Code == "" || len(table.Members) != 1 {
		t.Fatalf("create: %+v", table)
	}

	early := call(t, engine, "/table/v1/play/StartPlay", a.Token, `{"RoomId":"`+table.RoomId+`"}`)
	if early.Err == nil || early.Err.Code != errcode.ErrNotReady.Code {
		t.Fatalf("start before ready: %+v", early.Err)
	}
	call(t, engine, "/table/v1/room/JoinRoom", b.Token, `{"Code":"`+table.Code+`"}`).NoError(t)
	call(t, engine, "/table/v1/room/SetReady", a.Token, `{"RoomId":"`+table.RoomId+`","Ready":true}`).NoError(t)
	call(t, engine, "/table/v1/room/SetReady", b.Token, `{"RoomId":"`+table.RoomId+`","Ready":true}`).NoError(t)

	started := call(t, engine, "/table/v1/play/StartPlay", a.Token, `{"RoomId":"`+table.RoomId+`"}`)
	var snap play.SnapshotResult
	started.ResultInto(t, &snap)
	if snap.Phase != "tap" || snap.Seat != 1 || len(snap.Legal) != 1 || snap.Legal[0].Type != "Tap" {
		t.Fatalf("snapshot: %+v", snap)
	}
	body, _ := json.Marshal(map[string]any{
		"RoomId": table.RoomId, "PlayId": snap.PlayId, "ClientSeq": 1,
		"Action": map[string]any{"Type": "Tap"},
	})
	first := call(t, engine, "/table/v1/play/SubmitAction", a.Token, string(body))
	var submitted play.SubmitResult
	first.ResultInto(t, &submitted)
	if submitted.Idempotent || submitted.Cursor == 0 {
		t.Fatalf("submit: %+v", submitted)
	}
	second := call(t, engine, "/table/v1/play/SubmitAction", a.Token, string(body))
	var again play.SubmitResult
	second.ResultInto(t, &again)
	if !again.Idempotent || again.Cursor != submitted.Cursor {
		t.Fatalf("idempotent: %+v", again)
	}

	pulled := call(t, engine, "/table/v1/play/PullEvent", b.Token, `{"PlayId":"`+snap.PlayId+`","Cursor":0}`)
	var events play.PullResult
	pulled.ResultInto(t, &events)
	if len(events.Events) != 2 || events.Events[1].Type != "Settle" {
		t.Fatalf("events: %+v", events)
	}

	listed := call(t, engine, "/table/v1/record/ListRecord", a.Token, `{}`)
	var records record.ListResult
	listed.ResultInto(t, &records)
	if len(records.Items) != 1 || records.Items[0].Delta != 1 {
		t.Fatalf("records: %+v", records)
	}
	described := call(t, engine, "/table/v1/record/DescribeRecord", b.Token, `{"PlayId":"`+snap.PlayId+`"}`)
	var full record.DescribeResult
	described.ResultInto(t, &full)
	if len(full.Items) != 2 || full.Items[0].Delta+full.Items[1].Delta != 0 {
		t.Fatalf("describe record: %+v", full)
	}

	after := call(t, engine, "/table/v1/room/DescribeRoom", a.Token, `{"RoomId":"`+table.RoomId+`"}`)
	var againRoom room.RoomView
	after.ResultInto(t, &againRoom)
	if againRoom.Status != "open" || againRoom.PlayId != "" {
		t.Fatalf("room after hand: %+v", againRoom)
	}
	_ = context.Background()
}

type reply struct {
	Meta   meta
	Result json.RawMessage
	Err    *errcode.Error
}

type meta struct {
	RequestId string
	Action    string
	Version   string
	Service   string
}

func (r reply) NoError(t *testing.T) reply {
	t.Helper()
	if r.Err != nil {
		t.Fatalf("code %s: %s %s", r.Err.Code, r.Err.Message, r.Err.Detail)
	}
	return r
}

func (r reply) ResultInto(t *testing.T, dest any) {
	t.Helper()
	r.NoError(t)
	if err := json.Unmarshal(r.Result, dest); err != nil {
		t.Fatalf("result %s: %v", r.Result, err)
	}
}

func login(t *testing.T, engine *route.Engine, code, name string) session.LoginResult {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"Code": code, "Nickname": name})
	rep := call(t, engine, "/table/v1/session/LoginByWechat", "", string(raw))
	var out session.LoginResult
	rep.ResultInto(t, &out)
	if out.Token == "" || out.UserId == 0 {
		t.Fatalf("login: %+v", out)
	}
	return out
}

func call(t *testing.T, engine *route.Engine, path, token, body string) reply {
	t.Helper()
	buf := []byte(body)
	headers := []ut.Header{{Key: "Content-Type", Value: "application/json"}}
	if token != "" {
		headers = append(headers, ut.Header{Key: "Authorization", Value: "Bearer " + token})
	}
	w := ut.PerformRequest(engine, "POST", path, &ut.Body{Body: bytes.NewReader(buf), Len: len(buf)}, headers...)
	var packed struct {
		ResponseMetadata meta
		Result           json.RawMessage
		Error            *errcode.Error
	}
	if err := json.Unmarshal(w.Result().Body(), &packed); err != nil {
		t.Fatalf("response %s: %v", w.Result().Body(), err)
	}
	return reply{Meta: packed.ResponseMetadata, Result: packed.Result, Err: packed.Error}
}
