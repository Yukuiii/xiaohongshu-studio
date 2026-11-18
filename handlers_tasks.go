package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/Yukuiii/xiaohongshu-studio/accounts"
	"github.com/gin-gonic/gin"
)

const (
	maxPublishTaskBytes = 1 << 20
	idempotencyHeader   = "Idempotency-Key"
)

func (s *AppServer) requireTaskManager(c *gin.Context) bool {
	if s.tasks != nil {
		return true
	}
	respondError(c, http.StatusServiceUnavailable, "TASKS_UNAVAILABLE", "发布任务服务不可用", nil)
	return false
}

// taskImageIDs 从任务快照中提取归属于该账号的托管图片 ID，供任务页安全回显。
func (s *AppServer) taskImageIDs(task accounts.PublishTask) []string {
	if task.Kind != accounts.TaskKindPublish || len(task.Payload) == 0 {
		return nil
	}
	var payload struct {
		Images []string `json:"images"`
	}
	if json.Unmarshal(task.Payload, &payload) != nil || len(payload.Images) == 0 {
		return nil
	}
	ownDir, err := imageStorageDirForAccount(task.AccountID)
	if err != nil {
		return nil
	}
	baseDir, err := imageStorageDir()
	if err != nil {
		return nil
	}
	ownCanonical := canonicalPath(ownDir)
	baseCanonical := canonicalPath(baseDir)
	legacy := s.accounts != nil && task.AccountID == s.accounts.store.LegacyAccountID()
	ids := make([]string, 0, len(payload.Images))
	for _, item := range payload.Images {
		id := filepath.Base(item)
		if !workbenchImageID.MatchString(id) {
			continue
		}
		abs, err := filepath.Abs(item)
		if err != nil {
			continue
		}
		dir := canonicalPath(filepath.Dir(abs))
		if dir == ownCanonical || (legacy && dir == baseCanonical) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return ids
}

func (s *AppServer) respondTaskAccepted(c *gin.Context, task accounts.PublishTask) {
	task.ImageIDs = s.taskImageIDs(task)
	c.JSON(http.StatusAccepted, SuccessResponse{Success: true, Data: gin.H{"task": task}, Message: "发布任务已接收"})
}

func (s *AppServer) listPublishTasksHandler(c *gin.Context) {
	if !s.requireTaskManager(c) {
		return
	}
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "任务页码无效", nil)
		return
	}
	pageSize, err := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if err != nil || pageSize < 1 || pageSize > 100 {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "每页任务数量需为 1 至 100", nil)
		return
	}
	tasks, total, err := s.tasks.store.ListPublishTasks(c.Request.Context(), page, pageSize)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "TASK_LIST_FAILED", "读取任务列表失败", err)
		return
	}
	for i := range tasks {
		tasks[i].ImageIDs = s.taskImageIDs(tasks[i])
	}
	respondSuccess(c, gin.H{"tasks": tasks, "total": total, "page": page, "page_size": pageSize}, "任务列表已读取")
}

func (s *AppServer) getPublishTaskHandler(c *gin.Context) {
	if !s.requireTaskManager(c) {
		return
	}
	task, err := s.tasks.store.PublishTask(c.Request.Context(), c.Param("id"))
	if errors.Is(err, accounts.ErrInvalid) {
		respondError(c, http.StatusBadRequest, "INVALID_TASK", "任务标识无效", nil)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "TASK_READ_FAILED", "读取任务失败", err)
		return
	}
	task.ImageIDs = s.taskImageIDs(task)
	respondSuccess(c, gin.H{"task": task}, "任务已读取")
}
