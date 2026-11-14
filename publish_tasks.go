package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
	xhsErrors "github.com/xpzouying/xiaohongshu-mcp/errors"
)

// PublishTaskManager 持有任务执行和账号租约，不依赖提交请求的生命周期。
type PublishTaskManager struct {
	accounts *AccountManager
	store    *accounts.Store
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	started  bool
	closing  bool
	active   sync.WaitGroup
	timeout  time.Duration
	execute  func(context.Context, *accountRuntime, accounts.PublishTask) (json.RawMessage, error)
}

func NewPublishTaskManager(manager *AccountManager) *PublishTaskManager {
	m := &PublishTaskManager{accounts: manager, store: manager.store, timeout: 10 * time.Minute}
	m.execute = m.publish
	return m
}

// Start 恢复排队任务；执行中断的任务只标记待核对，不自动重发。
func (m *PublishTaskManager) Start(parent context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closing {
		return &accountError{http.StatusServiceUnavailable, "SERVICE_CLOSING", "任务服务正在关闭"}
	}
	if m.started {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	m.ctx, m.cancel = ctx, cancel
	if err := m.store.RecoverPublishTasks(ctx); err != nil {
		cancel()
		return err
	}
	queued, err := m.store.QueuedPublishTasks(ctx)
	if err != nil {
		cancel()
		return err
	}
	m.started = true
	for _, task := range queued {
		runtime, err := m.accounts.runtime(task.AccountID)
		if err != nil {
			m.complete(task, accounts.TaskOutcome{Status: accounts.TaskFailed, Message: "任务无法执行", Error: err.Error()})
			continue
		}
		release, err := runtime.lease(true)
		if err != nil {
			m.complete(task, accounts.TaskOutcome{Status: accounts.TaskFailed, Message: "账号暂不可用", Error: err.Error()})
			continue
		}
		m.active.Add(1)
		go m.run(task, runtime, release)
	}
	return nil
}

func (m *PublishTaskManager) Stop() {
	m.stopAccepting()
	m.active.Wait()
}

// stopAccepting 先拒绝新任务并取消执行，浏览器退出后再等待排空。
func (m *PublishTaskManager) stopAccepting() {
	m.mu.Lock()
	m.closing = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
}

func (m *PublishTaskManager) SubmitImage(ctx context.Context, accountID, requestID string, req *PublishRequest) (accounts.PublishTask, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return accounts.PublishTask{}, err
	}
	fingerprint := publicationFingerprint{
		Title: req.Title, Content: req.Content, Images: req.Images, Tags: req.Tags,
		Visibility: req.Visibility, AtUsers: req.AtUsers, ScheduleAt: req.ScheduleAt,
		IsOriginal: req.IsOriginal, Products: req.Products,
	}
	encoded, err := fingerprint.encode()
	if err != nil {
		return accounts.PublishTask{}, err
	}
	return m.submit(ctx, accounts.NewPublishTask{AccountID: accountID, Kind: accounts.TaskKindPublish, Title: req.Title, Payload: payload, Fingerprint: encoded, RequestID: requestID})
}

func (m *PublishTaskManager) SubmitVideo(ctx context.Context, accountID, requestID string, req *PublishVideoRequest) (accounts.PublishTask, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return accounts.PublishTask{}, err
	}
	fingerprint := publicationFingerprint{
		Title: req.Title, Content: req.Content, Video: req.Video, Tags: req.Tags,
		Visibility: req.Visibility, ScheduleAt: req.ScheduleAt,
		IsOriginal: req.IsOriginal, Products: req.Products,
	}
	encoded, err := fingerprint.encode()
	if err != nil {
		return accounts.PublishTask{}, err
	}
	return m.submit(ctx, accounts.NewPublishTask{AccountID: accountID, Kind: accounts.TaskKindPublishVideo, Title: req.Title, Payload: payload, Fingerprint: encoded, RequestID: requestID})
}

