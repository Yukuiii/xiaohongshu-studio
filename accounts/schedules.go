package accounts

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	_ "time/tzdata"
	"unicode/utf8"

	"github.com/robfig/cron/v3"
)

var (
	ErrScheduleNotFound     = errors.New("scheduled job or run not found")
	ErrScheduleActive       = errors.New("scheduled execution already active")
	ErrScheduleNotRetryable = errors.New("scheduled run cannot be retried")
)

type ScheduledJobInput struct {
	Name            string `json:"name"`
	AccountID       string `json:"account_id"`
	Keyword         string `json:"keyword"`
	Instructions    string `json:"instructions"`
	Cron            string `json:"cron"`
	Timezone        string `json:"timezone"`
	Enabled         bool   `json:"enabled"`
	PublicationType string `json:"publication_type"`
}

type ScheduledJob struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	AccountID       string     `json:"account_id"`
	AccountName     string     `json:"account_name"`
	UserID          string     `json:"-"`
	Keyword         string     `json:"keyword"`
	Instructions    string     `json:"instructions"`
	Cron            string     `json:"cron"`
	Timezone        string     `json:"timezone"`
	Enabled         bool       `json:"enabled"`
	NextRunAt       *time.Time `json:"next_run_at"`
	LastRunAt       *time.Time `json:"last_run_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	PublicationType string     `json:"publication_type"`
	LastRunID       string     `json:"last_run_id"`
	LastRunStatus   string     `json:"last_run_status"`
	LastRunError    string     `json:"last_run_error"`
	ActiveRunID     string     `json:"active_run_id"`
}

type ScheduleRun struct {
	ID            string          `json:"id"`
	ScheduleID    string          `json:"schedule_id"`
	AccountID     string          `json:"account_id"`
	AccountName   string          `json:"account_name"`
	Status        string          `json:"status"`
	Stage         string          `json:"stage"`
	ScheduledAt   time.Time       `json:"scheduled_at"`
	StartedAt     time.Time       `json:"started_at"`
	FinishedAt    *time.Time      `json:"finished_at"`
	Title         string          `json:"title"`
	Content       string          `json:"content"`
	Tags          []string        `json:"tags"`
	Sources       json.RawMessage `json:"sources"`
	Error         string          `json:"error"`
	PublishTaskID string          `json:"publish_task_id"`
	Result        json.RawMessage `json:"result,omitempty"`
	Snapshot      ScheduledJob    `json:"snapshot"`
	ImagePrompt   string          `json:"image_prompt"`
	Images        []string        `json:"images"`
	RetryOfID     string          `json:"retry_of_id"`
}

type ScheduleRunProgress struct {
	Stage         string
	Title         string
	Content       string
	Tags          []string
	Sources       json.RawMessage
	PublishTaskID string
	ImagePrompt   string   `json:"image_prompt"`
	Images        []string `json:"images"`
}

// 身份只写入数据库快照，不暴露给工作台接口。
type storedScheduleSnapshot struct {
	ScheduledJob
	UserID string `json:"user_id"`
}

const scheduledJobColumns = `id, name, account_id, account_name, user_id, keyword, instructions,
 cron, timezone, enabled, next_run_at, last_run_at, created_at, updated_at, publication_type`

const scheduledJobSummaryColumns = `,
 COALESCE((SELECT id FROM schedule_runs WHERE schedule_id = scheduled_jobs.id ORDER BY started_at DESC, rowid DESC LIMIT 1), ''),
 COALESCE((SELECT status FROM schedule_runs WHERE schedule_id = scheduled_jobs.id ORDER BY started_at DESC, rowid DESC LIMIT 1), ''),
 COALESCE((SELECT error FROM schedule_runs WHERE schedule_id = scheduled_jobs.id ORDER BY started_at DESC, rowid DESC LIMIT 1), ''),
 COALESCE((SELECT id FROM schedule_runs WHERE schedule_id = scheduled_jobs.id AND status IN ('running', 'publishing') LIMIT 1), '')`

const scheduleRunColumns = `id, schedule_id, account_id, account_name, status, stage,
 scheduled_at, started_at, finished_at, title, content, tags, sources, error, publish_task_id, snapshot, image_prompt, images, retry_of_id`

const scheduleRunResultColumns = `,
 (SELECT result FROM publish_tasks WHERE id = schedule_runs.publish_task_id AND account_id = schedule_runs.account_id)`

var scheduleParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// NextScheduleTime 严格使用五字段 cron 和显式时区，结果总在 after 之后。
func NextScheduleTime(expression, timezone string, after time.Time) (time.Time, error) {
	fields := strings.Fields(expression)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("%w: cron 必须包含五个字段", ErrInvalid)
	}
	if timezone == "" || timezone == "Local" || strings.TrimSpace(timezone) != timezone {
		return time.Time{}, fmt.Errorf("%w: 必须指定 IANA 时区", ErrInvalid)
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: 时区: %v", ErrInvalid, err)
	}
	parsed, err := scheduleParser.Parse(strings.Join(fields, " "))
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: cron: %v", ErrInvalid, err)
	}
	parsed.(*cron.SpecSchedule).Location = location
	next := parsed.Next(after)
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("%w: cron 没有可执行的日期", ErrInvalid)
	}
	return next.UTC(), nil
}

func normalizeScheduledJob(input ScheduledJobInput, now time.Time) (ScheduledJobInput, *time.Time, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Keyword = strings.TrimSpace(input.Keyword)
	input.Instructions = strings.TrimSpace(input.Instructions)
	input.Cron = strings.Join(strings.Fields(input.Cron), " ")
	input.Timezone = strings.TrimSpace(input.Timezone)
	if !validName(input.Name) || !validID(input.AccountID) || input.Keyword == "" || !utf8.ValidString(input.Keyword) || !utf8.ValidString(input.Instructions) {
		return input, nil, fmt.Errorf("%w: 名称、账号、关键词或创作要求无效", ErrInvalid)
	}
	if input.PublicationType == "" {
		input.PublicationType = "image"
	}
	if input.PublicationType != "image" && input.PublicationType != "long_text" {
		return input, nil, fmt.Errorf("%w: 发布类型必须是 image 或 long_text", ErrInvalid)
	}
	next, err := NextScheduleTime(input.Cron, input.Timezone, now)
	if err != nil {
		return input, nil, err
	}
	if !input.Enabled {
		return input, nil, nil
	}
	return input, &next, nil
}

func (s *Store) CreateScheduledJob(ctx context.Context, input ScheduledJobInput, now time.Time) (ScheduledJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ScheduledJob{}, os.ErrClosed
	}
	now = now.UTC().Truncate(time.Millisecond)
	input, next, err := normalizeScheduledJob(input, now)
	if err != nil {
		return ScheduledJob{}, err
	}
	a, err := s.get(input.AccountID)
	if err != nil {
		return ScheduledJob{}, err
	}
	if input.Enabled && a.UserID == "" {
		return ScheduledJob{}, fmt.Errorf("%w: 启用定时任务前必须绑定账号身份", ErrInvalid)
	}
	id, err := newID()
	if err != nil {
		return ScheduledJob{}, err
	}
	job := ScheduledJob{
		ID: id, Name: input.Name, AccountID: a.ID, AccountName: a.Name, UserID: a.UserID,
		Keyword: input.Keyword, Instructions: input.Instructions, Cron: input.Cron, Timezone: input.Timezone,
		Enabled: input.Enabled, NextRunAt: next, CreatedAt: now, UpdatedAt: now,
		PublicationType: input.PublicationType,
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO scheduled_jobs (`+scheduledJobColumns+`)
	 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?)`,
		job.ID, job.Name, job.AccountID, job.AccountName, job.UserID, job.Keyword, job.Instructions,
		job.Cron, job.Timezone, boolInt(job.Enabled),
		nullableTime(job.NextRunAt), now.UnixMilli(), now.UnixMilli(), job.PublicationType)
	return job, err
}

