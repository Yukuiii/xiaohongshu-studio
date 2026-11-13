package xiaohongshu

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-rod/rod/lib/proto"
	xhserrors "github.com/xpzouying/xiaohongshu-mcp/errors"
)

type userInfo struct {
	Guest     bool   `json:"guest"`
	Nickname  string `json:"nickname"`
	UserID    string `json:"userId"`
	UserIDAlt string `json:"user_id"`
}

// FetchCurrentUser 使用账号会话读取 explore 页面里的登录身份，不渲染页面。
func FetchCurrentUser(ctx context.Context, client *http.Client, userAgent string, cookies []*proto.NetworkCookie) (*CurrentUser, error) {
	session, err := NewHTTPSession(client, userAgent, cookies)
	if err != nil {
		return nil, err
	}
	page, final, err := session.Get(ctx, urlOfExplore)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(final.EscapedPath(), "/explore") {
		return nil, errors.New("探索页已跳转，无法确认账号身份")
	}
	return parseCurrentUser(page)
}

func parseCurrentUser(page []byte) (*CurrentUser, error) {
	var state struct {
		User struct {
			UserInfo json.RawMessage `json:"userInfo"`
		} `json:"user"`
	}
	if err := decodeInitialState(page, &state); err != nil {
		return nil, err
	}
	if len(state.User.UserInfo) == 0 || string(state.User.UserInfo) == "null" {
		return nil, errors.New("页面缺少账号身份")
	}

	var wrapper struct {
		Value *userInfo `json:"value"`
	}
	info := userInfo{}
	if err := json.Unmarshal(state.User.UserInfo, &wrapper); err == nil && wrapper.Value != nil {
		info = *wrapper.Value
	} else if err := json.Unmarshal(state.User.UserInfo, &info); err != nil {
		return nil, errors.New("解析账号身份失败")
	}
	if info.Guest {
		return nil, xhserrors.ErrLoginRequired
	}
	userID := info.UserID
	if userID == "" {
		userID = info.UserIDAlt
	}
	if userID == "" {
		return nil, errors.New("无法确认小红书账号身份")
	}
	return &CurrentUser{Nickname: info.Nickname, UserID: userID}, nil
}
