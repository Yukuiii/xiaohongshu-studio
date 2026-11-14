package accounts

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func scheduleTestTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func scheduleTestInput(accountID string) ScheduledJobInput {
	return ScheduledJobInput{
		Name: "自动创作", AccountID: accountID, Keyword: "咖啡",
		Cron: "* * * * *", Timezone: "Asia/Shanghai", Enabled: true,
	}
}

func scheduleTestStore(t *testing.T) (*Store, Account) {
	t.Helper()
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"))
	a := s.List()[0]
	require.NoError(t, s.BindIdentity(a.ID, "schedule-user", "创作者"))
	a, err := s.Get(a.ID)
	require.NoError(t, err)
	return s, a
}

func TestNextScheduleTimeCalendarAndDST(t *testing.T) {
	for _, test := range []struct {
		name, expression, timezone, after, want string
	}{
		{"strictly after minute", "* * * * *", "UTC", "2026-01-01T00:00:00Z", "2026-01-01T00:01:00Z"},
		{"timezone", "0 9 * * *", "Asia/Shanghai", "2026-01-01T00:59:30Z", "2026-01-01T01:00:00Z"},
		{"monthly missing day", "0 9 31 * *", "UTC", "2026-04-01T00:00:00Z", "2026-05-31T09:00:00Z"},
		{"leap day", "0 9 29 2 *", "UTC", "2026-01-01T00:00:00Z", "2028-02-29T09:00:00Z"},
		{"named weekday", "0 9 * * MON", "UTC", "2026-06-02T00:00:00Z", "2026-06-08T09:00:00Z"},
		{"day and weekday use cron OR", "0 9 13 * MON", "UTC", "2026-06-02T00:00:00Z", "2026-06-08T09:00:00Z"},
		{"spring gap", "30 2 * * *", "America/New_York", "2026-03-08T06:59:00Z", "2026-03-09T06:30:00Z"},
		{"autumn first minute", "30 1 * * *", "America/New_York", "2026-11-01T04:00:00Z", "2026-11-01T05:30:00Z"},
		{"autumn repeated minute", "30 1 * * *", "America/New_York", "2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z"},
	} {
		t.Run(test.name, func(t *testing.T) {
			next, err := NextScheduleTime(test.expression, test.timezone, scheduleTestTime(test.after))
			require.NoError(t, err)
			require.Equal(t, scheduleTestTime(test.want), next)
		})
	}
	for _, expression := range []string{"@daily", "0 0 9 * * *", "TZ=UTC 0 9 * * *", "61 * * * *", "0 9 31 2 *", "*/0 * * * *"} {
		_, err := NextScheduleTime(expression, "UTC", scheduleTestTime("2026-01-01T00:00:00Z"))
		require.ErrorIs(t, err, ErrInvalid, expression)
	}
	for _, timezone := range []string{"", "Local", "Unknown/Nowhere", "../UTC", "+08:00"} {
		_, err := NextScheduleTime("0 9 * * *", timezone, scheduleTestTime("2026-01-01T00:00:00Z"))
		require.ErrorIs(t, err, ErrInvalid, timezone)
	}
}