func (s *Store) UpdateScheduledJob(ctx context.Context, id string, input ScheduledJobInput, now time.Time) (ScheduledJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ScheduledJob{}, os.ErrClosed
	}
	now = now.UTC().Truncate(time.Millisecond)
	input, next, err := normalizeScheduledJob(input, now)
	if err != nil {
		return ScheduledJob{}, err
	}
	a, err := s.get(input.AccountID)
	if err != nil {
		return ScheduledJob{}, err
	}
	if input.Enabled && a.UserID == "" {
		return ScheduledJob{}, fmt.Errorf("%w: 启用定时任务前必须绑定账号身份", ErrInvalid)
	}
	return scanScheduledJob(s.db.QueryRowContext(ctx, `UPDATE scheduled_jobs SET
	 name = ?, account_id = ?, account_name = ?, user_id = ?, keyword = ?, instructions = ?,
	 cron = ?, timezone = ?, enabled = ?, next_run_at = ?, updated_at = ?, publication_type = ?
	 WHERE id = ? AND deleted = 0 RETURNING `+scheduledJobColumns,
		input.Name, a.ID, a.Name, a.UserID, input.Keyword, input.Instructions,
		input.Cron, input.Timezone, boolInt(input.Enabled), nullableTime(next), now.UnixMilli(), input.PublicationType, id), false)
}

