package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/Yukuiii/xiaohongshu-studio/accounts"
	"github.com/gin-gonic/gin"
)

type scheduledJobView struct {
	accounts.ScheduledJob
	ConfigurationError string `json:"configuration_error"`
	RetryAllowed       bool   `json:"retry_allowed"`
}

func (s *AppServer) scheduleModels(publicationType string) (LLMConfig, LLMConfig, error) {
	settings := s.settings.snapshot()
	if err := settings.Text.validate(true); err != nil {
		return LLMConfig{}, LLMConfig{}, errors.New("文字模型未配置或配置不完整，请到设置中保存文字模型后重试")
	}
	if publicationType != "long_text" {
		if err := settings.Image.validate(true); err != nil {
			return LLMConfig{}, LLMConfig{}, errors.New("生图模型未配置或配置不完整，请到设置中保存生图模型后重试")
		}
	}
	return settings.Text, settings.Image, nil
}

func (s *AppServer) scheduleView(job accounts.ScheduledJob) scheduledJobView {
	view := scheduledJobView{ScheduledJob: job}
	if _, _, err := s.scheduleModels(job.PublicationType); err != nil {
		view.ConfigurationError = err.Error()
	}
	if (job.LastRunStatus == "failed" || job.LastRunStatus == "skipped") && job.ActiveRunID == "" {
		protection, err := s.accounts.store.LoadProtection(job.AccountID)
		view.RetryAllowed = err == nil && !protection.Uncertain && protection.ActiveTaskID == ""
	}
	return view
}

func (s *AppServer) requireScheduleManager(c *gin.Context) bool {
	if s.schedules != nil && s.accounts != nil {
		return true
	}
	respondError(c, http.StatusServiceUnavailable, "SCHEDULES_UNAVAILABLE", "定时任务服务不可用", nil)
	return false
}

func respondScheduleError(c *gin.Context, err error) {
	if failure := accountFailure(err); failure != nil && !errors.Is(err, accounts.ErrInvalid) {
		respondError(c, failure.Status, failure.Code, failure.Message, nil)
		return
	}
	switch {
	case errors.Is(err, accounts.ErrScheduleNotFound):
		respondError(c, http.StatusNotFound, "SCHEDULE_NOT_FOUND", "定时任务不存在", nil)
	case errors.Is(err, accounts.ErrScheduleActive):
		respondError(c, http.StatusConflict, "SCHEDULE_ACTIVE", "此账号或定时任务正在执行，请等待完成后再操作", nil)
	case errors.Is(err, accounts.ErrScheduleNotRetryable):
		respondError(c, http.StatusConflict, "SCHEDULE_NOT_RETRYABLE", "只能重试最近一次明确失败或已跳过的执行，结果未知时请先核对实际发布情况", nil)
	case errors.Is(err, accounts.ErrInvalid):
		respondError(c, http.StatusBadRequest, "INVALID_SCHEDULE", err.Error(), nil)
	case errors.Is(err, accounts.ErrNotFound):
		respondError(c, http.StatusNotFound, "ACCOUNT_NOT_FOUND", "所选账号不存在或已移除", nil)
	default:
		respondError(c, http.StatusInternalServerError, "SCHEDULE_FAILED", "操作定时任务失败", err)
	}
}

func (s *AppServer) bindScheduleInput(c *gin.Context) (accounts.ScheduledJobInput, bool) {
	var input accounts.ScheduledJobInput
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(&input); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_SCHEDULE", "定时任务配置格式无效或内容过长", nil)
		return input, false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		respondError(c, http.StatusBadRequest, "INVALID_SCHEDULE", "请求只能包含一个配置对象", nil)
		return input, false
	}
	return input, true
}

func (s *AppServer) listSchedulesHandler(c *gin.Context) {
	if !s.requireScheduleManager(c) {
		return
	}
	jobs, err := s.accounts.store.ListScheduledJobs(c.Request.Context())
	if err != nil {
		respondScheduleError(c, err)
		return
	}
	views := make([]scheduledJobView, 0, len(jobs))
	for _, job := range jobs {
		views = append(views, s.scheduleView(job))
	}
	respondSuccess(c, gin.H{"schedules": views}, "定时任务已读取")
}

func (s *AppServer) createScheduleHandler(c *gin.Context) {
	if !s.requireScheduleManager(c) {
		return
	}
	input, ok := s.bindScheduleInput(c)
	if !ok {
		return
	}
	job, err := s.accounts.store.CreateScheduledJob(c.Request.Context(), input, time.Now())
	if err != nil {
		respondScheduleError(c, err)
		return
	}
	c.JSON(http.StatusCreated, SuccessResponse{Success: true, Data: gin.H{"schedule": s.scheduleView(job)}, Message: "定时任务已创建"})
}

func (s *AppServer) getScheduleHandler(c *gin.Context) {
	if !s.requireScheduleManager(c) {
		return
	}
	job, err := s.accounts.store.ScheduledJob(c.Request.Context(), c.Param("id"))
	if err != nil {
		respondScheduleError(c, err)
		return
	}
	respondSuccess(c, gin.H{"schedule": s.scheduleView(job)}, "定时任务已读取")
}

func (s *AppServer) updateScheduleHandler(c *gin.Context) {
	if !s.requireScheduleManager(c) {
		return
	}
	input, ok := s.bindScheduleInput(c)
	if !ok {
		return
	}
	job, err := s.accounts.store.UpdateScheduledJob(c.Request.Context(), c.Param("id"), input, time.Now())
	if err != nil {
		respondScheduleError(c, err)
		return
	}
	respondSuccess(c, gin.H{"schedule": s.scheduleView(job)}, "定时任务已保存")
}

func (s *AppServer) deleteScheduleHandler(c *gin.Context) {
	if !s.requireScheduleManager(c) {
		return
	}
	if err := s.accounts.store.DeleteScheduledJob(c.Request.Context(), c.Param("id")); err != nil {
		respondScheduleError(c, err)
		return
	}
	respondSuccess(c, gin.H{}, "定时任务已删除，执行记录已保留")
}

func (s *AppServer) listScheduleRunsHandler(c *gin.Context) {
	if !s.requireScheduleManager(c) {
		return
	}
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "执行记录页码无效", nil)
		return
	}
	pageSize, err := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil || pageSize < 1 || pageSize > 100 {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "每页记录数量需为 1 至 100", nil)
		return
	}
	runs, total, err := s.accounts.store.ListScheduleRuns(c.Request.Context(), c.Param("id"), page, pageSize)
	if err != nil {
		respondScheduleError(c, err)
		return
	}
	respondSuccess(c, gin.H{"runs": runs, "total": total, "page": page, "page_size": pageSize}, "执行记录已读取")
}

func (s *AppServer) retryScheduleHandler(c *gin.Context) {
	if !s.requireScheduleManager(c) {
		return
	}
	var input struct {
		RunID string `json:"run_id" binding:"required"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	if err := c.ShouldBindJSON(&input); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "请选择需要重试的失败执行记录", nil)
		return
	}
	run, err := s.schedules.Retry(c.Request.Context(), c.Param("id"), input.RunID)
	if err != nil {
		respondScheduleError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, SuccessResponse{Success: true, Data: gin.H{"run": run}, Message: "重试已接收，按当前配置执行"})
}
