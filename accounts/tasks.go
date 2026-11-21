package accounts

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrTaskNotFound           = errors.New("publish task not found")
	ErrTaskActive             = errors.New("publish task already active")
	ErrTaskDuplicateRequest   = errors.New("publish task request already exists")
	ErrTaskProtectionConflict = errors.New("publish task protection revision conflict")
	ErrTaskUncertain          = errors.New("上次发布结果尚未确认，请在小红书核对后到工作台解除发布锁定")
	ErrTaskLoginRequired      = errors.New("publish task requires a bound logged-in account")
	ErrTaskStateConflict      = errors.New("publish task state conflict")
)

type TaskKind string

const (
	TaskKindPublish      TaskKind = "publish"
	TaskKindPublishVideo TaskKind = "publish_video"
)

type TaskStatus string

const (
	TaskQueued    TaskStatus = "queued"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
	TaskUncertain TaskStatus = "uncertain"
)

// PublishedNote 是连续创作参考的已发布标题和正文。
type PublishedNote struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

// PublishTask 保存提交时的账号、内容和保护版本快照。
type PublishTask struct {
	ID                 string          `json:"id"`
	AccountID          string          `json:"account_id"`
	AccountName        string          `json:"account_name"`
	Kind               TaskKind        `json:"kind"`
	Title              string          `json:"title"`
	Status             TaskStatus      `json:"status"`
	Message            string          `json:"message"`
	Error              string          `json:"error,omitempty"`
	Result             json.RawMessage `json:"result,omitempty"`
	ImageIDs           []string        `json:"image_ids,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
	StartedAt          *time.Time      `json:"started_at"`
	FinishedAt         *time.Time      `json:"finished_at"`
	Payload            json.RawMessage `json:"-"`
	Fingerprint        string          `json:"-"`
	RequestID          string          `json:"-"`
	UserID             string          `json:"-"`
	ProtectionRevision int64           `json:"-"`
}

type NewPublishTask struct {
	AccountID   string
	Kind        TaskKind
	Title       string
	Payload     json.RawMessage
	Fingerprint string
	RequestID   string
}

type TaskOutcome struct {
	Status  TaskStatus
	Message string
	Error   string
	Result  json.RawMessage
}

const taskColumns = `id, account_id, account_name, user_id, kind, title, payload,
 fingerprint, request_id, protection_revision, status, message, error, result,
 created_at, updated_at, started_at, finished_at`

// EnqueuePublishTask 在同一事务中创建任务并锁住发布保护。
func (s *Store) EnqueuePublishTask(ctx context.Context, input NewPublishTask, baseProtectionRevision int64) (PublishTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.restartPending {
		return PublishTask{}, ErrRestartPending
	}
	if _, err := s.get(input.AccountID); err != nil {
		return PublishTask{}, err
	}
	if (input.Kind != TaskKindPublish && input.Kind != TaskKindPublishVideo) ||
		!utf8.ValidString(input.Title) || !json.Valid(input.Payload) ||
		strings.TrimSpace(input.Fingerprint) == "" || !utf8.ValidString(input.Fingerprint) ||
		strings.TrimSpace(input.RequestID) == "" || !utf8.ValidString(input.RequestID) || baseProtectionRevision < 0 {
		return PublishTask{}, fmt.Errorf("%w: publish task", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishTask{}, err
	}
	defer tx.Rollback()
	a, err := scanAccount(tx.QueryRowContext(ctx, `SELECT `+accountColumns+` FROM accounts WHERE id = ? AND deleted = 0`, input.AccountID))
	if errors.Is(err, sql.ErrNoRows) {
		return PublishTask{}, ErrNotFound
	}
	if err != nil {
		return PublishTask{}, err
	}
	if a.UserID == "" || !a.IsLoggedIn {
		return PublishTask{}, ErrTaskLoginRequired
	}
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM publish_tasks WHERE account_id = ? AND request_id = ?`, input.AccountID, input.RequestID).Scan(&exists)
	if err == nil {
		return PublishTask{}, ErrTaskDuplicateRequest
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PublishTask{}, err
	}
	if err := checkNoActivePublishTask(tx, input.AccountID); err != nil {
		return PublishTask{}, err
	}
	var revision int64
	var uncertain int
	err = tx.QueryRowContext(ctx, `SELECT revision, uncertain FROM publish_protection WHERE account_id = ?`, input.AccountID).Scan(&revision, &uncertain)
	missingProtection := errors.Is(err, sql.ErrNoRows)
	if err != nil && !missingProtection {
		return PublishTask{}, err
	}
	if revision != baseProtectionRevision {
		return PublishTask{}, ErrTaskProtectionConflict
	}
	if uncertain != 0 {
		return PublishTask{}, ErrTaskUncertain
	}
	id, err := newID()
	if err != nil {
		return PublishTask{}, err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	task := PublishTask{
		ID: id, AccountID: a.ID, AccountName: a.Name, UserID: a.UserID,
		Kind: input.Kind, Title: input.Title, Payload: input.Payload,
		Fingerprint: input.Fingerprint, RequestID: input.RequestID,
		ProtectionRevision: revision + 1, Status: TaskQueued, Message: "等待发布",
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO publish_tasks (`+taskColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', NULL, ?, ?, NULL, NULL)`,
		task.ID, task.AccountID, task.AccountName, task.UserID, task.Kind, task.Title,
		string(task.Payload), task.Fingerprint, task.RequestID, task.ProtectionRevision,
		task.Status, task.Message, now.UnixMilli(), now.UnixMilli(),
	); err != nil {
		return PublishTask{}, err
	}
	if missingProtection {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO publish_protection (account_id, revision, uncertain, updated_at) VALUES (?, ?, 1, ?)`,
			task.AccountID, task.ProtectionRevision, now.UnixMilli(),
		)
	} else {
		var result sql.Result
		result, err = tx.ExecContext(ctx,
			`UPDATE publish_protection SET revision = ?, uncertain = 1, reviewed_revision = NULL, updated_at = ?
			 WHERE account_id = ? AND revision = ? AND uncertain = 0`,
			task.ProtectionRevision, now.UnixMilli(), task.AccountID, revision,
		)
		if err == nil {
			err = requireTaskChange(result, ErrTaskProtectionConflict)
		}
	}
	if err != nil {
		return PublishTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishTask{}, err
	}
	return task, nil
}

func (s *Store) PublishTask(ctx context.Context, id string) (PublishTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return PublishTask{}, os.ErrClosed
	}
	return scanPublishTask(s.db.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM publish_tasks WHERE id = ?`, id))
}

