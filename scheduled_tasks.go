package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Yukuiii/xiaohongshu-studio/accounts"
	"github.com/Yukuiii/xiaohongshu-studio/pkg/xhsutil"
	"github.com/sirupsen/logrus"
)

// ScheduleManager 调度创作，实际发布复用持久任务和防重保护。
type ScheduleManager struct {
	server   *AppServer
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	started  bool
	closing  bool
	active   sync.WaitGroup
	interval time.Duration
	timeout  time.Duration
}

func newScheduleManager(server *AppServer) *ScheduleManager {
	return &ScheduleManager{
		server: server, interval: 5 * time.Second, timeout: 15 * time.Minute,
	}
}

func (m *ScheduleManager) Start(parent context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return errors.New("定时任务服务正在关闭")
	}
	if m.started {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	m.ctx, m.cancel = ctx, cancel
	if err := m.reconcile(ctx, true); err != nil {
		cancel()
		return err
	}
	if err := m.server.accounts.store.ResetScheduleTimes(ctx, time.Now()); err != nil {
		cancel()
		return err
	}
	m.started = true
	m.active.Add(1)
	go func() {
		defer m.active.Done()
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				if err := m.tick(ctx, now); err != nil && ctx.Err() == nil {
					logrus.WithError(err).Error("检查定时任务失败")
				}
			}
		}
	}()
	return nil
}

func (m *ScheduleManager) stopAccepting() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closing = true
	if m.cancel != nil {
		m.cancel()
	}
}

func (m *ScheduleManager) Stop() {
	m.stopAccepting()
	m.active.Wait()
}

func (m *ScheduleManager) tick(ctx context.Context, now time.Time) error {
	if err := m.reconcile(ctx, false); err != nil {
		return err
	}
	runs, err := m.server.accounts.store.ClaimDueScheduleRuns(ctx, now)
	if err != nil {
		return err
	}
	for _, run := range runs {
		m.mu.Lock()
		if m.closing || ctx.Err() != nil {
			m.mu.Unlock()
			return ctx.Err()
		}
		m.active.Add(1)
		m.mu.Unlock()
		go m.run(ctx, run)
	}
	return nil
}

// Retry 为失败记录创建一次新执行，重复请求返回同一记录。
func (m *ScheduleManager) Retry(ctx context.Context, scheduleID, runID string) (accounts.ScheduleRun, error) {
	m.mu.Lock()
	if !m.started || m.closing {
		m.mu.Unlock()
		return accounts.ScheduleRun{}, &accountError{http.StatusServiceUnavailable, "SCHEDULES_UNAVAILABLE", "定时任务服务尚未就绪或正在关闭"}
	}
	m.active.Add(1)
	m.mu.Unlock()
	launched := false
	defer func() {
		if !launched {
			m.active.Done()
		}
	}()
	run, created, err := m.server.accounts.store.RetryScheduleRun(ctx, scheduleID, runID, time.Now())
	if err != nil {
		return accounts.ScheduleRun{}, err
	}
	if created {
		launched = true
		go m.run(m.ctx, run)
	}
	return run, nil
}

func scheduleRequestID(runID string) string { return "schedule-run-" + runID }

// reconcile 启动时关联已接收的发布，绝不重发中断中的创作。
func (m *ScheduleManager) reconcile(ctx context.Context, restarting bool) error {
	store := m.server.accounts.store
	runs, err := store.UnfinishedScheduleRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range runs {
		if !restarting && run.Status != "publishing" {
			continue
		}
		task, err := store.PublishTaskByRequest(ctx, run.AccountID, scheduleRequestID(run.ID))
		if errors.Is(err, accounts.ErrTaskNotFound) {
			if restarting {
				if err := store.FinishScheduleRun(ctx, run.ID, "failed", "服务已重启，本次创作中断，未自动重新发布"); err != nil {
					return err
				}
			}
			continue
		}
		if err != nil {
			return err
		}
		if err := m.syncPublication(ctx, run, task); err != nil {
			return err
		}
	}
	return nil
}

func runProgress(run accounts.ScheduleRun, stage string) accounts.ScheduleRunProgress {
	return accounts.ScheduleRunProgress{Stage: stage, Title: run.Title, Content: run.Content, Tags: run.Tags, Sources: run.Sources, PublishTaskID: run.PublishTaskID, ImagePrompt: run.ImagePrompt, Images: run.Images}
}

func (m *ScheduleManager) syncPublication(ctx context.Context, run accounts.ScheduleRun, task accounts.PublishTask) error {
	store := m.server.accounts.store
	if run.PublishTaskID != task.ID {
		run.PublishTaskID = task.ID
		if err := store.UpdateScheduleRun(ctx, run.ID, runProgress(run, "publishing")); err != nil {
			return err
		}
	}
	switch task.Status {
	case accounts.TaskSucceeded, accounts.TaskFailed, accounts.TaskUncertain:
		return store.FinishScheduleRun(ctx, run.ID, string(task.Status), task.Error)
	}
	return nil
}

