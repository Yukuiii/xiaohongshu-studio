package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// OpenAI 兼容接口的轻量客户端。只依赖 base_url + api_key + model 三件套，
// DeepSeek / Kimi / Qwen / GLM / OpenAI 等主流服务都适配同一套协议。
// 配置由前端每次请求传入，服务端不保存。

// LLMConfig 一次模型调用所需的配置。
type LLMConfig struct {
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

// validate 校验必填项。needModel 为 false 时仅校验地址与 Key（如拉取模型列表）。
func (c LLMConfig) validate(needModel bool) error {
	if strings.TrimSpace(c.BaseURL) == "" {
		return errors.New("缺少接口地址 base_url")
	}
	if strings.TrimSpace(c.APIKey) == "" {
		return errors.New("缺少 api_key")
	}
	if needModel && strings.TrimSpace(c.Model) == "" {
		return errors.New("缺少模型名 model")
	}
	_, err := normalizeBaseURL(c.BaseURL)
	return err
}

// normalizeBaseURL 把用户填的地址规整为「不含端点后缀」的基础地址。
// 允许带尾斜杠，也允许直接粘贴完整的端点（如 /chat/completions），
// 统一裁掉端点部分，避免拼出 /v1/chat/completions/chat/completions。
func normalizeBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("接口地址为空")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", errors.New("接口地址必须是无账号、查询参数和片段的完整 http/https 地址")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/chat/completions", "/images/generations", "/models"} {
		if strings.HasSuffix(u.Path, suffix) {
			u.Path = strings.TrimSuffix(u.Path, suffix)
			break
		}
	}
	u.RawPath = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

const (
	llmChatTimeout  = 180 * time.Second
	llmListTimeout  = 30 * time.Second
	llmImageTimeout = 300 * time.Second
	// maxLLMResponseBytes 模型响应的读取上限，防止异常服务把内存打爆。
	maxLLMResponseBytes = 32 << 20
)

type llmClient struct {
	client *http.Client
}

func newLLMClient() *llmClient {
	// 不设全局超时，由每次调用各自的 context 控制时长。
	return &llmClient{client: &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}}}
}

// doJSON 发 JSON 请求并解析响应；非 2xx 仅返回状态码，避免密钥回显。
func (c *llmClient) doJSON(ctx context.Context, method, endpoint string, cfg LLMConfig, payload, out any) error {
	var body io.Reader
	if payload != nil {
		buf, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("序列化请求失败: %w", err)
		}
		body = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return errors.New("无法构造模型请求")
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return modelRequestError(ctx, "请求模型接口失败，请检查地址和网络")
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseBytes+1))
	if err != nil {
		return modelRequestError(ctx, "读取模型响应失败")
	}
	if len(data) > maxLLMResponseBytes {
		return errors.New("模型响应超过 32MiB 上限")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("模型接口返回 HTTP %d，请检查配置与模型支持情况", resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("模型接口未返回有效 JSON")
		}
	}
	return nil
}

// 不回显上游正文、URL 或网络错误，避免服务商将密钥带回响应。
func modelRequestError(ctx context.Context, message string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("模型请求已取消或超时: %w", err)
	}
	return errors.New(message)
}

// chat 调用 chat/completions，返回首条回复文本。
func (c *llmClient) chat(ctx context.Context, cfg LLMConfig, system, user string) (string, error) {
	base, err := normalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, llmChatTimeout)
	defer cancel()

	payload := map[string]any{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"temperature": 0.7,
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := c.doJSON(ctx, http.MethodPost, base+"/chat/completions", cfg, payload, &out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", errors.New("模型没有返回内容")
	}
	return out.Choices[0].Message.Content, nil
}

// listModels 拉取可选模型列表，供设置页下拉。
func (c *llmClient) listModels(ctx context.Context, cfg LLMConfig) ([]string, error) {
	base, err := normalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, llmListTimeout)
	defer cancel()

	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodGet, base+"/models", cfg, nil, &out); err != nil {
		return nil, err
	}
	models := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	if len(models) == 0 {
		return nil, errors.New("接口没有返回可用模型列表")
	}
	sort.Strings(models)
	return models, nil
}

// generateImage 调用 images/generations，返回图片字节。
// 兼容两种返回：data[0].b64_json 或 data[0].url（部分服务商只回链接）。
func (c *llmClient) generateImage(ctx context.Context, cfg LLMConfig, prompt string) ([]byte, error) {
	base, err := normalizeBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, llmImageTimeout)
	defer cancel()

	payload := map[string]any{
		"model":  cfg.Model,
		"prompt": prompt,
		"n":      1,
	}
	var out struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	if err := c.doJSON(ctx, http.MethodPost, base+"/images/generations", cfg, payload, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, errors.New("生图接口没有返回图片")
	}

	item := out.Data[0]
	if item.B64JSON != "" {
		if base64.StdEncoding.DecodedLen(len(item.B64JSON)) > maxImageBytes+2 {
			return nil, errors.New("图片超过 10MiB 上限")
		}
		data, err := base64.StdEncoding.DecodeString(item.B64JSON)
		if err != nil {
			return nil, fmt.Errorf("解析生图结果失败: %w", err)
		}
		if len(data) > maxImageBytes {
			return nil, errors.New("图片超过 10MiB 上限")
		}
		return data, nil
	}
	if item.URL != "" {
		return c.downloadImage(ctx, item.URL)
	}
	return nil, errors.New("生图接口返回中没有图片数据")
}

func (c *llmClient) downloadImage(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, errors.New("生图接口返回的图片地址无效")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errors.New("无法构造图片下载请求")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, modelRequestError(ctx, "下载生成图片失败")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("下载生成图片失败: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, modelRequestError(ctx, "下载生成图片失败")
	}
	if len(data) > maxImageBytes {
		return nil, errors.New("图片超过 10MiB 上限")
	}
	return data, nil
}
