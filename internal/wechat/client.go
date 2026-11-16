package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"table/internal/errcode"
)

type Identity struct {
	OpenID  string
	UnionID string
}

type Client struct {
	AppID  string
	Secret string
	HTTP   *http.Client
}

func (c Client) Exchange(ctx context.Context, code string) (Identity, error) {
	if code == "" {
		return Identity{}, errcode.WithDetail(errcode.ErrBadWeChat, "code is empty")
	}
	if c.AppID == "" || c.Secret == "" {
		return Identity{OpenID: "dev_" + code}, nil
	}
	httpc := c.HTTP
	if httpc == nil {
		httpc = &http.Client{Timeout: 5 * time.Second}
	}
	q := url.Values{}
	q.Set("appid", c.AppID)
	q.Set("secret", c.Secret)
	q.Set("js_code", code)
	q.Set("grant_type", "authorization_code")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.weixin.qq.com/sns/jscode2session?"+q.Encode(), nil)
	if err != nil {
		return Identity{}, err
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return Identity{}, errcode.WithDetail(errcode.ErrBadWeChat, err.Error())
	}
	defer resp.Body.Close()
	var body struct {
		OpenID  string `json:"openid"`
		UnionID string `json:"unionid"`
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return Identity{}, errcode.WithDetail(errcode.ErrBadWeChat, err.Error())
	}
	if body.ErrCode != 0 || body.OpenID == "" {
		return Identity{}, errcode.WithDetail(errcode.ErrBadWeChat, fmt.Sprintf("%d %s", body.ErrCode, body.ErrMsg))
	}
	return Identity{OpenID: body.OpenID, UnionID: body.UnionID}, nil
}