func (s *Store) PublishTaskByRequest(ctx context.Context, accountID, requestID string) (PublishTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return PublishTask{}, os.ErrClosed
	}
	return scanPublishTask(s.db.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM publish_tasks WHERE account_id = ? AND request_id = ?`, accountID, requestID,
	))
}

func (s *Store) ActivePublishTask(ctx context.Context, accountID string) (PublishTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return PublishTask{}, os.ErrClosed
	}
	return scanPublishTask(s.db.QueryRowContext(ctx,
		`SELECT `+taskColumns+` FROM publish_tasks WHERE account_id = ? AND status IN ('queued', 'running')`, accountID,
	))
}

// RecentPublishedNotes 返回当前绑定身份最近五篇成功发布的非空文本笔记。
func (s *Store) RecentPublishedNotes(ctx context.Context, accountID string) ([]PublishedNote, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, os.ErrClosed
	}
	// 在数据库内过滤 Unicode 空白正文，避免加载完整历史或图片等 payload 字段。
	rows, err := s.db.QueryContext(ctx, `SELECT t.title, json_extract(t.payload, '$.content')
	 FROM publish_tasks AS t JOIN accounts AS a ON a.id = t.account_id
	 WHERE t.account_id = ? AND a.deleted = 0 AND a.user_id <> '' AND t.user_id = a.user_id
	 AND t.status = 'succeeded' AND t.finished_at IS NOT NULL
	 AND CASE WHEN json_valid(t.payload) THEN
	   CASE WHEN json_type(t.payload, '$.content') = 'text' THEN
	     trim(json_extract(t.payload, '$.content'),
	       char(9, 10, 11, 12, 13, 32, 133, 160, 5760, 8192, 8193, 8194, 8195,
	            8196, 8197, 8198, 8199, 8200, 8201, 8202, 8232, 8233, 8239, 8287, 12288)) <> ''
	   ELSE 0 END
	 ELSE 0 END
	 ORDER BY t.finished_at DESC, t.id DESC LIMIT 5`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := make([]PublishedNote, 0, 5)
	for rows.Next() {
		var note PublishedNote
		if err := rows.Scan(&note.Title, &note.Content); err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	return notes, rows.Err()
}

// ListPublishTasks 返回全账号历史，页码从 1 开始，每页最多 100 条。
func (s *Store) ListPublishTasks(ctx context.Context, page, pageSize int) ([]PublishTask, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, 0, os.ErrClosed
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM publish_tasks`).Scan(&total); err != nil {
		return nil, 0, err
	}
	// 先按总数判断，避免极大页码的乘法溢出。
	if total == 0 || page-1 > (total-1)/pageSize {
		return []PublishTask{}, total, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+taskColumns+` FROM publish_tasks ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	tasks, err := scanPublishTasks(rows)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return tasks, total, nil
}

func (s *Store) QueuedPublishTasks(ctx context.Context) ([]PublishTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, os.ErrClosed
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+taskColumns+` FROM publish_tasks WHERE status = 'queued' ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	return scanPublishTasks(rows)
}

