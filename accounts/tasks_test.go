package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func publishTaskStore(t *testing.T) (*Store, Account) {
	t.Helper()
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"))
	a := s.List()[0]
	require.NoError(t, s.BindIdentity(a.ID, "test-user", "测试昵称"))
	return s, a
}

func publishTaskInput(accountID, requestID string) NewPublishTask {
	payload := json.RawMessage(`{"title":"后台发布","images":["/images/example.png"]}`)
	return NewPublishTask{
		AccountID: accountID, Kind: TaskKindPublish, Title: "后台发布", Payload: payload,
		Fingerprint: string(payload), RequestID: requestID,
	}
}

func TestEnqueuePublishTaskSnapshotAndJSON(t *testing.T) {
	s, a := publishTaskStore(t)
	ctx := context.Background()
	input := publishTaskInput(a.ID, "snapshot")
	task, err := s.EnqueuePublishTask(ctx, input, 0)
	require.NoError(t, err)
	require.True(t, validID(task.ID))
	require.Equal(t, a.ID, task.AccountID)
	require.Equal(t, a.Name, task.AccountName)
	require.Equal(t, "test-user", task.UserID)
	require.Equal(t, TaskQueued, task.Status)
	require.Equal(t, int64(1), task.ProtectionRevision)
	require.NotEmpty(t, task.Message)
	require.Nil(t, task.StartedAt)
	require.Nil(t, task.FinishedAt)
	require.Equal(t, task.CreatedAt, task.UpdatedAt)
	_, err = s.Rename(a.ID, "改名后")
	require.NoError(t, err)

	loaded, err := s.PublishTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, task, loaded)
	byRequest, err := s.PublishTaskByRequest(ctx, a.ID, input.RequestID)
	require.NoError(t, err)
	require.Equal(t, task.ID, byRequest.ID)
	active, err := s.ActivePublishTask(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, task.ID, active.ID)
	protection, err := s.LoadProtection(a.ID)
	require.NoError(t, err)
	require.True(t, protection.Uncertain)
	require.Equal(t, task.ProtectionRevision, protection.Revision)
	require.Equal(t, task.ID, protection.ActiveTaskID)

	encoded, err := json.Marshal(loaded)
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &fields))
	for _, key := range []string{"payload", "fingerprint", "request_id", "user_id", "protection_revision", "error", "result"} {
		require.NotContains(t, fields, key)
	}
	require.JSONEq(t, `null`, string(fields["started_at"]))
	require.JSONEq(t, `null`, string(fields["finished_at"]))
	var timestamp string
	require.NoError(t, json.Unmarshal(fields["created_at"], &timestamp))
	_, err = time.Parse(time.RFC3339, timestamp)
	require.NoError(t, err)
	_, err = s.PublishTask(ctx, "missing")
	require.ErrorIs(t, err, ErrTaskNotFound)
	_, err = s.PublishTaskByRequest(ctx, a.ID, "missing")
	require.ErrorIs(t, err, ErrTaskNotFound)
}