func TestScheduledJobValidationAndIdentity(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"))
	a := s.List()[0]
	ctx := context.Background()
	now := scheduleTestTime("2026-01-01T00:00:00Z")
	input := scheduleTestInput(a.ID)
	_, err := s.CreateScheduledJob(ctx, input, now)
	require.ErrorIs(t, err, ErrInvalid)
	input.Enabled = false
	input.Cron = "  0   9 * * *  "
	job, err := s.CreateScheduledJob(ctx, input, now)
	require.NoError(t, err)
	require.Nil(t, job.NextRunAt)
	require.Empty(t, job.UserID)
	require.Equal(t, "咖啡", job.Keyword)
	require.Empty(t, job.Instructions)
	require.Equal(t, "0 9 * * *", job.Cron)

	require.NoError(t, s.BindIdentity(a.ID, "bound-user", "创作者"))
	require.NoError(t, s.SetLoginStatus(a.ID, false))
	input.Enabled = true
	input.Instructions = "  面向新手，语气自然。\n不包含报价。  "
	job, err = s.UpdateScheduledJob(ctx, job.ID, input, now)
	require.NoError(t, err, "启用只要求已绑定身份，不要求当前登录")
	require.Equal(t, "bound-user", job.UserID)
	require.Equal(t, scheduleTestTime("2026-01-01T01:00:00Z"), *job.NextRunAt)
	require.Equal(t, "image", job.PublicationType)
	require.Equal(t, "面向新手，语气自然。\n不包含报价。", job.Instructions)
	encoded, err := json.Marshal(job)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "user_id")
	require.NotContains(t, string(encoded), "bound-user")
	for _, field := range []string{"topic", "reference_count"} {
		require.NotContains(t, string(encoded), `"`+field+`"`)
	}

	for _, mutate := range []func(*ScheduledJobInput){
		func(value *ScheduledJobInput) { value.Name = " " },
		func(value *ScheduledJobInput) { value.Keyword = " " },
		func(value *ScheduledJobInput) { value.PublicationType = "video" },
		func(value *ScheduledJobInput) { value.Keyword = string([]byte{0xff}) },
		func(value *ScheduledJobInput) { value.Instructions = string([]byte{0xff}) },
		func(value *ScheduledJobInput) { value.Timezone = "Local" },
	} {
		invalid := input
		mutate(&invalid)
		_, err := s.UpdateScheduledJob(ctx, job.ID, invalid, now)
		require.ErrorIs(t, err, ErrInvalid)
	}
	unchanged, err := s.ScheduledJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, job, unchanged)
	other, err := s.Create("账号 B")
	require.NoError(t, err)
	require.NoError(t, s.BindIdentity(other.ID, "other-user", "另一个身份"))
	input.AccountID = other.ID
	input.Instructions = ""
	job, err = s.UpdateScheduledJob(ctx, job.ID, input, now)
	require.NoError(t, err)
	require.Equal(t, "other-user", job.UserID)
	require.Equal(t, other.Name, job.AccountName)
	require.Empty(t, job.Instructions, "创作要求可以清空")
	_, err = s.UpdateScheduledJob(ctx, strings.Repeat("a", 32), input, now)
	require.ErrorIs(t, err, ErrScheduleNotFound)
}

func TestClaimDueSchedulesSerializesAccountsAndOccurrences(t *testing.T) {
	s, a := scheduleTestStore(t)
	ctx := context.Background()
	now := scheduleTestTime("2026-01-01T00:00:00Z")
	input := scheduleTestInput(a.ID)
	for range 2 {
		_, err := s.CreateScheduledJob(ctx, input, now)
		require.NoError(t, err)
	}
	other, err := s.Create("账号 B")
	require.NoError(t, err)
	require.NoError(t, s.BindIdentity(other.ID, "second-user", "第二个账号"))
	input.AccountID = other.ID
	_, err = s.CreateScheduledJob(ctx, input, now)
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make(chan []ScheduleRun, 8)
	errors := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runs, err := s.ClaimDueScheduleRuns(ctx, now.Add(time.Minute))
			results <- runs
			errors <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var claimed []ScheduleRun
	for runs := range results {
		claimed = append(claimed, runs...)
	}
	require.Len(t, claimed, 2)
	require.NotEqual(t, claimed[0].AccountID, claimed[1].AccountID)
	for _, run := range claimed {
		require.Equal(t, "composing", run.Stage)
		require.JSONEq(t, `[]`, string(run.Sources))
		require.Equal(t, "咖啡", run.Snapshot.Keyword)
	}
	var total, skipped int
	require.NoError(t, s.db.QueryRow(`SELECT count(*), sum(status = 'skipped') FROM schedule_runs`).Scan(&total, &skipped))
	require.Equal(t, 3, total)
	require.Equal(t, 1, skipped)

	// 活动执行持续期间只留下一个过期时点，下一次直接推进至当前时间之后。
	runs, err := s.ClaimDueScheduleRuns(ctx, now.Add(10*time.Minute))
	require.NoError(t, err)
	require.Empty(t, runs)
	require.NoError(t, s.db.QueryRow(`SELECT count(*) FROM schedule_runs`).Scan(&total))
	require.Equal(t, 6, total)
	jobs, err := s.ListScheduledJobs(ctx)
	require.NoError(t, err)
	for _, job := range jobs {
		require.Equal(t, now.Add(11*time.Minute), *job.NextRunAt)
	}
	for _, run := range claimed {
		require.NoError(t, s.FinishScheduleRun(ctx, run.ID, "succeeded", ""))
	}
	runs, err = s.ClaimDueScheduleRuns(ctx, now.Add(11*time.Minute))
	require.NoError(t, err)
	require.Len(t, runs, 2)
}

