package main

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *AppServer) listPromptsHandler(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if s.prompts == nil {
		respondPromptError(c, errPromptUnavailable)
		return
	}
	respondSuccess(c, gin.H{"prompts": s.prompts.view()}, "提示词已读取")
}

func (s *AppServer) putPromptHandler(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if s.prompts == nil {
		respondPromptError(c, errPromptUnavailable)
		return
	}
	var input struct {
		Content string `json:"content"`
	}
	if !bindAIRequest(c, &input) {
		return
	}
	view, err := s.prompts.save(c.Param("key"), input.Content)
	if err != nil {
		respondPromptError(c, err)
		return
	}
	respondSuccess(c, gin.H{"prompt": view}, "提示词已保存")
}

func (s *AppServer) deletePromptHandler(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if s.prompts == nil {
		respondPromptError(c, errPromptUnavailable)
		return
	}
	view, err := s.prompts.reset(c.Param("key"))
	if err != nil {
		respondPromptError(c, err)
		return
	}
	respondSuccess(c, gin.H{"prompt": view}, "已恢复默认提示词")
}

func respondPromptError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errPromptNotFound):
		respondError(c, http.StatusNotFound, "PROMPT_NOT_FOUND", "提示词模板不存在", nil)
	case errors.Is(err, errInvalidPrompt):
		respondError(c, http.StatusBadRequest, "INVALID_PROMPT", err.Error(), nil)
	default:
		respondError(c, http.StatusServiceUnavailable, "PROMPTS_UNAVAILABLE", "提示词读写失败，请检查存储目录", nil)
	}
}