func TestEnqueuePublishTaskPreconditions(t *testing.T) {
	s := openStore(t, filepath.Join(t.TempDir(), "accounts"))
	a := s.List()[0]
	ctx := context.Background()
	input := publishTaskInput(a.ID, "preconditions")
	_, err := s.EnqueuePublishTask(ctx, input, 0)
	require.ErrorIs(t, err, ErrTaskLoginRequired)
	require.NoError(t, s.BindIdentity(a.ID, "test-user", "测试"))
	require.NoError(t, s.SetLoginStatus(a.ID, false))
	_, err = s.EnqueuePublishTask(ctx, input, 0)
	require.ErrorIs(t, err, ErrTaskLoginRequired)
	require.NoError(t, s.SetLoginStatus(a.ID, true))

	for _, mutate := range []func(*NewPublishTask){
		func(input *NewPublishTask) { input.Kind = "unknown" },
		func(input *NewPublishTask) { input.Payload = json.RawMessage(`{`) },
		func(input *NewPublishTask) { input.RequestID = "" },
		func(input *NewPublishTask) { input.Fingerprint = "" },
	} {
		invalid := input
		mutate(&invalid)
		_, err := s.EnqueuePublishTask(ctx, invalid, 0)
		require.ErrorIs(t, err, ErrInvalid)
	}
	_, err = s.EnqueuePublishTask(ctx, input, -1)
	require.ErrorIs(t, err, ErrInvalid)
	protection, err := s.SaveProtection(a.ID, 0, true, "old-content", nil)
	require.NoError(t, err)
	_, err = s.EnqueuePublishTask(ctx, input, 0)
	require.ErrorIs(t, err, ErrTaskProtectionConflict)
	_, err = s.EnqueuePublishTask(ctx, input, protection.Revision)
	require.ErrorIs(t, err, ErrTaskUncertain)
	tasks, total, err := s.ListPublishTasks(ctx, 1, 20)
	require.NoError(t, err)
	require.Empty(t, tasks)
	require.Zero(t, total)
	require.NoError(t, s.Delete(a.ID))
	_, err = s.EnqueuePublishTask(ctx, input, protection.Revision)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestEnqueuePublishTaskRollsBackProtectionFailure(t *testing.T) {
	s, a := publishTaskStore(t)
	ctx := context.Background()
	_, err := s.db.Exec(`CREATE TRIGGER reject_task_protection BEFORE INSERT ON publish_protection
	 BEGIN SELECT RAISE(ABORT, 'protection write rejected'); END`)
	require.NoError(t, err)
	_, err = s.EnqueuePublishTask(ctx, publishTaskInput(a.ID, "atomic-enqueue"), 0)
	require.Error(t, err)
	_, total, err := s.ListPublishTasks(ctx, 1, 20)
	require.NoError(t, err)
	require.Zero(t, total)
	protection, err := s.LoadProtection(a.ID)
	require.NoError(t, err)
	require.Zero(t, protection.Revision)
	require.False(t, protection.Uncertain)
	require.Empty(t, protection.ActiveTaskID)
	_, err = s.db.Exec(`DROP TRIGGER reject_task_protection`)
	require.NoError(t, err)
	_, err = s.EnqueuePublishTask(ctx, publishTaskInput(a.ID, "atomic-enqueue"), 0)
	require.NoError(t, err, "失败事务不应占用幂等键")
}

func TestEnqueuePublishTaskConcurrentConflicts(t *testing.T) {
	for _, sameRequest := range []bool{true, false} {
		t.Run(fmt.Sprintf("same-request-%t", sameRequest), func(t *testing.T) {
			s, a := publishTaskStore(t)
			ctx := context.Background()
			const workers = 12
			start := make(chan struct{})
			results := make(chan error, workers)
			var wg sync.WaitGroup
			for i := range workers {
				requestID := "same-request"
				if !sameRequest {
					requestID = fmt.Sprintf("request-%d", i)
				}
				wg.Add(1)
				go func(requestID string) {
					defer wg.Done()
					<-start
					_, err := s.EnqueuePublishTask(ctx, publishTaskInput(a.ID, requestID), 0)
					results <- err
				}(requestID)
			}
			close(start)
			wg.Wait()
			close(results)
			var succeeded, conflicted int
			for err := range results {
				if err == nil {
					succeeded++
					continue
				}
				if sameRequest {
					require.ErrorIs(t, err, ErrTaskDuplicateRequest)
				} else {
					require.ErrorIs(t, err, ErrTaskActive)
				}
				conflicted++
			}
			require.Equal(t, 1, succeeded)
			require.Equal(t, workers-1, conflicted)
			_, total, err := s.ListPublishTasks(ctx, 1, 20)
			require.NoError(t, err)
			require.Equal(t, 1, total)
			protection, err := s.LoadProtection(a.ID)
			require.NoError(t, err)
			require.Equal(t, int64(1), protection.Revision)
		})
	}
}

func TestEnqueuePublishTaskAllowsIndependentAccounts(t *testing.T) {
	s, a := publishTaskStore(t)
	b, err := s.Create("另一个账号")
	require.NoError(t, err)
	require.NoError(t, s.BindIdentity(b.ID, "other-user", "另一个昵称"))
	ctx := context.Background()
	results := make(chan error, 2)
	for _, accountID := range []string{a.ID, b.ID} {
		go func(accountID string) {
			_, err := s.EnqueuePublishTask(ctx, publishTaskInput(accountID, "shared-request"), 0)
			results <- err
		}(accountID)
	}
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	for _, accountID := range []string{a.ID, b.ID} {
		task, err := s.PublishTaskByRequest(ctx, accountID, "shared-request")
		require.NoError(t, err)
		require.Equal(t, accountID, task.AccountID)
	}
}

func TestPublishTaskTransitionsAndProtection(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  TaskStatus
		started bool
	}{
		{"running-success", TaskSucceeded, true},
		{"running-failure", TaskFailed, true},
		{"running-uncertain", TaskUncertain, true},
		{"queued-failure", TaskFailed, false},
		{"queued-uncertain", TaskUncertain, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, a := publishTaskStore(t)
			ctx := context.Background()
			oldFingerprint := `{"title":"旧内容"}`
			protection, err := s.SaveProtection(a.ID, 0, false, oldFingerprint, nil)
			require.NoError(t, err)
			task, err := s.EnqueuePublishTask(ctx, publishTaskInput(a.ID, "complete"), protection.Revision)
			require.NoError(t, err)
			if test.started {
				task, err = s.StartPublishTask(ctx, task.ID)
				require.NoError(t, err)
				require.Equal(t, TaskRunning, task.Status)
				require.NotNil(t, task.StartedAt)
				_, err = s.StartPublishTask(ctx, task.ID)
				require.ErrorIs(t, err, ErrTaskStateConflict)
			}
			outcome := TaskOutcome{Status: test.status, Message: "实际完成说明", Result: json.RawMessage(`{"status":"发布完成"}`)}
			if test.status != TaskSucceeded {
				outcome.Error = "实际失败原因"
				outcome.Result = nil
			}
			finished, err := s.FinishPublishTask(ctx, task.ID, outcome)
			require.NoError(t, err)
			require.Equal(t, test.status, finished.Status)
			require.Equal(t, outcome.Message, finished.Message)
			require.Equal(t, outcome.Error, finished.Error)
			require.Equal(t, outcome.Result, finished.Result)
			require.NotNil(t, finished.FinishedAt)
			require.Equal(t, test.started, finished.StartedAt != nil)
			loaded, err := s.PublishTask(ctx, task.ID)
			require.NoError(t, err)
			require.Equal(t, finished, loaded)
			protection, err = s.LoadProtection(a.ID)
			require.NoError(t, err)
			require.Equal(t, task.ProtectionRevision+1, protection.Revision)
			require.Equal(t, test.status == TaskUncertain, protection.Uncertain)
			require.Empty(t, protection.ActiveTaskID)
			if test.status == TaskSucceeded {
				require.Equal(t, task.Fingerprint, protection.LastPublished)
			} else {
				require.Equal(t, oldFingerprint, protection.LastPublished)
			}
			_, err = s.ActivePublishTask(ctx, a.ID)
			require.ErrorIs(t, err, ErrTaskNotFound)
			_, err = s.FinishPublishTask(ctx, task.ID, outcome)
			require.ErrorIs(t, err, ErrTaskStateConflict)
			_, err = s.StartPublishTask(ctx, task.ID)
			require.ErrorIs(t, err, ErrTaskStateConflict)
			_, err = s.EnqueuePublishTask(ctx, publishTaskInput(a.ID, "complete"), protection.Revision)
			require.ErrorIs(t, err, ErrTaskDuplicateRequest, "终态任务仍占用原幂等键")
			byRequest, err := s.PublishTaskByRequest(ctx, a.ID, "complete")
			require.NoError(t, err)
			require.Equal(t, finished, byRequest)
		})
	}
}