func (m *PublishTaskManager) submit(ctx context.Context, input accounts.NewPublishTask) (accounts.PublishTask, error) {
	if input.RequestID == "" {
		id, err := randomSessionID()
		if err != nil {
			return accounts.PublishTask{}, err
		}
		input.RequestID = id
	}
	if len(input.RequestID) > 128 || strings.TrimSpace(input.RequestID) != input.RequestID || strings.ContainsAny(input.RequestID, "\r\n\t") {
		return accounts.PublishTask{}, &accountError{http.StatusBadRequest, "INVALID_REQUEST_ID", "发布请求标识无效"}
	}
	m.mu.Lock()
	if !m.started || m.closing {
		m.mu.Unlock()
		return accounts.PublishTask{}, &accountError{http.StatusServiceUnavailable, "TASKS_UNAVAILABLE", "发布任务服务尚未就绪或正在关闭"}
	}
	// 与 Stop 串行登记，避免提交尚未落盘时提前排空账号租约。
	m.active.Add(1)
	m.mu.Unlock()
	launched := false
	var release func()
	defer func() {
		if !launched {
			if release != nil {
				release()
			}
			m.active.Done()
		}
	}()

	if existing, err := m.existing(ctx, input); !errors.Is(err, accounts.ErrTaskNotFound) {
		return existing, err
	}
	runtime, err := m.accounts.runtime(input.AccountID)
	if err != nil {
		return accounts.PublishTask{}, err
	}
	release, err = runtime.lease(true)
	if err != nil {
		return accounts.PublishTask{}, err
	}
	protection, err := m.store.LoadProtection(input.AccountID)
	if err != nil {
		return accounts.PublishTask{}, err
	}
	if protection.ActiveTaskID != "" {
		existing, lookupErr := m.existing(ctx, input)
		if errors.Is(lookupErr, accounts.ErrTaskNotFound) {
			return accounts.PublishTask{}, accounts.ErrTaskProtectionConflict
		}
		return existing, lookupErr
	}
	if protection.Uncertain {
		return accounts.PublishTask{}, accounts.ErrTaskUncertain
	}
	if samePublication(input.Kind, protection.LastPublished, input.Fingerprint) {
		return accounts.PublishTask{}, &accountError{http.StatusConflict, "ALREADY_PUBLISHED", "这份内容已经发布成功，无需重复发布"}
	}
	task, err := m.store.EnqueuePublishTask(ctx, input, protection.Revision)
	if err != nil {
		if errors.Is(err, accounts.ErrTaskDuplicateRequest) || errors.Is(err, accounts.ErrTaskActive) || errors.Is(err, accounts.ErrTaskProtectionConflict) || errors.Is(err, accounts.ErrTaskUncertain) {
			if existing, lookupErr := m.existing(ctx, input); !errors.Is(lookupErr, accounts.ErrTaskNotFound) {
				return existing, lookupErr
			}
		}
		return accounts.PublishTask{}, err
	}
	launched = true
	go m.run(task, runtime, release)
	return task, nil
}

func (m *PublishTaskManager) existing(ctx context.Context, input accounts.NewPublishTask) (accounts.PublishTask, error) {
	if task, err := m.store.PublishTaskByRequest(ctx, input.AccountID, input.RequestID); err == nil {
		if task.Fingerprint != input.Fingerprint || task.Kind != input.Kind {
			return accounts.PublishTask{}, &accountError{http.StatusConflict, "IDEMPOTENCY_KEY_REUSED", "同一发布请求标识不能用于不同内容，请重新检查并确认"}
		}
		return task, nil
	} else if !errors.Is(err, accounts.ErrTaskNotFound) {
		return accounts.PublishTask{}, err
	}
	if _, err := m.store.ActivePublishTask(ctx, input.AccountID); err == nil {
		return accounts.PublishTask{}, accounts.ErrTaskActive
	} else {
		return accounts.PublishTask{}, err
	}
}

