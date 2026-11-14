package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

func scheduleListResponse(t *testing.T, router http.Handler) []scheduledJobView {
	t.Helper()
	response := taskHTTPRequest(t, router, newTaskHTTPRequest(http.MethodGet, "/api/v1/schedules", "", "", ""))
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var payload struct {
		Data struct {
			Schedules []scheduledJobView `json:"schedules"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	return payload.Data.Schedules
}

func TestScheduledMissingImageModelCanBeConfiguredAndRetriedOnlyOnce(t *testing.T) {
	var publications, textCalls, imageCalls atomic.Int32
	server, account, ctx := scheduleTestServer(t, func(_ context.Context, _ *accountRuntime, task accounts.PublishTask) (json.RawMessage, error) {
		var request PublishRequest
		if err := json.Unmarshal(task.Payload, &request); err != nil {
			return nil, err
		}
		if len(request.Images) != 1 {
			return nil, fmt.Errorf("预期一张生成图片，实际 %d", len(request.Images))
		}
		publications.Add(1)
		return json.RawMessage(`{"status":"发布完成"}`), nil
	})
	png := base64.StdEncoding.EncodeToString(testPNG(t))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/images/generations" {
			imageCalls.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": png}}})
			return
		}
		textCalls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": `{"title":"重试后的笔记","content":"使用配置完成的模型自动生成。","tags":[],"image_prompt":"温暖的早餐场景"}`}}}})
	}))
	defer upstream.Close()
	server.settings = testSettingsStore(t)
	_, err := server.settings.update(settingsUpdate{Text: settingsInput(upstream.URL, "text", "text-key")})
	require.NoError(t, err)
	var searches atomic.Int32
	server.schedules.search = func(context.Context, *accountRuntime, string) (*FeedsListResponse, error) {
		searches.Add(1)
		return &FeedsListResponse{Feeds: []xiaohongshu.Feed{{ID: "note", XsecToken: "token"}}}, nil
	}
	server.schedules.bodies = func(context.Context, *accountRuntime, []FeedBodyRequest) ([]FeedBodyResult, error) {
		return []FeedBodyResult{{FeedID: "note", Title: "参考", Content: "参考完整正文"}}, nil
	}
	due := time.Now().UTC().Truncate(time.Minute)
	job := testScheduledJob(t, server, account.ID, due)
	router := setupRoutes(server)
	unauthorized := taskHTTPRequest(t, router, httptest.NewRequest(http.MethodGet, "/api/v1/schedules", nil))
	require.Equal(t, http.StatusUnauthorized, unauthorized.Code)
	listed := scheduleListResponse(t, router)
	require.Len(t, listed, 1)
	require.Contains(t, listed[0].ConfigurationError, "生图模型")
	require.NoError(t, server.schedules.tick(ctx, due))
	failed := awaitScheduleStatus(t, server, job.ID, "failed")
	require.Contains(t, failed.Error, "生图模型")
	require.Zero(t, searches.Load(), "缺少生图配置时不应浪费搜索和文字模型调用")
	require.Zero(t, textCalls.Load())
	require.Zero(t, imageCalls.Load())
	listed = scheduleListResponse(t, router)
	require.Equal(t, failed.ID, listed[0].LastRunID)
	require.Contains(t, listed[0].LastRunError, "生图模型")
	require.True(t, listed[0].RetryAllowed)
	nextRun := listed[0].NextRunAt

	settingsBody, err := json.Marshal(map[string]any{
		"text":  map[string]string{"base_url": upstream.URL, "model": "text", "api_key": "text-key"},
		"image": map[string]string{"base_url": upstream.URL, "model": "image", "api_key": "image-key"},
	})
	require.NoError(t, err)
	configured := taskHTTPRequest(t, router, newTaskHTTPRequest(http.MethodPut, "/api/v1/settings", "", "", string(settingsBody)))
	require.Equal(t, http.StatusOK, configured.Code, configured.Body.String())
	path := "/api/v1/schedules/" + job.ID + "/retry"
	body := fmt.Sprintf(`{"run_id":%q}`, failed.ID)
	var retryID string
	for range 2 {
		response := taskHTTPRequest(t, router, newTaskHTTPRequest(http.MethodPost, path, "", "", body))
		require.Equal(t, http.StatusAccepted, response.Code, response.Body.String())
		var payload struct {
			Data struct {
				Run accounts.ScheduleRun `json:"run"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
		if retryID == "" {
			retryID = payload.Data.Run.ID
		} else {
			require.Equal(t, retryID, payload.Data.Run.ID)
		}
	}
	var retried accounts.ScheduleRun
	require.Eventually(t, func() bool {
		if err := server.schedules.reconcile(ctx, false); err != nil {
			return false
		}
		runs, total, err := server.accounts.store.ListScheduleRuns(ctx, job.ID, 1, 20)
		if err != nil || total != 2 {
			return false
		}
		for _, run := range runs {
			if run.ID == retryID {
				retried = run
				return run.Status == "succeeded"
			}
		}
		return false
	}, 3*time.Second, 5*time.Millisecond)
	require.Equal(t, failed.ID, retried.RetryOfID)
	require.Equal(t, "重试后的笔记", retried.Title)
	require.Len(t, retried.Images, 1)
	require.Equal(t, int32(1), textCalls.Load())
	require.Equal(t, int32(1), imageCalls.Load())
	require.Equal(t, int32(1), publications.Load())
	listed = scheduleListResponse(t, router)
	require.Equal(t, nextRun, listed[0].NextRunAt, "手动重试不改变正常计划")
	require.Empty(t, listed[0].ConfigurationError)
	require.False(t, listed[0].RetryAllowed)
	refused := taskHTTPRequest(t, router, newTaskHTTPRequest(http.MethodPost, path, "", "", fmt.Sprintf(`{"run_id":%q}`, retried.ID)))
	require.Equal(t, http.StatusConflict, refused.Code, refused.Body.String())
}

func TestScheduledLongTextDoesNotRequireOrCallImageModel(t *testing.T) {
	content := strings.Repeat("长文内容。", 240)
	var publications atomic.Int32
	server, account, ctx := scheduleTestServer(t, func(_ context.Context, _ *accountRuntime, task accounts.PublishTask) (json.RawMessage, error) {
		var request PublishRequest
		if err := json.Unmarshal(task.Payload, &request); err != nil {
			return nil, err
		}
		if request.PublicationType != "long_text" || request.Content != content || len(request.Images) != 0 {
			return nil, fmt.Errorf("长文提交类型或完整正文不正确")
		}
		publications.Add(1)
		return json.RawMessage(`{"status":"发布完成"}`), nil
	})
	var images atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/images/generations" {
			images.Add(1)
			w.WriteHeader(500)
			return
		}
		text, _ := json.Marshal(composedDraft{Title: "一篇长文", Content: content})
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(text)}}}})
	}))
	defer upstream.Close()
	server.settings = &settingsStore{}
	server.settings.data.Text = LLMConfig{BaseURL: upstream.URL, APIKey: "key", Model: "text"}
	server.schedules.search = func(context.Context, *accountRuntime, string) (*FeedsListResponse, error) {
		return &FeedsListResponse{Feeds: []xiaohongshu.Feed{{ID: "note", XsecToken: "token"}}}, nil
	}
	server.schedules.bodies = func(context.Context, *accountRuntime, []FeedBodyRequest) ([]FeedBodyResult, error) {
		return []FeedBodyResult{{FeedID: "note", Content: "完整资料"}}, nil
	}
	due := time.Now().UTC().Truncate(time.Minute)
	job, err := server.accounts.store.CreateScheduledJob(ctx, accounts.ScheduledJobInput{Name: "长文计划", AccountID: account.ID, Keyword: "生活", Topic: "生活中的观察", Cron: "* * * * *", Timezone: "Asia/Shanghai", ReferenceCount: 20, Enabled: true, PublicationType: "long_text"}, due.Add(-time.Minute))
	require.NoError(t, err)
	require.Empty(t, server.scheduleView(job).ConfigurationError)
	require.NoError(t, server.schedules.tick(ctx, due))
	run := awaitScheduleStatus(t, server, job.ID, "succeeded")
	require.Equal(t, content, run.Content)
	require.Empty(t, run.Images)
	require.Zero(t, images.Load())
	require.Equal(t, int32(1), publications.Load())
}