// StartPublishTask 只允许一个执行者将 queued 转为 running。
func (s *Store) StartPublishTask(ctx context.Context, id string) (PublishTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return PublishTask{}, os.ErrClosed
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishTask{}, err
	}
	defer tx.Rollback()
	task, err := scanPublishTask(tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM publish_tasks WHERE id = ?`, id))
	if err != nil {
		return PublishTask{}, err
	}
	if task.Status != TaskQueued {
		return PublishTask{}, ErrTaskStateConflict
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	result, err := tx.ExecContext(ctx,
		`UPDATE publish_tasks SET status = 'running', message = '正在发布', updated_at = ?, started_at = ? WHERE id = ? AND status = 'queued'`,
		now.UnixMilli(), now.UnixMilli(), id,
	)
	if err != nil {
		return PublishTask{}, err
	}
	if err := requireTaskChange(result, ErrTaskStateConflict); err != nil {
		return PublishTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishTask{}, err
	}
	task.Status, task.Message, task.UpdatedAt, task.StartedAt = TaskRunning, "正在发布", now, &now
	return task, nil
}

// FinishPublishTask 原子写入终态和发布保护，旧执行者不能覆盖新版本。
func (s *Store) FinishPublishTask(ctx context.Context, id string, outcome TaskOutcome) (PublishTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return PublishTask{}, os.ErrClosed
	}
	if outcome.Status != TaskSucceeded && outcome.Status != TaskFailed && outcome.Status != TaskUncertain {
		return PublishTask{}, fmt.Errorf("%w: task outcome", ErrInvalid)
	}
	if len(outcome.Result) > 0 && !json.Valid(outcome.Result) {
		return PublishTask{}, fmt.Errorf("%w: task result", ErrInvalid)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PublishTask{}, err
	}
	defer tx.Rollback()
	task, err := scanPublishTask(tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM publish_tasks WHERE id = ?`, id))
	if err != nil {
		return PublishTask{}, err
	}
	if task.Status != TaskRunning && (task.Status != TaskQueued || outcome.Status == TaskSucceeded) {
		return PublishTask{}, ErrTaskStateConflict
	}
	if err := finishPublishTaskTx(ctx, tx, &task, outcome, time.Now().UTC().Truncate(time.Millisecond)); err != nil {
		return PublishTask{}, err
	}
	if err := tx.Commit(); err != nil {
		return PublishTask{}, err
	}
	return task, nil
}