func TestPublishTaskCASAndInvalidTransitions(t *testing.T) {
	s, a := publishTaskStore(t)
	ctx := context.Background()
	task, err := s.EnqueuePublishTask(ctx, publishTaskInput(a.ID, "cas"), 0)
	require.NoError(t, err)
	_, err = s.FinishPublishTask(ctx, task.ID, TaskOutcome{Status: TaskSucceeded})
	require.ErrorIs(t, err, ErrTaskStateConflict)
	_, err = s.FinishPublishTask(ctx, task.ID, TaskOutcome{Status: TaskRunning})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = s.FinishPublishTask(ctx, task.ID, TaskOutcome{Status: TaskFailed, Result: json.RawMessage(`{`)})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = s.StartPublishTask(ctx, "missing")
	require.ErrorIs(t, err, ErrTaskNotFound)
	_, err = s.FinishPublishTask(ctx, "missing", TaskOutcome{Status: TaskFailed})
	require.ErrorIs(t, err, ErrTaskNotFound)

	for _, operation := range []func() error{
		func() error {
			_, err := s.StartPublishTask(ctx, task.ID)
			return err
		},
		func() error {
			_, err := s.FinishPublishTask(ctx, task.ID, TaskOutcome{Status: TaskFailed})
			return err
		},
	} {
		start := make(chan struct{})
		results := make(chan error, 2)
		for range 2 {
			go func(operation func() error) {
				<-start
				results <- operation()
			}(operation)
		}
		close(start)
		first, second := <-results, <-results
		require.True(t, (first == nil && errors.Is(second, ErrTaskStateConflict)) || (second == nil && errors.Is(first, ErrTaskStateConflict)))
	}
	protection, err := s.LoadProtection(a.ID)
	require.NoError(t, err)
	require.Equal(t, int64(2), protection.Revision)
}

