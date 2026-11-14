package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
	xhsErrors "github.com/xpzouying/xiaohongshu-mcp/errors"
)

func taskTestServer(t *testing.T, execute func(context.Context, *accountRuntime, accounts.PublishTask) (json.RawMessage, error)) (*AppServer, accounts.Account) {
	t.Helper()
	manager := testAccountManager(t)
	account := manager.store.List()[0]
	require.NoError(t, manager.store.BindIdentity(account.ID, "task-user", "测试作者"))
	server := NewAccountAppServer(manager, "task-token")
	server.tasks.execute = execute
	require.NoError(t, server.tasks.Start(context.Background()))
	t.Cleanup(server.tasks.Stop)
	return server, account
}

func taskHTTPRequest(t *testing.T, router http.Handler, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		router.ServeHTTP(response, request)
		close(done)
	}()
	select {
	case <-done:
		return response
	case <-time.After(2 * time.Second):
		t.Fatal("任务 HTTP 请求没有及时返回")
		return nil
	}
}

func newTaskHTTPRequest(method, path, accountID, key, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer task-token")
	request.Header.Set("Content-Type", "application/json")
	if accountID != "" {
		request.Header.Set(accountHeader, accountID)
	}
	if key != "" {
		request.Header.Set(idempotencyHeader, key)
	}
	return request
}