// RecoverPublishTasks 保留排队任务，只将中断的执行标为结果未知。
func (s *Store) RecoverPublishTasks(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return os.ErrClosed
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+taskColumns+` FROM publish_tasks WHERE status = 'running' ORDER BY created_at, id`)
	if err != nil {
		return err
	}
	// 单连接必须先关闭查询，再执行同事务内的状态更新。
	tasks, err := scanPublishTasks(rows)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	outcome := TaskOutcome{Status: TaskUncertain, Message: "服务中断，发布结果未知，请核对小红书后解除保护", Error: "服务在发布过程中中断"}
	for i := range tasks {
		if err := finishPublishTaskTx(ctx, tx, &tasks[i], outcome, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func finishPublishTaskTx(ctx context.Context, tx *sql.Tx, task *PublishTask, outcome TaskOutcome, now time.Time) error {
	result, err := tx.ExecContext(ctx,
		`UPDATE publish_protection SET revision = revision + 1, uncertain = ?,
		 last_published = CASE WHEN ? = 'succeeded' THEN ? ELSE last_published END,
		 reviewed_revision = NULL, updated_at = ? WHERE account_id = ? AND revision = ?`,
		boolInt(outcome.Status == TaskUncertain), outcome.Status, task.Fingerprint,
		now.UnixMilli(), task.AccountID, task.ProtectionRevision,
	)
	if err != nil {
		return err
	}
	if err := requireTaskChange(result, ErrTaskProtectionConflict); err != nil {
		return err
	}
	var payload any
	if len(outcome.Result) > 0 {
		payload = string(outcome.Result)
	}
	result, err = tx.ExecContext(ctx,
		`UPDATE publish_tasks SET status = ?, message = ?, error = ?, result = ?, updated_at = ?, finished_at = ?
		 WHERE id = ? AND status = ?`,
		outcome.Status, outcome.Message, outcome.Error, payload, now.UnixMilli(), now.UnixMilli(), task.ID, task.Status,
	)
	if err != nil {
		return err
	}
	if err := requireTaskChange(result, ErrTaskStateConflict); err != nil {
		return err
	}
	task.Status, task.Message, task.Error, task.Result = outcome.Status, outcome.Message, outcome.Error, outcome.Result
	task.UpdatedAt, task.FinishedAt = now, &now
	return nil
}

func checkNoActivePublishTask(tx *sql.Tx, accountID string) error {
	var exists int
	err := tx.QueryRow(`SELECT 1 FROM publish_tasks WHERE account_id = ? AND status IN ('queued', 'running')`, accountID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrTaskActive
}

func requireTaskChange(result sql.Result, conflict error) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return conflict
	}
	return nil
}

func scanPublishTask(scanner interface{ Scan(...any) error }) (PublishTask, error) {
	var task PublishTask
	var payload string
	var result sql.NullString
	var created, updated int64
	var started, finished sql.NullInt64
	if err := scanner.Scan(
		&task.ID, &task.AccountID, &task.AccountName, &task.UserID, &task.Kind, &task.Title,
		&payload, &task.Fingerprint, &task.RequestID, &task.ProtectionRevision,
		&task.Status, &task.Message, &task.Error, &result, &created, &updated, &started, &finished,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PublishTask{}, ErrTaskNotFound
		}
		return PublishTask{}, err
	}
	task.Payload = json.RawMessage(payload)
	if result.Valid {
		task.Result = json.RawMessage(result.String)
	}
	task.CreatedAt, task.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	if started.Valid {
		value := time.UnixMilli(started.Int64).UTC()
		task.StartedAt = &value
	}
	if finished.Valid {
		value := time.UnixMilli(finished.Int64).UTC()
		task.FinishedAt = &value
	}
	return task, nil
}

func scanPublishTasks(rows *sql.Rows) ([]PublishTask, error) {
	defer rows.Close()
	tasks := make([]PublishTask, 0)
	for rows.Next() {
		task, err := scanPublishTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return tasks, nil
}