func TestFinishPublishTaskProtectsRevisionAndRollsBack(t *testing.T) {
	s, a := publishTaskStore(t)
	ctx := context.Background()
	task, err := s.EnqueuePublishTask(ctx, publishTaskInput(a.ID, "finish-atomic"), 0)
	require.NoError(t, err)
	_, err = s.StartPublishTask(ctx, task.ID)
	require.NoError(t, err)
	_, err = s.db.Exec(`CREATE TRIGGER reject_task_finish BEFORE UPDATE OF status ON publish_tasks
	 WHEN NEW.status = 'succeeded' BEGIN SELECT RAISE(ABORT, 'task finish rejected'); END`)
	require.NoError(t, err)
	_, err = s.FinishPublishTask(ctx, task.ID, TaskOutcome{Status: TaskSucceeded})
	require.Error(t, err)
	protection, err := s.LoadProtection(a.ID)
	require.NoError(t, err)
	require.Equal(t, task.ProtectionRevision, protection.Revision)
	require.True(t, protection.Uncertain)
	require.Empty(t, protection.LastPublished)
	loaded, err := s.PublishTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, TaskRunning, loaded.Status)
	require.Nil(t, loaded.FinishedAt)
	_, err = s.db.Exec(`DROP TRIGGER reject_task_finish`)
	require.NoError(t, err)
	_, err = s.db.Exec(`UPDATE publish_protection SET revision = revision + 1 WHERE account_id = ?`, a.ID)
	require.NoError(t, err)
	_, err = s.FinishPublishTask(ctx, task.ID, TaskOutcome{Status: TaskSucceeded})
	require.ErrorIs(t, err, ErrTaskProtectionConflict)
	loaded, err = s.PublishTask(ctx, task.ID)
	require.NoError(t, err)
	require.Equal(t, TaskRunning, loaded.Status)
	require.Nil(t, loaded.FinishedAt)
}