func (s *Store) DeleteScheduledJob(ctx context.Context, id string) error {
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
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM scheduled_jobs WHERE id = ? AND deleted = 0`, id).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrScheduleNotFound
		}
		return err
	}
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM schedule_runs WHERE schedule_id = ? AND status IN ('running', 'publishing') LIMIT 1`, id).Scan(&exists)
	if err == nil {
		return ErrScheduleActive
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_jobs SET deleted = 1, enabled = 0, next_run_at = NULL, updated_at = ? WHERE id = ?`, time.Now().UnixMilli(), id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ScheduledJob(ctx context.Context, id string) (ScheduledJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ScheduledJob{}, os.ErrClosed
	}
	return scanScheduledJob(s.db.QueryRowContext(ctx, `SELECT `+scheduledJobColumns+scheduledJobSummaryColumns+` FROM scheduled_jobs WHERE id = ? AND deleted = 0`, id), true)
}

func (s *Store) ListScheduledJobs(ctx context.Context) ([]ScheduledJob, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, os.ErrClosed
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+scheduledJobColumns+scheduledJobSummaryColumns+` FROM scheduled_jobs WHERE deleted = 0 ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	return scanScheduledJobs(rows, true)
}

// ResetScheduleTimes 跳过停机期间的时点，保留所有执行记录供恢复流程处理。
func (s *Store) ResetScheduleTimes(ctx context.Context, now time.Time) error {
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
	rows, err := tx.QueryContext(ctx, `SELECT `+scheduledJobColumns+` FROM scheduled_jobs WHERE enabled = 1 AND deleted = 0`)
	if err != nil {
		return err
	}
	jobs, err := scanScheduledJobs(rows, false)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		next, err := NextScheduleTime(job.Cron, job.Timezone, now)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scheduled_jobs SET next_run_at = ? WHERE id = ?`, next.UnixMilli(), job.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClaimDueScheduleRuns 一次事务推进时钟并创建快照；账号忙时留下 skipped，不排队补跑。
func (s *Store) ClaimDueScheduleRuns(ctx context.Context, now time.Time) ([]ScheduleRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, os.ErrClosed
	}
	if s.restartPending {
		return nil, nil
	}
	now = now.UTC().Truncate(time.Millisecond)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT `+scheduledJobColumns+` FROM scheduled_jobs
	 WHERE enabled = 1 AND deleted = 0 AND next_run_at <= ? ORDER BY next_run_at, id`, now.UnixMilli())
	if err != nil {
		return nil, err
	}
	jobs, err := scanScheduledJobs(rows, false)
	if err != nil {
		return nil, err
	}
	runs := make([]ScheduleRun, 0, len(jobs))
	for _, job := range jobs {
		next, err := NextScheduleTime(job.Cron, job.Timezone, now)
		if err != nil {
			return nil, err
		}
		result, err := tx.ExecContext(ctx, `UPDATE scheduled_jobs SET next_run_at = ?
		 WHERE id = ? AND enabled = 1 AND deleted = 0 AND next_run_at = ?`, next.UnixMilli(), job.ID, job.NextRunAt.UnixMilli())
		if err != nil {
			return nil, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			continue
		}
		id, err := newID()
		if err != nil {
			return nil, err
		}
		run := ScheduleRun{
			ID: id, ScheduleID: job.ID, AccountID: job.AccountID, AccountName: job.AccountName,
			Status: "running", Stage: "composing", ScheduledAt: *job.NextRunAt, StartedAt: now,
			Tags: []string{}, Sources: json.RawMessage(`[]`), Images: []string{}, Snapshot: job,
		}
		if err := checkNoActiveScheduleRun(tx, job.AccountID, job.ID); errors.Is(err, ErrScheduleActive) {
			run.Status, run.Stage, run.Error, run.FinishedAt = "skipped", "finished", "该计划或同账号已有定时任务正在执行", &now
		} else if err != nil {
			return nil, err
		}
		snapshot, err := json.Marshal(storedScheduleSnapshot{ScheduledJob: job, UserID: job.UserID})
		if err != nil {
			return nil, err
		}
		// 时钟回拨或重复 claim 也不会重建已经执行过的时点。
		result, err = tx.ExecContext(ctx, `INSERT INTO schedule_runs (`+scheduleRunColumns+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', '', '[]', '[]', ?, '', ?, '', '[]', '')
		 ON CONFLICT(schedule_id, scheduled_at) WHERE retry_of_id = '' DO NOTHING`,
			run.ID, run.ScheduleID, run.AccountID, run.AccountName, run.Status, run.Stage,
			run.ScheduledAt.UnixMilli(), now.UnixMilli(), nullableTime(run.FinishedAt), run.Error, string(snapshot))
		if err != nil {
			return nil, err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scheduled_jobs SET last_run_at = ? WHERE id = ?`, now.UnixMilli(), job.ID); err != nil {
			return nil, err
		}
		if run.Status == "running" {
			runs = append(runs, run)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return runs, nil
}

func (s *Store) UnfinishedScheduleRuns(ctx context.Context) ([]ScheduleRun, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, os.ErrClosed
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+scheduleRunColumns+` FROM schedule_runs WHERE status IN ('running', 'publishing') ORDER BY started_at, id`)
	if err != nil {
		return nil, err
	}
	return scanScheduleRuns(rows, false)
}

