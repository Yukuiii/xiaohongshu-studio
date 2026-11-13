package xiaohongshu

import (
	"testing"

	"github.com/stretchr/testify/require"
	xhserrors "github.com/xpzouying/xiaohongshu-mcp/errors"
)

func TestParseCurrentUser(t *testing.T) {
	cases := []struct {
		name    string
		page    string
		want    *CurrentUser
		wantErr error
		message string
	}{
		{
			name: "直接返回身份对象",
			page: bodyHTML(`{"user":{"userInfo":{"user_id":"5c678e9b","nickname":"苏铭","guest":false}}}`),
			want: &CurrentUser{Nickname: "苏铭", UserID: "5c678e9b"},
		},
		{
			name: "兼容 value 包装",
			page: bodyHTML(`{"user":{"userInfo":{"value":{"userId":"user-value","nickname":"作者"}}}}`),
			want: &CurrentUser{Nickname: "作者", UserID: "user-value"},
		},
		{
			name:    "游客状态需要登录",
			page:    bodyHTML(`{"user":{"userInfo":{"guest":true}}}`),
			wantErr: xhserrors.ErrLoginRequired,
		},
		{
			name:    "缺少用户标识不能当成已登录",
			page:    bodyHTML(`{"user":{"userInfo":{"guest":false,"nickname":"未知"}}}`),
			message: "无法确认小红书账号身份",
		},
		{
			name:    "缺少身份数据",
			page:    bodyHTML(`{"user":{}}`),
			message: "页面缺少账号身份",
		},
		{
			name:    "缺少初始化数据",
			page:    "<html></html>",
			message: "缺少初始化数据",
		},
		{
			name:    "身份数据格式错误",
			page:    bodyHTML(`{"user":{"userInfo":"broken"}}`),
			message: "解析账号身份失败",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCurrentUser([]byte(tc.page))
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			if tc.message != "" {
				require.ErrorContains(t, err, tc.message)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}
