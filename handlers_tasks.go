package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
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

func respondTaskAccepted(c *gin.Context, task accounts.PublishTask) {
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
	respondSuccess(c, gin.H{"task": task}, "任务已读取")
}