// ListScheduleRuns 保留已删除配置的历史；页码从 1 开始，每页最多 100 条。
func (s *Store) ListScheduleRuns(ctx context.Context, scheduleID string, page, pageSize int) ([]ScheduleRun, int, error) {
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
	var exists, total int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM scheduled_jobs WHERE id = ?`, scheduleID).Scan(&exists); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, 0, ErrScheduleNotFound
		}
		return nil, 0, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM schedule_runs WHERE schedule_id = ?`, scheduleID).Scan(&total); err != nil {
		return nil, 0, err
	}
	if total == 0 || page-1 > (total-1)/pageSize {
		return []ScheduleRun{}, total, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+scheduleRunColumns+scheduleRunResultColumns+` FROM schedule_runs WHERE schedule_id = ? ORDER BY started_at DESC, rowid DESC LIMIT ? OFFSET ?`, scheduleID, pageSize, (page-1)*pageSize)
	if err != nil {
		return nil, 0, err
	}
	runs, err := scanScheduleRuns(rows, true)
	if err != nil {
		return nil, 0, err
	}
	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}
	return runs, total, nil
}

func (s *Store) UpdateScheduleRun(ctx context.Context, id string, progress ScheduleRunProgress) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return os.ErrClosed
	}
	switch progress.Stage {
	case "searching", "reading", "composing", "imaging", "publishing":
	default:
		return fmt.Errorf("%w: 无效的定时执行阶段", ErrInvalid)
	}
	if !utf8.ValidString(progress.Title) || !utf8.ValidString(progress.Content) || !utf8.ValidString(progress.ImagePrompt) || !utf8.ValidString(progress.PublishTaskID) ||
		(progress.PublishTaskID != "" && (progress.Stage != "publishing" || strings.TrimSpace(progress.PublishTaskID) != progress.PublishTaskID)) {
		return fmt.Errorf("%w: 无效的执行进度", ErrInvalid)
	}
	if progress.Tags == nil {
		progress.Tags = []string{}
	}
	for _, tag := range progress.Tags {
		if !utf8.ValidString(tag) {
			return fmt.Errorf("%w: 无效的话题", ErrInvalid)
		}
	}
	tags, err := json.Marshal(progress.Tags)
	if err != nil {
		return err
	}
	if progress.Images == nil {
		progress.Images = []string{}
	}
	for _, image := range progress.Images {
		if strings.TrimSpace(image) == "" || !utf8.ValidString(image) {
			return fmt.Errorf("%w: 无效的托管图片 ID", ErrInvalid)
		}
	}
	images, err := json.Marshal(progress.Images)
	if err != nil {
		return err
	}
	if len(progress.Sources) == 0 {
		progress.Sources = json.RawMessage(`[]`)
	}
	if !json.Valid(progress.Sources) || bytes.TrimSpace(progress.Sources)[0] != '[' {
		return fmt.Errorf("%w: 参考正文必须是 JSON 数组", ErrInvalid)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE schedule_runs SET stage = ?, title = ?, content = ?, tags = ?, sources = ?, publish_task_id = ?, image_prompt = ?, images = ?,
	 status = CASE WHEN ? <> '' THEN 'publishing' ELSE status END
	 WHERE id = ? AND status IN ('running', 'publishing')
	 AND (status = 'running' OR (? = 'publishing' AND ? = publish_task_id))`,
		progress.Stage, progress.Title, progress.Content, string(tags), string(progress.Sources), progress.PublishTaskID, progress.ImagePrompt, string(images),
		progress.PublishTaskID, id, progress.Stage, progress.PublishTaskID)
	if err != nil {
		return err
	}
	return s.requireScheduleRunChange(ctx, result, id)
}

func (s *Store) FinishScheduleRun(ctx context.Context, id, status, message string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return os.ErrClosed
	}
	switch status {
	case "succeeded", "failed", "uncertain", "skipped":
	default:
		return fmt.Errorf("%w: 无效的定时执行终态", ErrInvalid)
	}
	if !utf8.ValidString(message) {
		return fmt.Errorf("%w: 无效的执行结果", ErrInvalid)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE schedule_runs SET status = ?, stage = 'finished', error = ?, finished_at = ?
	 WHERE id = ? AND status IN ('running', 'publishing')`, status, message, time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	return s.requireScheduleRunChange(ctx, result, id)
}

