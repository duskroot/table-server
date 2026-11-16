package errcode

import "errors"

// Error is the business error returned in the response envelope.
type Error struct {
	Code    string
	Message string
	Detail  string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Detail == "" {
		return e.Code + ": " + e.Message
	}
	return e.Code + ": " + e.Message + ": " + e.Detail
}

func New(code, message, detail string) *Error {
	return &Error{Code: code, Message: message, Detail: detail}
}

// WithDetail returns a copy so package-level sentinels stay immutable.
func WithDetail(src *Error, detail string) *Error {
	if src == nil {
		return New("100001", "系统错误", detail)
	}
	cp := *src
	cp.Detail = detail
	return &cp
}

func From(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

var (
	ErrSystem          = New("100001", "系统错误", "")
	ErrBadJSON         = New("100002", "请求体不是合法 JSON", "")
	ErrNoAction        = New("100003", "接口不存在", "")
	ErrUnauthenticated = New("200001", "未登录", "")
	ErrBadWeChat       = New("200002", "微信登录失败", "")
	ErrRoomNotFound    = New("300001", "房间不存在", "")
	ErrRoomClosed      = New("300002", "房间已关闭", "")
	ErrSeatFull        = New("300003", "座位已满", "")
	ErrNotHost         = New("300004", "仅房主可操作", "")
	ErrNotMember       = New("300005", "不在房间中", "")
	ErrPlaying         = New("300006", "对局进行中", "")
	ErrNotPlaying      = New("300007", "当前没有进行中的对局", "")
	ErrNotReady        = New("300008", "仍有玩家未准备", "")
	ErrTooFew          = New("300010", "人数不足", "")
	ErrAway            = New("300011", "你已离座", "")
	ErrNoRecord        = New("300012", "没有这条战绩", "")
	ErrBadRoom         = New("300013", "房间参数不正确", "")
	ErrBadAction       = New("400201", "当前不能这样出牌", "")
	ErrNotYourTurn     = New("400202", "还没轮到你", "")
	ErrSeatMismatch    = New("400203", "座位不匹配", "")
	ErrBadPayload      = New("400204", "动作参数不正确", "")
	ErrBadGame         = New("500001", "玩法不存在", "")
	ErrSettleMissing   = New("500003", "结算缺失", "")
)