func TestScheduleClaimRollbackAndClockRewind(t *testing.T) {
	s, a := scheduleTestStore(t)
	ctx := context.Background()
	now := scheduleTestTime("2026-01-01T00:00:00Z")
	job, err := s.CreateScheduledJob(ctx, scheduleTestInput(a.ID), now)
	require.NoError(t, err)
	_, err = s.db.Exec(`CREATE TRIGGER reject_schedule_run BEFORE INSERT ON schedule_runs BEGIN SELECT RAISE(ABORT, 'injected failure'); END`)
	require.NoError(t, err)
	_, err = s.ClaimDueScheduleRuns(ctx, now.Add(time.Minute))
	require.Error(t, err)
	unchanged, err := s.ScheduledJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, job.NextRunAt, unchanged.NextRunAt)
	require.Nil(t, unchanged.LastRunAt)
	_, err = s.db.Exec(`DROP TRIGGER reject_schedule_run`)
	require.NoError(t, err)
	runs, err := s.ClaimDueScheduleRuns(ctx, now.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	require.NoError(t, s.FinishScheduleRun(ctx, runs[0].ID, "failed", "失败后不重试"))
	require.NoError(t, s.ResetScheduleTimes(ctx, now))
	runs, err = s.ClaimDueScheduleRuns(ctx, now.Add(time.Minute))
	require.NoError(t, err)
	require.Empty(t, runs)
	history, total, err := s.ListScheduleRuns(ctx, job.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, "failed", history[0].Status)
	advanced, err := s.ScheduledJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, now.Add(2*time.Minute), *advanced.NextRunAt)
}

func TestScheduleSnapshotDraftAndRestart(t *testing.T) {
	s, a := scheduleTestStore(t)
	ctx := context.Background()
	now := scheduleTestTime("2026-01-01T00:00:00Z")
	input := scheduleTestInput(a.ID)
	job, err := s.CreateScheduledJob(ctx, input, now)
	require.NoError(t, err)
	runs, err := s.ClaimDueScheduleRuns(ctx, now.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	run := runs[0]
	progress := ScheduleRunProgress{
		Stage: "publishing", Title: "保留的标题", Content: "保留的完整正文", Tags: []string{"咖啡"},
		Sources:       json.RawMessage(`[{"feed_id":"ref","title":"参考","content":"完整参考正文","url":"https://example.com/ref","author":"作者","error":""}]`),
		PublishTaskID: "publication-id",
		ImagePrompt:   "手冲咖啡配图", Images: []string{"managed-image"},
	}
	require.NoError(t, s.UpdateScheduleRun(ctx, run.ID, progress))
	_, err = s.db.Exec(`UPDATE schedule_runs SET snapshot = json_set(snapshot, '$.topic', '旧选题', '$.instructions', '旧要求', '$.reference_count', 5) WHERE id = ?`, run.ID)
	require.NoError(t, err)
	input.Keyword = "后续关键词"
	input.Instructions = "后续创作要求"
	input.Enabled = false
	_, err = s.UpdateScheduledJob(ctx, job.ID, input, now.Add(2*time.Minute))
	require.NoError(t, err)
	require.ErrorIs(t, s.DeleteScheduledJob(ctx, job.ID), ErrScheduleActive)
	require.ErrorIs(t, s.Delete(a.ID), ErrScheduleActive)
	require.NoError(t, s.Close())
	require.NoError(t, s.db.Close())
	reopenedDatabase := openDatabase(t, s.Root())
	reopened, err := Open(s.Root(), reopenedDatabase)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	require.NoError(t, reopened.ResetScheduleTimes(ctx, now.Add(48*time.Hour)))
	unfinished, err := reopened.UnfinishedScheduleRuns(ctx)
	require.NoError(t, err)
	require.Len(t, unfinished, 1)
	recovered := unfinished[0]
	require.Equal(t, "publishing", recovered.Status)
	require.Equal(t, "schedule-user", recovered.Snapshot.UserID)
	require.Equal(t, "咖啡", recovered.Snapshot.Keyword)
	require.Equal(t, "旧要求", recovered.Snapshot.Instructions)
	require.Equal(t, progress.Content, recovered.Content)
	require.Equal(t, progress.Images, recovered.Images)
	require.Equal(t, progress.ImagePrompt, recovered.ImagePrompt)
	require.JSONEq(t, string(progress.Sources), string(recovered.Sources))
	encoded, err := json.Marshal(recovered)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "user_id")
	require.NotContains(t, string(encoded), "schedule-user")
	for _, field := range []string{"topic", "reference_count"} {
		require.NotContains(t, string(encoded), `"`+field+`"`)
	}

	require.NoError(t, reopened.FinishScheduleRun(ctx, run.ID, "uncertain", "已提交，结果未知"))
	require.NoError(t, reopened.FinishScheduleRun(ctx, run.ID, "failed", "迟到的失败不能覆盖终态"))
	require.NoError(t, reopened.UpdateScheduleRun(ctx, run.ID, ScheduleRunProgress{Stage: "composing"}))
	require.NoError(t, reopened.DeleteScheduledJob(ctx, job.ID))
	require.NoError(t, reopened.Delete(a.ID))
	_, err = reopened.ScheduledJob(ctx, job.ID)
	require.ErrorIs(t, err, ErrScheduleNotFound)
	history, total, err := reopened.ListScheduleRuns(ctx, job.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, "uncertain", history[0].Status)
	require.Equal(t, progress.Title, history[0].Title)
	require.Equal(t, progress.Content, history[0].Content)
	require.Equal(t, "已提交，结果未知", history[0].Error)
	require.Equal(t, a.Name, history[0].AccountName)
	require.Equal(t, "schedule-user", history[0].Snapshot.UserID)
}