func TestRecoverPublishTasksAfterReopen(t *testing.T) {
	s, a := publishTaskStore(t)
	ctx := context.Background()
	running, err := s.EnqueuePublishTask(ctx, publishTaskInput(a.ID, "running"), 0)
	require.NoError(t, err)
	running, err = s.StartPublishTask(ctx, running.ID)
	require.NoError(t, err)
	b, err := s.Create("排队账号")
	require.NoError(t, err)
	require.NoError(t, s.BindIdentity(b.ID, "queued-user", "排队"))
	queued, err := s.EnqueuePublishTask(ctx, publishTaskInput(b.ID, "queued"), 0)
	require.NoError(t, err)
	require.NoError(t, s.Close())
	reopened, err := Open(s.root, s.db)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, reopened.Close()) })
	require.NoError(t, reopened.RecoverPublishTasks(ctx))
	recovered, err := reopened.PublishTask(ctx, running.ID)
	require.NoError(t, err)
	require.Equal(t, TaskUncertain, recovered.Status)
	require.Contains(t, recovered.Message, "服务中断")
	require.Contains(t, recovered.Message, "核对")
	require.NotEmpty(t, recovered.Error)
	require.Equal(t, running.StartedAt, recovered.StartedAt)
	require.NotNil(t, recovered.FinishedAt)
	protection, err := reopened.LoadProtection(a.ID)
	require.NoError(t, err)
	require.True(t, protection.Uncertain)
	require.Equal(t, running.ProtectionRevision+1, protection.Revision)
	require.Empty(t, protection.ActiveTaskID)
	_, err = reopened.StartPublishTask(ctx, running.ID)
	require.ErrorIs(t, err, ErrTaskStateConflict)
	queuedAgain, err := reopened.PublishTask(ctx, queued.ID)
	require.NoError(t, err)
	require.Equal(t, queued, queuedAgain)
	pending, err := reopened.QueuedPublishTasks(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, queued.ID, pending[0].ID)
	queuedProtection, err := reopened.LoadProtection(b.ID)
	require.NoError(t, err)
	require.Equal(t, queued.ProtectionRevision, queuedProtection.Revision)
	require.Equal(t, queued.ID, queuedProtection.ActiveTaskID)
	require.True(t, queuedProtection.Uncertain)
	require.NoError(t, reopened.RecoverPublishTasks(ctx))
	unchanged, err := reopened.LoadProtection(a.ID)
	require.NoError(t, err)
	require.Equal(t, protection, unchanged, "重复恢复不应再次推进终态任务的保护版本")
	_, err = reopened.StartPublishTask(ctx, queued.ID)
	require.NoError(t, err)
}

func TestPublishTaskPagination(t *testing.T) {
	s, a := publishTaskStore(t)
	ctx := context.Background()
	var ids []string
	var revision int64
	for i := range 5 {
		input := publishTaskInput(a.ID, fmt.Sprintf("page-%d", i))
		if i == 0 {
			input.Kind = TaskKindPublishVideo
		}
		task, err := s.EnqueuePublishTask(ctx, input, revision)
		require.NoError(t, err)
		_, err = s.FinishPublishTask(ctx, task.ID, TaskOutcome{Status: TaskFailed})
		require.NoError(t, err)
		revision += 2
		_, err = s.db.Exec(`UPDATE publish_tasks SET created_at = ? WHERE id = ?`, int64(i+1)*1000, task.ID)
		require.NoError(t, err)
		ids = append(ids, task.ID)
	}
	tasks, total, err := s.ListPublishTasks(ctx, 2, 2)
	require.NoError(t, err)
	require.Equal(t, 5, total)
	require.Len(t, tasks, 2)
	require.Equal(t, ids[2], tasks[0].ID)
	require.Equal(t, ids[1], tasks[1].ID)
	tasks, total, err = s.ListPublishTasks(ctx, 0, 0)
	require.NoError(t, err)
	require.Equal(t, 5, total)
	require.Len(t, tasks, 5)
	require.Equal(t, ids[4], tasks[0].ID)
	require.Equal(t, TaskKindPublishVideo, tasks[4].Kind)
	tasks, total, err = s.ListPublishTasks(ctx, int(^uint(0)>>1), 100)
	require.NoError(t, err)
	require.Equal(t, 5, total)
	require.NotNil(t, tasks)
	require.Empty(t, tasks)
	pending, err := s.QueuedPublishTasks(ctx)
	require.NoError(t, err)
	require.NotNil(t, pending)
	require.Empty(t, pending)
}