func (s *Store) requireScheduleRunChange(ctx context.Context, result sql.Result, id string) error {
	affected, err := result.RowsAffected()
	if err != nil || affected == 1 {
		return err
	}
	var status string
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM schedule_runs WHERE id = ?`, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrScheduleNotFound
		}
		return err
	}
	if status != "running" && status != "publishing" {
		return nil
	}
	return fmt.Errorf("%w: 发布任务关联冲突", ErrInvalid)
}

func checkNoActiveScheduleRun(tx *sql.Tx, accountID, scheduleID string) error {
	var exists int
	err := tx.QueryRow(`SELECT 1 FROM schedule_runs WHERE (account_id = ? OR schedule_id = ?) AND status IN ('running', 'publishing') LIMIT 1`, accountID, scheduleID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrScheduleActive
}

func scanScheduledJob(scanner interface{ Scan(...any) error }, withSummary bool) (ScheduledJob, error) {
	var job ScheduledJob
	var enabled int
	var next, last sql.NullInt64
	var created, updated int64
	fields := []any{&job.ID, &job.Name, &job.AccountID, &job.AccountName, &job.UserID,
		&job.Keyword, &job.Instructions, &job.Cron, &job.Timezone,
		&enabled, &next, &last, &created, &updated, &job.PublicationType,
		&job.LastRunID, &job.LastRunStatus, &job.LastRunError, &job.ActiveRunID}
	if !withSummary {
		fields = fields[:15]
	}
	if err := scanner.Scan(fields...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ScheduledJob{}, ErrScheduleNotFound
		}
		return ScheduledJob{}, err
	}
	job.Enabled = enabled != 0
	job.CreatedAt, job.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	if next.Valid {
		value := time.UnixMilli(next.Int64).UTC()
		job.NextRunAt = &value
	}
	if last.Valid {
		value := time.UnixMilli(last.Int64).UTC()
		job.LastRunAt = &value
	}
	return job, nil
}

func scanScheduledJobs(rows *sql.Rows, withSummary bool) ([]ScheduledJob, error) {
	defer rows.Close()
	jobs := make([]ScheduledJob, 0)
	for rows.Next() {
		job, err := scanScheduledJob(rows, withSummary)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func scanScheduleRun(scanner interface{ Scan(...any) error }, withResult bool) (ScheduleRun, error) {
	var run ScheduleRun
	var scheduled, started int64
	var finished sql.NullInt64
	var tags, sources, snapshot, images string
	var result sql.NullString
	fields := []any{&run.ID, &run.ScheduleID, &run.AccountID, &run.AccountName, &run.Status, &run.Stage,
		&scheduled, &started, &finished, &run.Title, &run.Content, &tags, &sources,
		&run.Error, &run.PublishTaskID, &snapshot, &run.ImagePrompt, &images, &run.RetryOfID, &result}
	if !withResult {
		fields = fields[:19]
	}
	if err := scanner.Scan(fields...); err != nil {
		return ScheduleRun{}, err
	}
	run.ScheduledAt, run.StartedAt = time.UnixMilli(scheduled).UTC(), time.UnixMilli(started).UTC()
	if finished.Valid {
		value := time.UnixMilli(finished.Int64).UTC()
		run.FinishedAt = &value
	}
	if err := json.Unmarshal([]byte(tags), &run.Tags); err != nil {
		return ScheduleRun{}, err
	}
	if err := json.Unmarshal([]byte(images), &run.Images); err != nil {
		return ScheduleRun{}, err
	}
	run.Sources = json.RawMessage(sources)
	if result.Valid {
		run.Result = json.RawMessage(result.String)
	}
	var stored storedScheduleSnapshot
	if err := json.Unmarshal([]byte(snapshot), &stored); err != nil {
		return ScheduleRun{}, err
	}
	run.Snapshot = stored.ScheduledJob
	run.Snapshot.UserID = stored.UserID
	return run, nil
}

func scanScheduleRuns(rows *sql.Rows, withResult bool) ([]ScheduleRun, error) {
	defer rows.Close()
	runs := make([]ScheduleRun, 0)
	for rows.Next() {
		run, err := scanScheduleRun(rows, withResult)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// RetryScheduleRun 仅手动重试失败或跳过的执行；来源 ID 是幂等键，不改变正常计划。
func (s *Store) RetryScheduleRun(ctx context.Context, scheduleID, runID string, now time.Time) (ScheduleRun, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return ScheduleRun{}, false, os.ErrClosed
	}
	if s.restartPending {
		return ScheduleRun{}, false, ErrRestartPending
	}
	now = now.UTC().Truncate(time.Millisecond)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScheduleRun{}, false, err
	}
	defer tx.Rollback()
	job, err := scanScheduledJob(tx.QueryRowContext(ctx, `SELECT `+scheduledJobColumns+` FROM scheduled_jobs WHERE id = ? AND deleted = 0`, scheduleID), false)
	if err != nil {
		return ScheduleRun{}, false, err
	}
	account, err := s.get(job.AccountID)
	if err != nil {
		return ScheduleRun{}, false, err
	}
	if account.UserID == "" {
		return ScheduleRun{}, false, fmt.Errorf("%w: 重试前必须绑定账号身份", ErrInvalid)
	}
	var sourceStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM schedule_runs WHERE id = ? AND schedule_id = ?`, runID, scheduleID).Scan(&sourceStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ScheduleRun{}, false, ErrScheduleNotFound
		}
		return ScheduleRun{}, false, err
	}
	if sourceStatus != "failed" && sourceStatus != "skipped" {
		return ScheduleRun{}, false, ErrScheduleNotRetryable
	}
	existing, err := scanScheduleRun(tx.QueryRowContext(ctx, `SELECT `+scheduleRunColumns+` FROM schedule_runs WHERE retry_of_id = ?`, runID), false)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ScheduleRun{}, false, err
	}
	var latestID string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM schedule_runs WHERE schedule_id = ? ORDER BY started_at DESC, rowid DESC LIMIT 1`, scheduleID).Scan(&latestID); err != nil {
		return ScheduleRun{}, false, err
	}
	if latestID != runID {
		return ScheduleRun{}, false, ErrScheduleNotRetryable
	}
	if err := checkNoActiveScheduleRun(tx, job.AccountID, scheduleID); err != nil {
		return ScheduleRun{}, false, err
	}
	if err := checkNoActivePublishTask(tx, job.AccountID); err != nil {
		if errors.Is(err, ErrTaskActive) {
			return ScheduleRun{}, false, ErrScheduleActive
		}
		return ScheduleRun{}, false, err
	}
	var uncertain int
	err = tx.QueryRowContext(ctx, `SELECT uncertain FROM publish_protection WHERE account_id = ?`, job.AccountID).Scan(&uncertain)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ScheduleRun{}, false, err
	}
	if uncertain != 0 {
		return ScheduleRun{}, false, ErrTaskUncertain
	}
	// 暂停时创建的配置可能尚未绑定身份，重试快照使用当前已核验身份。
	job.AccountName, job.UserID = account.Name, account.UserID
	id, err := newID()
	if err != nil {
		return ScheduleRun{}, false, err
	}
	run := ScheduleRun{
		ID: id, ScheduleID: job.ID, AccountID: job.AccountID, AccountName: job.AccountName,
		Status: "running", Stage: "composing", ScheduledAt: now, StartedAt: now,
		Tags: []string{}, Sources: json.RawMessage(`[]`), Images: []string{}, Snapshot: job, RetryOfID: runID,
	}
	snapshot, err := json.Marshal(storedScheduleSnapshot{ScheduledJob: job, UserID: job.UserID})
	if err != nil {
		return ScheduleRun{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schedule_runs (`+scheduleRunColumns+`)
	 VALUES (?, ?, ?, ?, 'running', 'composing', ?, ?, NULL, '', '', '[]', '[]', '', '', ?, '', '[]', ?)`,
		run.ID, run.ScheduleID, run.AccountID, run.AccountName, now.UnixMilli(), now.UnixMilli(), string(snapshot), runID); err != nil {
		return ScheduleRun{}, false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_jobs SET last_run_at = ? WHERE id = ?`, now.UnixMilli(), scheduleID); err != nil {
		return ScheduleRun{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ScheduleRun{}, false, err
	}
	return run, true, nil
}