func (m *PublishTaskManager) run(task accounts.PublishTask, runtime *accountRuntime, release func()) {
	defer m.active.Done()
	defer release()
	ctx, cancel := context.WithTimeout(m.ctx, m.timeout)
	defer cancel()
	if err := runtime.waitBrowserIdle(ctx); err != nil {
		if m.ctx.Err() == nil {
			m.complete(task, accounts.TaskOutcome{Status: accounts.TaskFailed, Message: "等待账号浏览器失败", Error: err.Error()})
		}
		return
	}
	if m.ctx.Err() != nil {
		return
	}
	started, err := m.store.StartPublishTask(ctx, task.ID)
	if err != nil {
		if m.ctx.Err() == nil {
			m.complete(task, accounts.TaskOutcome{Status: accounts.TaskFailed, Message: "任务未能开始", Error: err.Error()})
		}
		return
	}
	result, err := m.invoke(ctx, runtime, started)
	outcome := accounts.TaskOutcome{Status: accounts.TaskSucceeded, Message: "发布完成", Result: result}
	if err != nil {
		outcome = accounts.TaskOutcome{Status: accounts.TaskFailed, Message: "发布失败，请查看失败原因", Error: err.Error()}
		if errors.Is(err, xhsErrors.ErrPublishUncertain) {
			outcome.Status = accounts.TaskUncertain
			outcome.Message = "发布结果待确认，请先到小红书核对后再处理发布保护"
		}
	}
	m.complete(started, outcome)
}

func (m *PublishTaskManager) invoke(ctx context.Context, runtime *accountRuntime, task accounts.PublishTask) (result json.RawMessage, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: 发布任务执行异常: %v", xhsErrors.ErrPublishUncertain, recovered)
		}
	}()
	return m.execute(ctx, runtime, task)
}

func (m *PublishTaskManager) complete(task accounts.PublishTask, outcome accounts.TaskOutcome) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := m.store.FinishPublishTask(ctx, task.ID, outcome); err != nil {
		logrus.WithField("task_id", task.ID).WithError(err).Error("保存发布任务结果失败，保留发布保护")
	}
}

func (m *PublishTaskManager) publish(ctx context.Context, runtime *accountRuntime, task accounts.PublishTask) (json.RawMessage, error) {
	record, err := m.store.Get(runtime.id)
	if err != nil {
		return nil, err
	}
	if record.UserID != task.UserID {
		return nil, accounts.ErrIdentityMismatch
	}
	service := &XiaohongshuService{account: runtime, waitForBrowser: true}
	switch task.Kind {
	case accounts.TaskKindPublish:
		var req PublishRequest
		if err := json.Unmarshal(task.Payload, &req); err != nil {
			return nil, fmt.Errorf("读取图文任务失败: %w", err)
		}
		result, err := service.PublishContent(ctx, &req)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	case accounts.TaskKindPublishVideo:
		var req PublishVideoRequest
		if err := json.Unmarshal(task.Payload, &req); err != nil {
			return nil, fmt.Errorf("读取视频任务失败: %w", err)
		}
		result, err := service.PublishVideo(ctx, &req)
		if err != nil {
			return nil, err
		}
		return json.Marshal(result)
	default:
		return nil, fmt.Errorf("不支持的发布任务类型: %s", task.Kind)
	}
}

// publicationFingerprint 归一化可选字段，保留图片和标签的顺序。
type publicationFingerprint struct {
	Title      string   `json:"title"`
	Content    string   `json:"content"`
	Images     []string `json:"images,omitempty"`
	Video      string   `json:"video,omitempty"`
	Tags       []string `json:"tags"`
	Visibility string   `json:"visibility"`
	AtUsers    []string `json:"at_users,omitempty"`
	ScheduleAt string   `json:"schedule_at,omitempty"`
	IsOriginal bool     `json:"is_original,omitempty"`
	Products   []string `json:"products,omitempty"`
}

func (p *publicationFingerprint) encode() (string, error) {
	if p.Tags == nil {
		p.Tags = []string{}
	}
	if p.Visibility == "" {
		p.Visibility = "公开可见"
	}
	data, err := json.Marshal(p)
	return string(data), err
}

func samePublication(kind accounts.TaskKind, stored, fingerprint string) bool {
	if stored == "" {
		return false
	}
	var value publicationFingerprint
	if json.Unmarshal([]byte(stored), &value) != nil {
		return false
	}
	if (kind == accounts.TaskKindPublish && len(value.Images) == 0) || (kind == accounts.TaskKindPublishVideo && value.Video == "") {
		return false
	}
	encoded, err := value.encode()
	return err == nil && encoded == fingerprint
}