func (m *ScheduleManager) run(parent context.Context, run accounts.ScheduleRun) {
	defer m.active.Done()
	ctx, cancel := context.WithTimeout(parent, m.timeout)
	defer cancel()
	var executionErr error
	defer func() {
		if recovered := recover(); recovered != nil {
			executionErr = fmt.Errorf("定时创作异常: %v", recovered)
		}
		if executionErr != nil {
			m.finishFailure(run, executionErr)
		}
	}()
	executionErr = m.createPublication(ctx, &run)
}

func (m *ScheduleManager) createPublication(ctx context.Context, run *accounts.ScheduleRun) error {
	store := m.server.accounts.store
	job := run.Snapshot
	runtime, err := m.server.accounts.runtime(run.AccountID)
	if err != nil {
		return err
	}
	release, err := runtime.lease(true)
	if err != nil {
		return err
	}
	defer release()
	account, err := store.Get(run.AccountID)
	if err != nil {
		return err
	}
	if job.UserID == "" || account.UserID != job.UserID {
		return accounts.ErrIdentityMismatch
	}
	protection, err := store.LoadProtection(run.AccountID)
	if err != nil {
		return err
	}
	if protection.ActiveTaskID != "" {
		return accounts.ErrTaskActive
	}
	if protection.Uncertain {
		return accounts.ErrTaskUncertain
	}
	cfg, imageConfig, err := m.server.scheduleModels(job.PublicationType)
	if err != nil {
		return err
	}

	if err := store.UpdateScheduleRun(ctx, run.ID, runProgress(*run, "composing")); err != nil {
		return err
	}
	recentNotes, err := store.RecentPublishedNotes(ctx, run.AccountID)
	if err != nil {
		return fmt.Errorf("读取近期发布内容失败: %w", err)
	}
	draft, err := composeKeywordDraft(ctx, cfg, job.Keyword, job.Instructions, job.PublicationType, recentNotes)
	if err != nil {
		return fmt.Errorf("生成笔记失败: %w", err)
	}
	run.Title, run.Content, run.Tags = draft.Title, draft.Content, draft.Tags
	run.ImagePrompt = draft.ImagePrompt
	if err := store.UpdateScheduleRun(ctx, run.ID, runProgress(*run, "composing")); err != nil {
		return err
	}
	if xhsutil.CalcTitleLength(draft.Title) > 20 {
		return errors.New("生成的标题超过 20 字，已保留稿件，本次未发布")
	}
	contentLength := utf8.RuneCountInString(draft.Content)
	for _, tag := range draft.Tags {
		contentLength += utf8.RuneCountInString(strings.TrimPrefix(tag, "#")) + 2
	}
	contentLimit := 1000
	if job.PublicationType == "long_text" {
		contentLimit = 10000
	}
	if contentLength > contentLimit {
		return fmt.Errorf("生成的正文与话题超过 %d 字，已保留稿件，本次未发布", contentLimit)
	}
	var imagePaths []string
	if job.PublicationType != "long_text" {
		if err := store.UpdateScheduleRun(ctx, run.ID, runProgress(*run, "imaging")); err != nil {
			return err
		}
		prompt, err := renderPrompt(imagePromptTemplate, map[string]string{"title": draft.Title, "content": draft.Content, "image_prompt": draft.ImagePrompt})
		if err != nil {
			return err
		}
		data, err := newLLMClient().generateImage(ctx, imageConfig, prompt)
		if err != nil {
			return fmt.Errorf("生成配图失败: %w", err)
		}
		image, err := saveAccountImage(data, "定时任务配图", run.AccountID)
		if err != nil {
			return fmt.Errorf("保存配图失败: %w", err)
		}
		run.Images = []string{image.ID}
		if err := store.UpdateScheduleRun(ctx, run.ID, runProgress(*run, "imaging")); err != nil {
			_ = os.Remove(image.Path)
			return err
		}
		imagePaths = []string{image.Path}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	task, err := m.server.tasks.SubmitContent(ctx, run.AccountID, scheduleRequestID(run.ID), &PublishRequest{
		Title: draft.Title, Content: draft.Content, Tags: draft.Tags, Images: imagePaths, PublicationType: job.PublicationType, Visibility: "公开可见",
	})
	if err != nil {
		return err
	}
	return m.syncPublication(ctx, *run, task)
}

// finishFailure 先核对幂等任务，防止响应中断把已接收的发布记作未发布。
func (m *ScheduleManager) finishFailure(run accounts.ScheduleRun, cause error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	store := m.server.accounts.store
	task, err := store.PublishTaskByRequest(ctx, run.AccountID, scheduleRequestID(run.ID))
	if err == nil {
		err = m.syncPublication(ctx, run, task)
	} else if errors.Is(err, accounts.ErrTaskNotFound) {
		err = store.FinishScheduleRun(ctx, run.ID, "failed", cause.Error())
	}
	if err != nil {
		logrus.WithField("schedule_run_id", run.ID).WithError(err).Error("保存定时执行结果失败")
	}
}
