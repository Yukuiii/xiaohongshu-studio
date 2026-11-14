package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
)

func scheduleTestServer(t *testing.T, execute func(context.Context, *accountRuntime, accounts.PublishTask) (json.RawMessage, error)) (*AppServer, accounts.Account, context.Context) {
	t.Helper()
	server, account := taskTestServer(t, execute)
	ctx, cancel := context.WithCancel(context.Background())
	server.schedules.interval = time.Hour
	require.NoError(t, server.schedules.Start(ctx))
	t.Cleanup(func() { cancel(); server.schedules.Stop() })
	return server, account, ctx
}

func testScheduledJob(t *testing.T, server *AppServer, accountID string, due time.Time) accounts.ScheduledJob {
	t.Helper()
	job, err := server.accounts.store.CreateScheduledJob(context.Background(), accounts.ScheduledJobInput{
		Name: "自动生活笔记", AccountID: accountID, Keyword: "早餐", Instructions: "面向\"上班族\"\n保留 {{keyword}} 字样，语气自然。",
		Cron: "* * * * *", Timezone: "Asia/Shanghai", Enabled: true,
	}, due.Add(-time.Minute))
	require.NoError(t, err)
	return job
}

func awaitScheduleStatus(t *testing.T, server *AppServer, jobID, status string) accounts.ScheduleRun {
	t.Helper()
	var result accounts.ScheduleRun
	require.Eventually(t, func() bool {
		if err := server.schedules.reconcile(context.Background(), false); err != nil {
			return false
		}
		runs, _, err := server.accounts.store.ListScheduleRuns(context.Background(), jobID, 1, 20)
		if err != nil || len(runs) != 1 {
			return false
		}
		result = runs[0]
		return string(result.Status) == status
	}, 3*time.Second, 5*time.Millisecond)
	return result
}

func TestScheduledKeywordCreationPersistsPublishedDraft(t *testing.T) {
	var publications atomic.Int32
	server, account, ctx := scheduleTestServer(t, func(_ context.Context, _ *accountRuntime, task accounts.PublishTask) (json.RawMessage, error) {
		var request PublishRequest
		if err := json.Unmarshal(task.Payload, &request); err != nil {
			return nil, err
		}
		if request.Title != "早餐新思路" || request.Content != "提前准备早餐，留出从容的一刻。" || len(request.Images) != 1 {
			return nil, errors.New("自动发布内容或单图设置不正确")
		}
		if _, err := os.Stat(request.Images[0]); err != nil {
			return nil, err
		}
		publications.Add(1)
		return json.Marshal(PublishResponse{Title: request.Title, Content: request.Content, Status: "发布完成"})
	})
	var modelCalls atomic.Int32
	var imageCalls atomic.Int32
	imageData := base64.StdEncoding.EncodeToString(testPNG(t))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/images/generations" {
			var input struct {
				N      int    `json:"n"`
				Prompt string `json:"prompt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.N != 1 || !strings.Contains(input.Prompt, "早餐新思路") {
				t.Error("生图请求未包含笔记或图片数量不是1")
				w.WriteHeader(400)
				return
			}
			imageCalls.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]string{"b64_json": imageData}}})
			return
		}
		var input struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if len(input.Messages) != 2 {
			t.Error("模型未收到系统和用户消息")
			w.WriteHeader(400)
			return
		}
		var request map[string]string
		_, rawInput, _ := strings.Cut(input.Messages[1].Content, "```json\n")
		rawInput, _, _ = strings.Cut(rawInput, "\n```")
		if err := json.Unmarshal([]byte(rawInput), &request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if len(request) != 2 || request["keyword"] != "早餐" || request["instructions"] != "面向\"上班族\"\n保留 {{keyword}} 字样，语气自然。" {
			t.Error("模型应收到关键词与完整创作要求")
			w.WriteHeader(400)
			return
		}
		if strings.Contains(input.Messages[0].Content, "早餐") || strings.Contains(input.Messages[1].Content, "test-secret") {
			t.Error("关键词或凭据越过提示词边界")
			w.WriteHeader(400)
			return
		}
		modelCalls.Add(1)
		text, _ := json.Marshal(composedDraft{Title: "早餐新思路", Content: "提前准备早餐，留出从容的一刻。", Tags: []string{"早餐"}})
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(text)}}}})
	}))
	defer upstream.Close()
	server.settings = &settingsStore{}
	server.settings.data.Text = LLMConfig{BaseURL: upstream.URL, APIKey: "test-secret", Model: "test"}
	server.settings.data.Image = LLMConfig{BaseURL: upstream.URL, APIKey: "image-test-secret", Model: "image-test"}
	due := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	job := testScheduledJob(t, server, account.ID, due)
	require.NoError(t, server.schedules.tick(ctx, due))
	run := awaitScheduleStatus(t, server, job.ID, "succeeded")
	require.Equal(t, "早餐新思路", run.Title)
	require.Equal(t, "提前准备早餐，留出从容的一刻。", run.Content)
	require.Equal(t, []string{"早餐"}, run.Tags)
	require.NotEmpty(t, run.PublishTaskID)
	require.Len(t, run.Images, 1)
	require.JSONEq(t, `[]`, string(run.Sources))
	require.Equal(t, "早餐", run.Snapshot.Keyword)
	require.Equal(t, int32(1), modelCalls.Load())
	require.Equal(t, int32(1), publications.Load())
	require.Equal(t, int32(1), imageCalls.Load())
	require.NoError(t, server.schedules.tick(ctx, due))
	_, total, err := server.accounts.store.ListScheduleRuns(ctx, job.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 1, total, "相同时点重复检查不得重新执行")
}