func TestScheduleResetSkipsOfflineAndAccountDeletionDisables(t *testing.T) {
	s, a := scheduleTestStore(t)
	ctx := context.Background()
	now := scheduleTestTime("2026-01-01T00:00:00Z")
	job, err := s.CreateScheduledJob(ctx, scheduleTestInput(a.ID), now)
	require.NoError(t, err)
	restartedAt := now.Add(24 * time.Hour)
	require.NoError(t, s.ResetScheduleTimes(ctx, restartedAt))
	runs, err := s.ClaimDueScheduleRuns(ctx, restartedAt)
	require.NoError(t, err)
	require.Empty(t, runs)
	_, total, err := s.ListScheduleRuns(ctx, job.ID, 1, 20)
	require.NoError(t, err)
	require.Zero(t, total)
	require.NoError(t, s.Delete(a.ID))
	paused, err := s.ScheduledJob(ctx, job.ID)
	require.NoError(t, err)
	require.False(t, paused.Enabled)
	require.Nil(t, paused.NextRunAt)
	runs, err = s.ClaimDueScheduleRuns(ctx, restartedAt.Add(time.Minute))
	require.NoError(t, err)
	require.Empty(t, runs)
	_, err = s.CreateScheduledJob(ctx, scheduleTestInput(a.ID), restartedAt)
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, s.DeleteScheduledJob(ctx, job.ID))
}

func TestScheduleProgressValidationAndPagination(t *testing.T) {
	s, a := scheduleTestStore(t)
	ctx := context.Background()
	now := scheduleTestTime("2026-01-01T00:00:00Z")
	job, err := s.CreateScheduledJob(ctx, scheduleTestInput(a.ID), now)
	require.NoError(t, err)
	var lastRun ScheduleRun
	for minute := 1; minute <= 3; minute++ {
		runs, err := s.ClaimDueScheduleRuns(ctx, now.Add(time.Duration(minute)*time.Minute))
		require.NoError(t, err)
		require.Len(t, runs, 1)
		lastRun = runs[0]
		if minute < 3 {
			require.NoError(t, s.FinishScheduleRun(ctx, lastRun.ID, "succeeded", ""))
		}
	}
	for _, progress := range []ScheduleRunProgress{
		{Stage: "finished"},
		{Stage: "reading", Sources: json.RawMessage(`{}`)},
		{Stage: "reading", Sources: json.RawMessage(`null`)},
		{Stage: "reading", Sources: json.RawMessage(`[invalid]`)},
		{Stage: "reading", PublishTaskID: "task"},
	} {
		require.ErrorIs(t, s.UpdateScheduleRun(ctx, lastRun.ID, progress), ErrInvalid)
	}
	require.ErrorIs(t, s.FinishScheduleRun(ctx, lastRun.ID, "running", ""), ErrInvalid)
	require.ErrorIs(t, s.FinishScheduleRun(ctx, "missing", "failed", ""), ErrScheduleNotFound)
	require.NoError(t, s.UpdateScheduleRun(ctx, lastRun.ID, ScheduleRunProgress{Stage: "publishing", Title: "稿件", PublishTaskID: "task"}))
	require.ErrorIs(t, s.UpdateScheduleRun(ctx, lastRun.ID, ScheduleRunProgress{Stage: "reading"}), ErrInvalid)
	require.ErrorIs(t, s.UpdateScheduleRun(ctx, lastRun.ID, ScheduleRunProgress{Stage: "publishing", PublishTaskID: "other-task"}), ErrInvalid)
	require.NoError(t, s.FinishScheduleRun(ctx, lastRun.ID, "succeeded", ""))

	page, total, err := s.ListScheduleRuns(ctx, job.ID, 2, 1)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, page, 1)
	require.Equal(t, now.Add(2*time.Minute), page[0].ScheduledAt)
	page, total, err = s.ListScheduleRuns(ctx, job.ID, math.MaxInt, 100)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Empty(t, page)
	page, total, err = s.ListScheduleRuns(ctx, job.ID, 0, 0)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Len(t, page, 3)
	require.Equal(t, lastRun.ID, page[0].ID)
	_, _, err = s.ListScheduleRuns(ctx, strings.Repeat("0", 32), 1, 20)
	require.ErrorIs(t, err, ErrScheduleNotFound)
}