func taskResponse(t *testing.T, response *httptest.ResponseRecorder) accounts.PublishTask {
	t.Helper()
	var payload struct {
		Data struct {
			Task accounts.PublishTask `json:"task"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	return payload.Data.Task
}

func awaitTaskStatus(t *testing.T, store *accounts.Store, id string, expected accounts.TaskStatus) accounts.PublishTask {
	t.Helper()
	var task accounts.PublishTask
	require.Eventually(t, func() bool {
		var err error
		task, err = store.PublishTask(context.Background(), id)
		return err == nil && task.Status == expected
	}, 2*time.Second, 5*time.Millisecond)
	return task
}

func TestPublishTaskHTTPReturnsBeforeExecutionAndSurvivesRequestCancellation(t *testing.T) {
	started := make(chan context.Context, 1)
	finish := make(chan struct{})
	server, account := taskTestServer(t, func(ctx context.Context, _ *accountRuntime, task accounts.PublishTask) (json.RawMessage, error) {
		started <- ctx
		select {
		case <-finish:
			return json.Marshal(PublishResponse{Title: task.Title, Status: "发布完成"})
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	_, err := server.accounts.store.Create("另一个账号")
	require.NoError(t, err)
	router := setupRoutes(server)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	body := `{"title":"后台发布","content":"正文","images":["/tmp/image.png"],"tags":[],"visibility":"公开可见"}`
	response := taskHTTPRequest(t, router, newTaskHTTPRequest(http.MethodPost, "/api/v1/publish", account.ID, "request-one", body).WithContext(ctx))
	require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
	task := taskResponse(t, response)
	require.NotEmpty(t, task.ID)
	var execution context.Context
	select {
	case execution = <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("发布任务没有开始")
	}
	cancel()
	require.NoError(t, execution.Err(), "请求结束不能取消后台任务")
	awaitTaskStatus(t, server.accounts.store, task.ID, accounts.TaskRunning)

	list := taskHTTPRequest(t, router, newTaskHTTPRequest(http.MethodGet, "/api/v1/tasks", "", "", ""))
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	require.Contains(t, list.Body.String(), task.ID, "多个账号且未选择账号时仍能查看全局任务")
	protection, err := server.accounts.store.LoadProtection(account.ID)
	require.NoError(t, err)
	require.Equal(t, task.ID, protection.ActiveTaskID)
	unlock := taskHTTPRequest(t, router, newTaskHTTPRequest(http.MethodPut, "/api/v1/publish-protection", account.ID, "", fmt.Sprintf(`{"base_revision":%d,"uncertain":false,"last_published":""}`, protection.Revision)))
	require.Equal(t, http.StatusConflict, unlock.Code)
	require.Contains(t, unlock.Body.String(), "PUBLISH_TASK_ACTIVE")

	close(finish)
	completed := awaitTaskStatus(t, server.accounts.store, task.ID, accounts.TaskSucceeded)
	require.NotNil(t, completed.FinishedAt)
	protection, err = server.accounts.store.LoadProtection(account.ID)
	require.NoError(t, err)
	require.False(t, protection.Uncertain)
	require.Empty(t, protection.ActiveTaskID)
	require.NotEmpty(t, protection.LastPublished)
}

func TestPublishTaskIdempotencyDoesNotRunTwice(t *testing.T) {
	started := make(chan struct{}, 1)
	finish := make(chan struct{})
	var calls atomic.Int32
	server, account := taskTestServer(t, func(ctx context.Context, _ *accountRuntime, _ accounts.PublishTask) (json.RawMessage, error) {
		calls.Add(1)
		started <- struct{}{}
		select {
		case <-finish:
			return json.RawMessage(`{"status":"发布完成"}`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	request := &PublishRequest{Title: "幂等发布", Content: "正文", Images: []string{"/tmp/image.png"}}
	first, err := server.tasks.SubmitImage(context.Background(), account.ID, "same-request", request)
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("任务没有执行")
	}
	second, err := server.tasks.SubmitImage(context.Background(), account.ID, "same-request", request)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	_, err = server.tasks.SubmitImage(context.Background(), account.ID, "different-request", request)
	require.ErrorIs(t, err, accounts.ErrTaskActive)
	changed := *request
	changed.Content = "不同内容"
	_, err = server.tasks.SubmitImage(context.Background(), account.ID, "same-request", &changed)
	require.Equal(t, "IDEMPOTENCY_KEY_REUSED", accountFailure(err).Code)
	_, err = server.tasks.SubmitImage(context.Background(), account.ID, "another-request", &changed)
	require.ErrorIs(t, err, accounts.ErrTaskActive)
	close(finish)
	awaitTaskStatus(t, server.accounts.store, first.ID, accounts.TaskSucceeded)
	replayed, err := server.tasks.SubmitImage(context.Background(), account.ID, "same-request", request)
	require.NoError(t, err)
	require.Equal(t, first.ID, replayed.ID)
	require.Equal(t, accounts.TaskSucceeded, replayed.Status)
	for _, key := range []string{"different-request", "new-request"} {
		_, err = server.tasks.SubmitImage(context.Background(), account.ID, key, request)
		require.Equal(t, "ALREADY_PUBLISHED", accountFailure(err).Code)
	}
	require.Equal(t, int32(1), calls.Load())
}

func TestPublishTaskWaitsForAccountAndKeepsSubmissionSnapshot(t *testing.T) {
	started := make(chan PublishRequest, 1)
	finish := make(chan struct{})
	server, account := taskTestServer(t, func(ctx context.Context, runtime *accountRuntime, task accounts.PublishTask) (json.RawMessage, error) {
		lease, err := runtime.borrowBrowserWhenReady(ctx, false)
		if err != nil {
			return nil, err
		}
		defer lease.Close()
		var request PublishRequest
		if err := json.Unmarshal(task.Payload, &request); err != nil {
			return nil, err
		}
		started <- request
		select {
		case <-finish:
			return json.RawMessage(`{"status":"发布完成"}`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	runtime, err := server.accounts.runtime(account.ID)
	require.NoError(t, err)
	held, err := runtime.borrowBrowser(context.Background(), false)
	require.NoError(t, err)
	t.Cleanup(held.Close)
	request := &PublishRequest{Title: "原始标题", Content: "原始正文", Images: []string{"/tmp/original.png"}}
	task, err := server.tasks.SubmitImage(context.Background(), account.ID, "queued-snapshot", request)
	require.NoError(t, err)
	stored, err := server.accounts.store.PublishTask(context.Background(), task.ID)
	require.NoError(t, err)
	require.Equal(t, accounts.TaskQueued, stored.Status)
	require.Error(t, runtime.maintenance(context.Background(), true), "排队任务必须保护账号及素材")
	require.Error(t, runtime.maintenance(context.Background(), false))
	request.Title = "后续编辑"
	request.Images[0] = "/tmp/changed.png"
	held.Close()
	select {
	case accepted := <-started:
		require.Equal(t, "原始标题", accepted.Title)
		require.Equal(t, []string{"/tmp/original.png"}, accepted.Images)
	case <-time.After(2 * time.Second):
		t.Fatal("浏览器释放后任务没有继续")
	}
	close(finish)
	awaitTaskStatus(t, server.accounts.store, task.ID, accounts.TaskSucceeded)
}

func TestPublishTaskFailureAndUncertainResultsKeepCorrectProtection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		panics    bool
		status    accounts.TaskStatus
		uncertain bool
	}{
		{"before-submit", errors.New("图片不存在"), false, accounts.TaskFailed, false},
		{"after-submit", fmt.Errorf("%w: 未收到跳转", xhsErrors.ErrPublishUncertain), false, accounts.TaskUncertain, true},
		{"panic", nil, true, accounts.TaskUncertain, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, account := taskTestServer(t, func(context.Context, *accountRuntime, accounts.PublishTask) (json.RawMessage, error) {
				if tc.panics {
					panic("浏览器连接中断")
				}
				return nil, tc.err
			})
			task, err := server.tasks.SubmitImage(context.Background(), account.ID, "failure", &PublishRequest{Title: "发布测试", Content: "正文", Images: []string{"/tmp/image.png"}})
			require.NoError(t, err)
			completed := awaitTaskStatus(t, server.accounts.store, task.ID, tc.status)
			require.NotEmpty(t, completed.Error)
			protection, err := server.accounts.store.LoadProtection(account.ID)
			require.NoError(t, err)
			require.Equal(t, tc.uncertain, protection.Uncertain)
			require.Empty(t, protection.ActiveTaskID)
			require.Empty(t, protection.LastPublished)
		})
	}
}

func TestPublishTaskQueuedShutdownResumesWithoutDuplicate(t *testing.T) {
	server, account := taskTestServer(t, func(context.Context, *accountRuntime, accounts.PublishTask) (json.RawMessage, error) {
		t.Error("浏览器繁忙时不应执行发布")
		return nil, errors.New("不应执行")
	})
	runtime, err := server.accounts.runtime(account.ID)
	require.NoError(t, err)
	held, err := runtime.borrowBrowser(context.Background(), false)
	require.NoError(t, err)
	t.Cleanup(held.Close)
	task, err := server.tasks.SubmitImage(context.Background(), account.ID, "resume", &PublishRequest{Title: "重启恢复", Content: "正文", Images: []string{"/tmp/image.png"}})
	require.NoError(t, err)
	server.tasks.Stop()
	stored, err := server.accounts.store.PublishTask(context.Background(), task.ID)
	require.NoError(t, err)
	require.Equal(t, accounts.TaskQueued, stored.Status)
	held.Close()
	resumed := NewPublishTaskManager(server.accounts)
	var executions atomic.Int32
	resumed.execute = func(context.Context, *accountRuntime, accounts.PublishTask) (json.RawMessage, error) {
		executions.Add(1)
		return json.RawMessage(`{"status":"发布完成"}`), nil
	}
	t.Cleanup(resumed.Stop)
	require.NoError(t, resumed.Start(context.Background()))
	awaitTaskStatus(t, server.accounts.store, task.ID, accounts.TaskSucceeded)
	_, total, err := server.accounts.store.ListPublishTasks(context.Background(), 1, 20)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, int32(1), executions.Load())
}

func TestPublishTaskLegacyFingerprintBlocksDuplicate(t *testing.T) {
	server, account := taskTestServer(t, func(context.Context, *accountRuntime, accounts.PublishTask) (json.RawMessage, error) {
		t.Error("已有成功记录不能再次发布")
		return nil, errors.New("重复发布")
	})
	legacy := `{"visibility":"公开可见","tags":[],"images":["/tmp/image.png"],"content":"正文 <b> & 内容","title":"旧记录"}`
	_, err := server.accounts.store.SaveProtection(account.ID, 0, false, legacy, nil)
	require.NoError(t, err)
	_, err = server.tasks.SubmitImage(context.Background(), account.ID, "legacy", &PublishRequest{Title: "旧记录", Content: "正文 <b> & 内容", Images: []string{"/tmp/image.png"}})
	require.Equal(t, "ALREADY_PUBLISHED", accountFailure(err).Code)
	_, total, err := server.accounts.store.ListPublishTasks(context.Background(), 1, 20)
	require.NoError(t, err)
	require.Zero(t, total)
}

func TestPublishVideoUsesAsyncTaskAndTaskEndpointsRequireAuthentication(t *testing.T) {
	server, account := taskTestServer(t, func(_ context.Context, _ *accountRuntime, task accounts.PublishTask) (json.RawMessage, error) {
		if task.Kind != accounts.TaskKindPublishVideo {
			return nil, fmt.Errorf("任务类型不匹配: %s", task.Kind)
		}
		return json.RawMessage(`{"status":"发布完成"}`), nil
	})
	router := setupRoutes(server)
	response := taskHTTPRequest(t, router, newTaskHTTPRequest(http.MethodPost, "/api/v1/publish_video", account.ID, "video", `{"title":"视频任务","content":"正文","video":"/tmp/video.mp4"}`))
	require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
	task := taskResponse(t, response)
	awaitTaskStatus(t, server.accounts.store, task.ID, accounts.TaskSucceeded)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+task.ID, nil)
	unauthorized := taskHTTPRequest(t, router, request)
	require.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	detail := taskHTTPRequest(t, router, newTaskHTTPRequest(http.MethodGet, "/api/v1/tasks/"+task.ID, "", "", ""))
	require.Equal(t, http.StatusOK, detail.Code)
	require.Equal(t, accounts.TaskSucceeded, taskResponse(t, detail).Status)
	invalidPage := taskHTTPRequest(t, router, newTaskHTTPRequest(http.MethodGet, "/api/v1/tasks?page=0", "", "", ""))
	require.Equal(t, http.StatusBadRequest, invalidPage.Code)
}
