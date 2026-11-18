package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const maxAIRequestBytes = 1 << 20

type composeSource struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Content string `json:"content"`
	URL     string `json:"url"`
	Author  string `json:"author"`
}

type composeRequest struct {
	Config       LLMConfig       `json:"config"`
	Topic        string          `json:"topic"`
	Instructions string          `json:"instructions"`
	Sources      []composeSource `json:"sources"`
}

type composedDraft struct {
	Title       string   `json:"title"`
	Content     string   `json:"content"`
	Tags        []string `json:"tags"`
	ImagePrompt string   `json:"image_prompt"`
}

func bindAIRequest(c *gin.Context, dst any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAIRequestBytes)
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(dst); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "请求格式不正确或超过 1MiB 上限", nil)
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST", "请求只能包含一个 JSON 对象", nil)
		return false
	}
	return true
}

func validateAIConfig(c *gin.Context, cfg LLMConfig, needModel bool) bool {
	if err := cfg.validate(needModel); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_CONFIG", err.Error(), nil)
		return false
	}
	return true
}

func (s *AppServer) resolveAIConfig(c *gin.Context, cfg *LLMConfig, kind string, needModel bool) bool {
	resolved, err := s.resolveLLMConfig(kind, *cfg)
	if err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_KIND", err.Error(), nil)
		return false
	}
	*cfg = resolved
	return validateAIConfig(c, resolved, needModel)
}

func respondAIError(c *gin.Context, err error) {
	status := http.StatusBadGateway
	if c.Request.Context().Err() != nil {
		status = http.StatusRequestTimeout
	}
	respondError(c, status, "AI_REQUEST_FAILED", err.Error(), nil)
}

func (s *AppServer) aiModelsHandler(c *gin.Context) {
	var req struct {
		Config LLMConfig `json:"config"`
		Kind   string    `json:"kind"`
	}
	if !bindAIRequest(c, &req) || !s.resolveAIConfig(c, &req.Config, req.Kind, false) {
		return
	}
	models, err := newLLMClient().listModels(c.Request.Context(), req.Config)
	if err != nil {
		respondAIError(c, err)
		return
	}
	respondSuccess(c, gin.H{"models": models}, "模型列表已读取")
}

func (s *AppServer) aiComposeHandler(c *gin.Context) {
	var req composeRequest
	if !bindAIRequest(c, &req) || !s.resolveAIConfig(c, &req.Config, "text", true) {
		return
	}
	if strings.TrimSpace(req.Topic) == "" || len(req.Sources) < 1 || len(req.Sources) > 5 {
		respondError(c, http.StatusBadRequest, "INVALID_SOURCES", "请填写选题并选择 1 至 5 条完整来源", nil)
		return
	}
	for _, source := range req.Sources {
		if strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.Content) == "" {
			respondError(c, http.StatusBadRequest, "INVALID_SOURCES", "来源缺少笔记标识或正文，请重新读取详情", nil)
			return
		}
	}
	draft, err := composeDraft(c.Request.Context(), req.Config, req.Topic, req.Instructions, req.Sources, s.promptTemplates())
	if err != nil {
		respondAIError(c, err)
		return
	}
	respondSuccess(c, draft, "草稿已生成，请编辑并确认后发布")
}

func parseComposedDraft(text string) (composedDraft, error) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		line := strings.IndexByte(text, '\n')
		if line < 0 || !strings.HasSuffix(text, "```") {
			return composedDraft{}, errors.New("模型草稿的 JSON 围栏不完整")
		}
		language := strings.TrimSpace(text[3:line])
		if language != "" && !strings.EqualFold(language, "json") {
			return composedDraft{}, errors.New("模型草稿不是 JSON")
		}
		text = strings.TrimSpace(text[line+1 : len(text)-3])
	}
	var draft composedDraft
	if err := json.Unmarshal([]byte(text), &draft); err != nil || strings.TrimSpace(draft.Title) == "" || strings.TrimSpace(draft.Content) == "" {
		return composedDraft{}, errors.New("模型草稿格式错误，需要有效的标题、正文和标签字段")
	}
	if draft.Tags == nil {
		draft.Tags = []string{}
	}
	return draft, nil
}

func (s *AppServer) aiImageHandler(c *gin.Context) {
	var req struct {
		Config LLMConfig `json:"config"`
		Prompt string    `json:"prompt"`
	}
	if !bindAIRequest(c, &req) || !s.resolveAIConfig(c, &req.Config, "image", true) {
		return
	}
	if strings.TrimSpace(req.Prompt) == "" {
		respondError(c, http.StatusBadRequest, "INVALID_PROMPT", "请填写图片描述", nil)
		return
	}
	data, err := newLLMClient().generateImage(c.Request.Context(), req.Config, req.Prompt)
	if err != nil {
		respondAIError(c, err)
		return
	}
	image, err := saveAccountImage(data, "AI 配图", requestAccountID(c))
	if err != nil {
		respondAIError(c, err)
		return
	}
	respondSuccess(c, gin.H{"image": image}, "图片已生成")
}