func TestScheduleRunPublishResultsPersistAndPaginate(t *testing.T) {
	s, a := scheduleTestStore(t)
	ctx := context.Background()
	now := scheduleTestTime("2026-01-01T00:00:00Z")
	job, err := s.CreateScheduledJob(ctx, scheduleTestInput(a.ID), now)
	require.NoError(t, err)
	other, err := s.Create("其他发布账号")
	require.NoError(t, err)
	require.NoError(t, s.BindIdentity(other.ID, "other-publisher", "其他创作者"))
	cases := []struct {
		name          string
		taskAccountID string
		publishTaskID string
		result        json.RawMessage
	}{
		{
			name: "linked", taskAccountID: a.ID,
			result: json.RawMessage(`{"status":"发布完成","note_id":"65a123456789012345678901","note_url":"https://www.xiaohongshu.com/explore/65a123456789012345678901?xsec_token=token&xsec_source=pc_publish"}`),
		},
		{
			name: "link-unavailable", taskAccountID: a.ID,
			result: json.RawMessage(`{"status":"发布完成","note_url":"","note_link_error":"发布已成功，但未能获取笔记链接"}`),
		},
		{name: "legacy-result", taskAccountID: a.ID, result: json.RawMessage(`{"status":"发布完成"}`)},
		{name: "null-result", taskAccountID: a.ID},
		{name: "unlinked"},
		{name: "missing-task", publishTaskID: "missing-publication"},
		{
			name: "other-account", taskAccountID: other.ID,
			result: json.RawMessage(`{"status":"发布完成","note_id":"65b123456789012345678902","note_url":"https://www.xiaohongshu.com/explore/65b123456789012345678902"}`),
		},
	}
	var expected []ScheduleRun
	var tasks []PublishTask
	for index, test := range cases {
		runs, err := s.ClaimDueScheduleRuns(ctx, now.Add(time.Duration(index+1)*time.Minute))
		require.NoError(t, err, test.name)
		require.Len(t, runs, 1, test.name)
		run := runs[0]
		taskID := test.publishTaskID
		var task PublishTask
		if test.taskAccountID != "" {
			protection, err := s.LoadProtection(test.taskAccountID)
			require.NoError(t, err, test.name)
			task, err = s.EnqueuePublishTask(ctx, publishTaskInput(test.taskAccountID, test.name), protection.Revision)
			require.NoError(t, err, test.name)
			taskID = task.ID
		}
		if taskID != "" {
			require.NoError(t, s.UpdateScheduleRun(ctx, run.ID, ScheduleRunProgress{Stage: "publishing", PublishTaskID: taskID}), test.name)
		}
		if task.ID != "" {
			pending, _, err := s.ListScheduleRuns(ctx, job.ID, 1, 1)
			require.NoError(t, err, test.name)
			require.Len(t, pending, 1, test.name)
			require.Nil(t, pending[0].Result, test.name)
			_, err = s.StartPublishTask(ctx, task.ID)
			require.NoError(t, err, test.name)
			task, err = s.FinishPublishTask(ctx, task.ID, TaskOutcome{Status: TaskSucceeded, Result: test.result})
			require.NoError(t, err, test.name)
			require.Equal(t, TaskSucceeded, task.Status, test.name)
			tasks = append(tasks, task)
		}
		unfinished, err := s.UnfinishedScheduleRuns(ctx)
		require.NoError(t, err, test.name)
		require.Len(t, unfinished, 1, test.name)
		require.Nil(t, unfinished[0].Result, test.name)
		require.NoError(t, s.FinishScheduleRun(ctx, run.ID, "succeeded", ""), test.name)
		run.PublishTaskID = taskID
		if test.taskAccountID == a.ID {
			run.Result = test.result
		}
		expected = append(expected, run)
	}
	checkHistory := func(store *Store) {
		t.Helper()
		for index, want := range expected {
			page, total, err := store.ListScheduleRuns(ctx, job.ID, len(expected)-index, 1)
			require.NoError(t, err, cases[index].name)
			require.Equal(t, len(expected), total)
			require.Len(t, page, 1)
			got := page[0]
			require.Equal(t, want.ID, got.ID)
			require.Equal(t, a.ID, got.AccountID)
			require.Equal(t, want.PublishTaskID, got.PublishTaskID)
			require.Equal(t, "succeeded", got.Status)
			require.Equal(t, "finished", got.Stage)
			require.Empty(t, got.Error)
			encoded, err := json.Marshal(got)
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(encoded, &fields))
			if len(want.Result) == 0 {
				require.Nil(t, got.Result, cases[index].name)
				require.NotContains(t, fields, "result", cases[index].name)
			} else {
				require.JSONEq(t, string(want.Result), string(got.Result), cases[index].name)
				require.JSONEq(t, string(want.Result), string(fields["result"]), cases[index].name)
			}
		}
		for _, want := range tasks {
			got, err := store.PublishTask(ctx, want.ID)
			require.NoError(t, err)
			require.Equal(t, want, got)
			require.Equal(t, TaskSucceeded, got.Status)
		}
	}
	checkHistory(s)
	require.NoError(t, s.Close())
	require.NoError(t, s.db.Close())
	reopened, err := Open(s.Root(), openDatabase(t, s.Root()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	checkHistory(reopened)
}

func TestScheduleRetryIdempotencyAndCurrentConfiguration(t *testing.T) {
	s, a := scheduleTestStore(t)
	ctx := context.Background()
	now := scheduleTestTime("2026-01-01T00:00:00Z")
	input := scheduleTestInput(a.ID)
	job, err := s.CreateScheduledJob(ctx, input, now)
	require.NoError(t, err)
	runs, err := s.ClaimDueScheduleRuns(ctx, now.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	source := runs[0]
	require.NoError(t, s.UpdateScheduleRun(ctx, source.ID, ScheduleRunProgress{
		Stage: "imaging", Title: "原稿标题", Content: "原稿正文", ImagePrompt: "原始图片提示词", Images: []string{"original-image"},
	}))
	require.NoError(t, s.FinishScheduleRun(ctx, source.ID, "failed", "生图模型尚未配置"))
	summary, err := s.ScheduledJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, source.ID, summary.LastRunID)
	require.Equal(t, "failed", summary.LastRunStatus)
	require.Equal(t, "生图模型尚未配置", summary.LastRunError)
	require.Empty(t, summary.ActiveRunID)
	input.Enabled = false
	input.PublicationType = "long_text"
	input.Keyword = "重试的新关键词"
	input.Instructions = "使用本次修改的创作要求"
	paused, err := s.UpdateScheduledJob(ctx, job.ID, input, source.StartedAt)
	require.NoError(t, err)
	require.Nil(t, paused.NextRunAt)

	type retryResult struct {
		run     ScheduleRun
		created bool
		err     error
	}
	results := make(chan retryResult, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			run, created, err := s.RetryScheduleRun(ctx, job.ID, source.ID, source.StartedAt)
			results <- retryResult{run, created, err}
		}()
	}
	wg.Wait()
	close(results)
	createdCount := 0
	var retry ScheduleRun
	for result := range results {
		require.NoError(t, result.err)
		if result.created {
			createdCount++
		}
		if retry.ID != "" {
			require.Equal(t, retry.ID, result.run.ID)
		}
		retry = result.run
	}
	require.Equal(t, 1, createdCount)
	require.Equal(t, source.ID, retry.RetryOfID)
	require.Equal(t, source.ScheduledAt, retry.ScheduledAt, "重试不占用普通时点的唯一索引")
	require.Equal(t, "long_text", retry.Snapshot.PublicationType)
	require.Equal(t, "重试的新关键词", retry.Snapshot.Keyword)
	require.Equal(t, input.Instructions, retry.Snapshot.Instructions)
	require.Equal(t, "composing", retry.Stage)
	require.JSONEq(t, `[]`, string(retry.Sources))
	require.False(t, retry.Snapshot.Enabled)
	require.Empty(t, retry.Images)
	require.Empty(t, retry.ImagePrompt)
	listed, err := s.ListScheduledJobs(ctx)
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, retry.ID, listed[0].LastRunID)
	require.Equal(t, retry.ID, listed[0].ActiveRunID)
	require.Equal(t, "running", listed[0].LastRunStatus)
	require.Empty(t, listed[0].LastRunError)
	require.False(t, listed[0].Enabled)
	require.Nil(t, listed[0].NextRunAt)

	require.NoError(t, s.FinishScheduleRun(ctx, retry.ID, "failed", "重试仍失败"))
	existing, created, err := s.RetryScheduleRun(ctx, job.ID, source.ID, source.StartedAt)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, retry.ID, existing.ID)
	require.Equal(t, "failed", existing.Status)
	secondRetry, created, err := s.RetryScheduleRun(ctx, job.ID, retry.ID, source.StartedAt)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, retry.ID, secondRetry.RetryOfID)
	history, total, err := s.ListScheduleRuns(ctx, job.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 3, total)
	require.Equal(t, secondRetry.ID, history[0].ID, "同毫秒的重试链按插入顺序展示")
	require.Equal(t, source.ID, history[2].ID)
	require.Equal(t, "原稿正文", history[2].Content)
	require.Equal(t, "原始图片提示词", history[2].ImagePrompt)
	require.Equal(t, []string{"original-image"}, history[2].Images)
	require.Equal(t, "image", history[2].Snapshot.PublicationType)
}

