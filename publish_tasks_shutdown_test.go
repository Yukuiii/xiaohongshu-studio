package main

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
	xhsErrors "github.com/xpzouying/xiaohongshu-mcp/errors"
)

func TestPublishTaskShutdownClosesBrowserBeforeWaiting(t *testing.T) {
	started := make(chan *fakeAccountBrowser, 1)
	server, account := taskTestServer(t, func(ctx context.Context, runtime *accountRuntime, _ accounts.PublishTask) (json.RawMessage, error) {
		lease, err := runtime.borrowBrowser(ctx, false)
		if err != nil {
			return nil, err
		}
		defer lease.Close()
		browser := lease.accountBrowser.(*fakeAccountBrowser)
		started <- browser
		// 模拟不响应任务取消、只能随浏览器退出结束的页面请求。
		<-browser.closed
		return nil, xhsErrors.ErrPublishUncertain
	})
	server.accounts.drainTimeout = 10 * time.Millisecond
	server.accounts.checkIdentity = func(context.Context, *accountRuntime, accountBrowser) error { return nil }
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ready := make(chan struct{})
	stopped := make(chan error, 1)
	go func() {
		stopped <- server.StartContext(ctx, "127.0.0.1:0", func(net.Addr) { close(ready) })
	}()
	select {
	case <-ready:
	case err := <-stopped:
		t.Fatalf("服务未就绪便退出: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("服务未能就绪")
	}
	_, err := server.tasks.SubmitContent(context.Background(), account.ID, "shutdown", &PublishRequest{
		Title: "退出中的发布", Content: "正文", Images: []string{"/tmp/image.png"},
	})
	require.NoError(t, err)
	var browser *fakeAccountBrowser
	select {
	case browser = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("任务未能开始")
	}
	t.Cleanup(func() { require.NoError(t, browser.Shutdown(context.Background())) })
	cancel()
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("退出未能关闭浏览器并排空发布任务")
	}
	require.False(t, browser.Alive(), "释放账号数据根前必须关闭浏览器")
}