func TestPublishTaskPageSizeLimit(t *testing.T) {
	s, a := publishTaskStore(t)
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback() })
	for i := range 105 {
		_, err := tx.ExecContext(ctx, `INSERT INTO publish_tasks
		 (id, account_id, account_name, user_id, kind, title, payload, fingerprint, request_id, protection_revision, status, created_at, updated_at)
		 VALUES (?, ?, ?, 'test-user', 'publish', '分页内容', '{}', '{}', ?, 1, 'failed', ?, ?)`,
			fmt.Sprintf("%032x", i), a.ID, a.Name, fmt.Sprintf("page-limit-%d", i), i, i,
		)
		require.NoError(t, err)
	}
	require.NoError(t, tx.Commit())
	tasks, total, err := s.ListPublishTasks(ctx, 1, 1000)
	require.NoError(t, err)
	require.Equal(t, 105, total)
	require.Len(t, tasks, 100)
	tasks, total, err = s.ListPublishTasks(ctx, 2, 1000)
	require.NoError(t, err)
	require.Equal(t, 105, total)
	require.Len(t, tasks, 5)
}

func insertPublishedNoteHistory(t *testing.T, s *Store, a Account, task PublishTask) {
	t.Helper()
	if task.Kind == "" {
		task.Kind = TaskKindPublish
	}
	if task.Status == "" {
		task.Status = TaskSucceeded
	}
	if task.RequestID == "" {
		task.RequestID = task.ID
	}
	_, err := s.db.Exec(`INSERT INTO publish_tasks
	 (id, account_id, account_name, user_id, kind, title, payload, fingerprint, request_id,
	  protection_revision, status, created_at, updated_at, finished_at)
	 VALUES (?, ?, ?, ?, ?, ?, ?, '{}', ?, 1, ?, 1, 1, ?)`,
		task.ID, a.ID, a.Name, task.UserID, task.Kind, task.Title, string(task.Payload),
		task.RequestID, task.Status, nullableTime(task.FinishedAt))
	require.NoError(t, err)
}

func TestRecentPublishedNotesOrderAndLimit(t *testing.T) {
	s, a := publishTaskStore(t)
	ctx := context.Background()
	for _, item := range []struct {
		id         string
		kind       TaskKind
		payload    string
		requestID  string
		finishedAt int64
	}{
		{"01", TaskKindPublish, `{"content":"最早正文"}`, "manual-01", 1000},
		{"02", TaskKindPublish, `{"content":"次早正文"}`, "manual-02", 2000},
		{"03", TaskKindPublish, `{"content":"手动图文正文","images":["/images/3.png"]}`, "manual-03", 3000},
		{"04", TaskKindPublish, `{"content":"定时图文正文","images":["/images/4.png"]}`, "schedule-04", 4000},
		{"05", TaskKindPublish, `{"content":"最新中文正文\n保留换行和标点。"}`, "schedule-05", 6000},
		{"06", TaskKindPublish, `{"content":"长文正文","publication_type":"long_text"}`, "manual-06", 5000},
		{"07", TaskKindPublishVideo, `{"content":"视频介绍正文","video":"/videos/7.mp4"}`, "manual-07", 5000},
	} {
		finished := time.UnixMilli(item.finishedAt)
		insertPublishedNoteHistory(t, s, a, PublishTask{
			ID: item.id, UserID: "test-user", Title: "标题" + item.id, Kind: item.kind,
			Payload: json.RawMessage(item.payload), RequestID: item.requestID, FinishedAt: &finished,
		})
	}
	want := []PublishedNote{
		{Title: "标题05", Content: "最新中文正文\n保留换行和标点。"},
		{Title: "标题07", Content: "视频介绍正文"},
		{Title: "标题06", Content: "长文正文"},
		{Title: "标题04", Content: "定时图文正文"},
		{Title: "标题03", Content: "手动图文正文"},
	}
	for range 2 {
		notes, err := s.RecentPublishedNotes(ctx, a.ID)
		require.NoError(t, err)
		require.Equal(t, want, notes)
	}
}