func TestScheduleRetryRejectsInvalidSourcesAndPreservesNextTime(t *testing.T) {
	for _, status := range []string{"running", "succeeded", "uncertain", "failed", "skipped"} {
		t.Run(status, func(t *testing.T) {
			s, a := scheduleTestStore(t)
			ctx := context.Background()
			now := scheduleTestTime("2026-01-01T00:00:00Z")
			job, err := s.CreateScheduledJob(ctx, scheduleTestInput(a.ID), now)
			require.NoError(t, err)
			runs, err := s.ClaimDueScheduleRuns(ctx, now.Add(time.Minute))
			require.NoError(t, err)
			require.Len(t, runs, 1)
			if status != "running" {
				require.NoError(t, s.FinishScheduleRun(ctx, runs[0].ID, status, ""))
			}
			before, err := s.ScheduledJob(ctx, job.ID)
			require.NoError(t, err)
			_, created, err := s.RetryScheduleRun(ctx, job.ID, runs[0].ID, now.Add(90*time.Second))
			if status == "failed" || status == "skipped" {
				require.NoError(t, err)
				require.True(t, created)
			} else {
				require.ErrorIs(t, err, ErrScheduleNotRetryable)
				require.False(t, created)
			}
			after, err := s.ScheduledJob(ctx, job.ID)
			require.NoError(t, err)
			require.Equal(t, before.NextRunAt, after.NextRunAt)
			require.True(t, after.Enabled)
			_, _, err = s.RetryScheduleRun(ctx, job.ID, "missing", now)
			require.ErrorIs(t, err, ErrScheduleNotFound)
		})
	}
}

