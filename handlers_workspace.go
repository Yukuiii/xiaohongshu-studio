package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/xpzouying/xiaohongshu-mcp/accounts"
)

const maxDraftBytes = 1 << 20

func (s *AppServer) draftAccount(c *gin.Context) (*accountRuntime, *accounts.Store, bool) {
	if s.accounts == nil {
		respondError(c, http.StatusServiceUnavailable, "DRAFTS_UNAVAILABLE", "草稿存储需要账号模式", nil)
		return nil, nil, false
	}
	account := requestAccount(c)
	if account == nil {
		respondError(c, http.StatusBadRequest, "ACCOUNT_REQUIRED", "请明确选择要操作的小红书账号", nil)
		return nil, nil, false
	}
	return account, s.accounts.store, true
}

func (s *AppServer) getDraftHandler(c *gin.Context) {
	_, store, ok := s.draftAccount(c)
	if !ok {
		return
	}
	draft, found, err := store.LoadDraft(requestAccountID(c))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "DRAFT_READ_FAILED", "读取草稿失败", err)
		return
	}
	if !found {
		respondSuccess(c, gin.H{"draft": nil, "revision": 0}, "尚无服务端草稿")
		return
	}
	respondSuccess(c, gin.H{
		"draft":      json.RawMessage(draft.Payload),
		"revision":   draft.Revision,
		"updated_at": draft.UpdatedAt,
	}, "草稿已读取")
}

func (s *AppServer) putDraftHandler(c *gin.Context) {
	_, store, ok := s.draftAccount(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxDraftBytes)
	var req struct {
		Draft        json.RawMessage `json:"draft"`
		BaseRevision int64           `json:"base_revision"`
	}
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "草稿内容无效或超过 1MiB", nil)
		return
	}
	if len(req.Draft) == 0 || string(req.Draft) == "null" || req.BaseRevision < 0 {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "草稿内容无效", nil)
		return
	}
	draft, err := store.SaveDraft(requestAccountID(c), req.Draft, req.BaseRevision)
	if errors.Is(err, accounts.ErrRevisionConflict) {
		respondError(c, http.StatusConflict, "DRAFT_REVISION_CONFLICT", "草稿已在其他窗口更新，请先读取最新版本", nil)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "DRAFT_SAVE_FAILED", "保存草稿失败", err)
		return
	}
	respondSuccess(c, gin.H{"revision": draft.Revision, "updated_at": draft.UpdatedAt}, "草稿已保存")
}

func (s *AppServer) deleteDraftHandler(c *gin.Context) {
	_, store, ok := s.draftAccount(c)
	if !ok {
		return
	}
	if err := store.DeleteDraft(requestAccountID(c)); err != nil {
		respondError(c, http.StatusInternalServerError, "DRAFT_DELETE_FAILED", "清空草稿失败", err)
		return
	}
	respondSuccess(c, gin.H{"revision": 0}, "草稿已清空")
}

func (s *AppServer) getPublishProtectionHandler(c *gin.Context) {
	_, store, ok := s.draftAccount(c)
	if !ok {
		return
	}
	protection, err := store.LoadProtection(requestAccountID(c))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "PROTECTION_READ_FAILED", "读取发布保护失败", err)
		return
	}
	respondSuccess(c, gin.H{
		"revision":       protection.Revision,
		"uncertain":      protection.Uncertain,
		"last_published": protection.LastPublished,
	}, "发布保护已读取")
}

func (s *AppServer) putPublishProtectionHandler(c *gin.Context) {
	_, store, ok := s.draftAccount(c)
	if !ok {
		return
	}
	var req struct {
		BaseRevision  int64  `json:"base_revision"`
		Uncertain     bool   `json:"uncertain"`
		LastPublished string `json:"last_published"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxDraftBytes)
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil || req.BaseRevision < 0 {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "发布保护内容无效", nil)
		return
	}
	protection, err := store.SaveProtection(requestAccountID(c), req.BaseRevision, req.Uncertain, req.LastPublished, nil)
	if errors.Is(err, accounts.ErrRevisionConflict) {
		respondError(c, http.StatusConflict, "PROTECTION_REVISION_CONFLICT", "发布记录已在其他窗口更新，请先核对结果", nil)
		return
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "PROTECTION_SAVE_FAILED", "保存发布保护失败", err)
		return
	}
	respondSuccess(c, gin.H{"revision": protection.Revision}, "发布保护已保存")
}