func TestRecentPublishedNotesFiltersBeforeLimit(t *testing.T) {
	s, a := publishTaskStore(t)
	ctx := context.Background()
	finished := time.UnixMilli(1000)
	insertPublishedNoteHistory(t, s, a, PublishTask{
		ID: "valid", UserID: "test-user", Title: "有效标题",
		Payload: json.RawMessage(`{"content":"  中文正文\n"}`), FinishedAt: &finished,
	})
	finished = time.UnixMilli(2000)
	for i, payload := range []string{
		`{}`, `{"content":null}`, `{"content":123}`, `{"content":false}`,
		`{"content":[]}`, `{"content":{}}`, `{"content":""}`,
		`{"content":" \t\n\r\u000b\u000c\u0085\u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000"}`,
		`invalid json`,
	} {
		insertPublishedNoteHistory(t, s, a, PublishTask{
			ID: fmt.Sprintf("empty-%d", i), UserID: "test-user",
			Payload: json.RawMessage(payload), FinishedAt: &finished,
		})
	}
	for _, status := range []TaskStatus{TaskFailed, TaskUncertain, TaskQueued, TaskRunning, TaskSucceeded} {
		t.Run(string(status), func(t *testing.T) {
			task := PublishTask{
				ID: string(status), UserID: "test-user", Status: status,
				Payload: json.RawMessage(`{"content":"尚未成功完成"}`),
			}
			if status == TaskFailed || status == TaskUncertain {
				task.FinishedAt = &finished
			}
			insertPublishedNoteHistory(t, s, a, task)
			notes, err := s.RecentPublishedNotes(ctx, a.ID)
			require.NoError(t, err)
			require.Equal(t, []PublishedNote{{Title: "有效标题", Content: "  中文正文\n"}}, notes)
			_, err = s.db.Exec(`DELETE FROM publish_tasks WHERE id = ?`, task.ID)
			require.NoError(t, err)
		})
	}
}

func TestRecentPublishedNotesAccountIdentityAndClosedStore(t *testing.T) {
	s, a := publishTaskStore(t)
	ctx := context.Background()
	other, err := s.Create("另一个账号")
	require.NoError(t, err)
	require.NoError(t, s.BindIdentity(other.ID, "other-user", "其他用户"))
	finished := time.UnixMilli(1000)
	for i, item := range []struct {
		account Account
		userID  string
	}{
		{a, "test-user"}, {a, "old-user"}, {a, ""}, {other, "other-user"},
	} {
		insertPublishedNoteHistory(t, s, item.account, PublishTask{
			ID: fmt.Sprintf("identity-%d", i), UserID: item.userID, Title: item.userID,
			Payload: json.RawMessage(`{"content":"身份专属正文"}`), FinishedAt: &finished,
		})
	}
	for _, item := range []struct {
		accountID string
		title     string
	}{
		{a.ID, "test-user"}, {other.ID, "other-user"},
	} {
		notes, err := s.RecentPublishedNotes(ctx, item.accountID)
		require.NoError(t, err)
		require.Equal(t, []PublishedNote{{Title: item.title, Content: "身份专属正文"}}, notes)
	}
	for _, statement := range []string{
		`UPDATE accounts SET user_id = '' WHERE id = ?`,
		`UPDATE accounts SET user_id = 'test-user', deleted = 1 WHERE id = ?`,
	} {
		_, err := s.db.Exec(statement, a.ID)
		require.NoError(t, err)
		notes, err := s.RecentPublishedNotes(ctx, a.ID)
		require.NoError(t, err)
		require.Empty(t, notes)
	}
	notes, err := s.RecentPublishedNotes(ctx, "missing-account")
	require.NoError(t, err)
	require.Empty(t, notes)
	require.NoError(t, s.Close())
	_, err = s.RecentPublishedNotes(ctx, other.ID)
	require.ErrorIs(t, err, os.ErrClosed)
}