func TestScheduleRetryConflictsAndTombstones(t *testing.T) {
	s, a := scheduleTestStore(t)
	ctx := context.Background()
	now := scheduleTestTime("2026-01-01T00:00:00Z")
	input := scheduleTestInput(a.ID)
	job, err := s.CreateScheduledJob(ctx, input, now)
	require.NoError(t, err)
	runs, err := s.ClaimDueScheduleRuns(ctx, now.Add(time.Minute))
	require.NoError(t, err)
	source := runs[0]
	require.NoError(t, s.FinishScheduleRun(ctx, source.ID, "failed", "失败"))
	input.Enabled = false
	_, err = s.UpdateScheduledJob(ctx, job.ID, input, now.Add(time.Minute))
	require.NoError(t, err)
	otherJob, err := s.CreateScheduledJob(ctx, scheduleTestInput(a.ID), now.Add(time.Minute))
	require.NoError(t, err)
	runs, err = s.ClaimDueScheduleRuns(ctx, now.Add(2*time.Minute))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	_, _, err = s.RetryScheduleRun(ctx, job.ID, source.ID, now.Add(2*time.Minute))
	require.ErrorIs(t, err, ErrScheduleActive)
	_, _, err = s.RetryScheduleRun(ctx, otherJob.ID, source.ID, now)
	require.ErrorIs(t, err, ErrScheduleNotFound)
	require.NoError(t, s.FinishScheduleRun(ctx, runs[0].ID, "succeeded", ""))
	_, err = s.db.Exec(`INSERT INTO publish_protection (account_id, revision, uncertain, updated_at) VALUES (?, 1, 1, 1)`, a.ID)
	require.NoError(t, err)
	_, _, err = s.RetryScheduleRun(ctx, job.ID, source.ID, now.Add(2*time.Minute))
	require.ErrorIs(t, err, ErrTaskUncertain)
	_, err = s.db.Exec(`UPDATE publish_protection SET uncertain = 0 WHERE account_id = ?`, a.ID)
	require.NoError(t, err)
	require.NoError(t, s.SetLoginStatus(a.ID, true))
	task, err := s.EnqueuePublishTask(ctx, NewPublishTask{
		AccountID: a.ID, Kind: TaskKindPublish, Title: "手动发布", Payload: json.RawMessage(`{}`), Fingerprint: "fingerprint", RequestID: "manual-request",
	}, 1)
	require.NoError(t, err)
	_, _, err = s.RetryScheduleRun(ctx, job.ID, source.ID, now.Add(2*time.Minute))
	require.ErrorIs(t, err, ErrScheduleActive)
	_, err = s.FinishPublishTask(ctx, task.ID, TaskOutcome{Status: TaskFailed, Message: "取消"})
	require.NoError(t, err)
	require.NoError(t, s.Delete(a.ID))
	_, _, err = s.RetryScheduleRun(ctx, job.ID, source.ID, now.Add(2*time.Minute))
	require.ErrorIs(t, err, ErrNotFound)
	require.NoError(t, s.DeleteScheduledJob(ctx, job.ID))
	_, _, err = s.RetryScheduleRun(ctx, job.ID, source.ID, now.Add(2*time.Minute))
	require.ErrorIs(t, err, ErrScheduleNotFound)
}

