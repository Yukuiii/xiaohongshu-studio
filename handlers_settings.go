package main

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

func (s *AppServer) getSettingsHandler(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if s.settings == nil {
		respondSettingsError(c, errSettingsUnavailable)
		return
	}
	respondSuccess(c, s.settings.snapshot().view(), "模型配置已读取")
}

func (s *AppServer) putSettingsHandler(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var input settingsUpdate
	if !bindAIRequest(c, &input) {
		return
	}
	settings, err := s.settings.update(input)
	if err != nil {
		respondSettingsError(c, err)
		return
	}
	respondSuccess(c, settings.view(), "模型配置已保存")
}

func (s *AppServer) deleteSettingsHandler(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	settings, err := s.settings.clear()
	if err != nil {
		respondSettingsError(c, err)
		return
	}
	respondSuccess(c, settings.view(), "模型配置已清除")
}

func respondSettingsError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, errSettingsInitialized):
		respondError(c, http.StatusConflict, "SETTINGS_ALREADY_INITIALIZED", err.Error(), nil)
	case errors.Is(err, errInvalidSettings):
		respondError(c, http.StatusBadRequest, "INVALID_CONFIG", err.Error(), nil)
	default:
		respondError(c, http.StatusServiceUnavailable, "SETTINGS_UNAVAILABLE", "模型配置读写失败，请检查存储目录", nil)
	}
}