func TestScheduledModelFailureDoesNotPublish(t *testing.T) {
	var publications atomic.Int32
	server, account, ctx := scheduleTestServer(t, func(context.Context, *accountRuntime, accounts.PublishTask) (json.RawMessage, error) {
		publications.Add(1)
		return nil, errors.New("不应发布")
	})
	var modelCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { modelCalls.Add(1); w.WriteHeader(500) }))
	defer upstream.Close()
	server.settings = &settingsStore{}
	server.settings.data.Text = LLMConfig{BaseURL: upstream.URL, APIKey: "test-secret", Model: "test"}
	server.settings.data.Image = LLMConfig{BaseURL: upstream.URL, APIKey: "image-test-secret", Model: "image-test"}
	due := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
	job := testScheduledJob(t, server, account.ID, due)
	require.NoError(t, server.schedules.tick(ctx, due))
	run := awaitScheduleStatus(t, server, job.ID, "failed")
	require.Contains(t, run.Error, "HTTP 500")
	require.JSONEq(t, `[]`, string(run.Sources))
	require.Empty(t, run.Title)
	require.Equal(t, int32(1), modelCalls.Load())
	require.Zero(t, publications.Load())
}

func TestScheduledRecoveryFindsAcceptedPublicationWithoutResubmitting(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		t.Run(fmt.Sprintf("accepted-%t", accepted), func(t *testing.T) {
			var publications atomic.Int32
			server, account, ctx := scheduleTestServer(t, func(context.Context, *accountRuntime, accounts.PublishTask) (json.RawMessage, error) {
				publications.Add(1)
				return json.RawMessage(`{"status":"发布完成"}`), nil
			})
			due := time.Now().UTC().Truncate(time.Minute).Add(time.Minute)
			job := testScheduledJob(t, server, account.ID, due)
			runs, err := server.accounts.store.ClaimDueScheduleRuns(ctx, due)
			require.NoError(t, err)
			require.Len(t, runs, 1)
			run := runs[0]
			require.NoError(t, server.accounts.store.UpdateScheduleRun(ctx, run.ID, accounts.ScheduleRunProgress{Stage: "composing", Title: "已保存的标题", Content: "已保存的正文", Tags: []string{}, Sources: json.RawMessage(`[]`)}))
			if accepted {
				task, err := server.tasks.SubmitContent(ctx, account.ID, scheduleRequestID(run.ID), &PublishRequest{Title: "已保存的标题", Content: "已保存的正文", Images: []string{"/tmp/image.png"}})
				require.NoError(t, err)
				awaitTaskStatus(t, server.accounts.store, task.ID, accounts.TaskSucceeded)
			}
			require.NoError(t, server.schedules.reconcile(ctx, true))
			status := "failed"
			if accepted {
				status = "succeeded"
			}
			result := awaitScheduleStatus(t, server, job.ID, status)
			require.Equal(t, "已保存的正文", result.Content)
			if accepted {
				require.Equal(t, int32(1), publications.Load())
				require.NotEmpty(t, result.PublishTaskID)
			} else {
				require.Zero(t, publications.Load())
				require.Empty(t, result.PublishTaskID)
			}
		})
	}
}