func TestScheduleActiveRunSurvivesAccountSwitch(t *testing.T) {
	s, a := scheduleTestStore(t)
	ctx := context.Background()
	now := scheduleTestTime("2026-01-01T00:00:00Z")
	input := scheduleTestInput(a.ID)
	job, err := s.CreateScheduledJob(ctx, input, now)
	require.NoError(t, err)
	runs, err := s.ClaimDueScheduleRuns(ctx, now.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	original := runs[0]
	other, err := s.Create("切换后的账号")
	require.NoError(t, err)
	require.NoError(t, s.BindIdentity(other.ID, "other-identity", "另一个账号"))
	input.AccountID = other.ID
	_, err = s.UpdateScheduledJob(ctx, job.ID, input, now.Add(time.Minute))
	require.NoError(t, err)
	runs, err = s.ClaimDueScheduleRuns(ctx, now.Add(2*time.Minute))
	require.NoError(t, err)
	require.Empty(t, runs)
	summary, err := s.ScheduledJob(ctx, job.ID)
	require.NoError(t, err)
	require.Equal(t, "skipped", summary.LastRunStatus)
	require.Equal(t, original.ID, summary.ActiveRunID)
	_, _, err = s.RetryScheduleRun(ctx, job.ID, summary.LastRunID, now.Add(2*time.Minute))
	require.ErrorIs(t, err, ErrScheduleActive)
	require.NoError(t, s.FinishScheduleRun(ctx, original.ID, "failed", "原执行失败"))
	_, _, err = s.RetryScheduleRun(ctx, job.ID, original.ID, now.Add(2*time.Minute))
	require.ErrorIs(t, err, ErrScheduleNotRetryable, "存在更新的执行，不能重试旧失败")
	retry, created, err := s.RetryScheduleRun(ctx, job.ID, summary.LastRunID, now.Add(2*time.Minute))
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, other.ID, retry.AccountID)
	require.Equal(t, "other-identity", retry.Snapshot.UserID)
}

func TestScheduleRetryRollbackAndRestart(t *testing.T) {
	s, a := scheduleTestStore(t)
	ctx := context.Background()
	now := scheduleTestTime("2026-01-01T00:00:00Z")
	job, err := s.CreateScheduledJob(ctx, scheduleTestInput(a.ID), now)
	require.NoError(t, err)
	runs, err := s.ClaimDueScheduleRuns(ctx, now.Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, runs, 1)
	source := runs[0]
	require.NoError(t, s.FinishScheduleRun(ctx, source.ID, "failed", "失败"))
	_, err = s.db.Exec(`CREATE TRIGGER reject_retry_time BEFORE UPDATE OF last_run_at ON scheduled_jobs BEGIN SELECT RAISE(ABORT, 'injected failure'); END`)
	require.NoError(t, err)
	_, created, err := s.RetryScheduleRun(ctx, job.ID, source.ID, now.Add(90*time.Second))
	require.Error(t, err)
	require.False(t, created)
	_, total, err := s.ListScheduleRuns(ctx, job.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 1, total, "更新时间失败时创建的重试也必须回滚")
	_, err = s.db.Exec(`DROP TRIGGER reject_retry_time`)
	require.NoError(t, err)
	retry, created, err := s.RetryScheduleRun(ctx, job.ID, source.ID, now.Add(90*time.Second))
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, s.Close())
	require.NoError(t, s.db.Close())
	reopened, err := Open(s.Root(), openDatabase(t, s.Root()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	replayed, created, err := reopened.RetryScheduleRun(ctx, job.ID, source.ID, now.Add(time.Hour))
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, retry.ID, replayed.ID)
	require.Equal(t, "schedule-user", replayed.Snapshot.UserID)
	require.Equal(t, "image", replayed.Snapshot.PublicationType)
	_, total, err = reopened.ListScheduleRuns(ctx, job.ID, 1, 20)
	require.NoError(t, err)
	require.Equal(t, 2, total)
}
