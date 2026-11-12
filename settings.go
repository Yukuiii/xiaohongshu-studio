package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/xpzouying/xiaohongshu-mcp/cookies"
)

var (
	errSettingsInitialized = errors.New("模型配置已经初始化")
	errSettingsUnavailable = errors.New("模型配置存储不可用")
	errInvalidSettings     = errors.New("模型配置无效")
	errInvalidSettingsKind = errors.New("kind 必须为 text 或 image")
)

type modelSettings struct {
	Initialized bool      `json:"initialized"`
	Text        LLMConfig `json:"text"`
	Image       LLMConfig `json:"image"`
}

type settingsConfigView struct {
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	HasAPIKey bool   `json:"has_api_key"`
}

type settingsView struct {
	Initialized bool               `json:"initialized"`
	Text        settingsConfigView `json:"text"`
	Image       settingsConfigView `json:"image"`
}

func (s modelSettings) view() settingsView {
	redact := func(c LLMConfig) settingsConfigView {
		return settingsConfigView{BaseURL: c.BaseURL, Model: c.Model, HasAPIKey: c.APIKey != ""}
	}
	return settingsView{Initialized: s.Initialized, Text: redact(s.Text), Image: redact(s.Image)}
}

type settingsConfigInput struct {
	BaseURL string          `json:"base_url"`
	Model   string          `json:"model"`
	APIKey  json.RawMessage `json:"api_key,omitempty"`
}

type settingsUpdate struct {
	Text                settingsConfigInput `json:"text"`
	Image               settingsConfigInput `json:"image"`
	OnlyIfUninitialized bool                `json:"only_if_uninitialized"`
}

type settingsStore struct {
	mu   sync.RWMutex
	path string
	data modelSettings
}

func defaultSettingsPath() string {
	if path := os.Getenv("XHS_SETTINGS_PATH"); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(cookies.GetCookiesFilePath()), ".xhs-settings.json")
}

// openSettingsStore 只在生产装配时调用，测试自行指定临时路径。
func openSettingsStore(path string) (*settingsStore, error) {
	if path == "" {
		return nil, errSettingsUnavailable
	}
	store := &settingsStore{path: path}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, errors.New("无法读取模型配置文件")
	}
	var saved struct {
		Initialized *bool      `json:"initialized"`
		Text        *LLMConfig `json:"text"`
		Image       *LLMConfig `json:"image"`
	}
	if json.Unmarshal(data, &saved) != nil || saved.Initialized == nil || saved.Text == nil || saved.Image == nil {
		return nil, errors.New("模型配置文件无效，请检查文件")
	}
	text, textErr := normalizeSettingsConfig(*saved.Text)
	image, imageErr := normalizeSettingsConfig(*saved.Image)
	if textErr != nil || imageErr != nil || (!*saved.Initialized && (!text.empty() || !image.empty())) {
		return nil, errors.New("模型配置文件无效，请检查文件")
	}
	store.data = modelSettings{Initialized: *saved.Initialized, Text: text, Image: image}
	return store, nil
}

func (s *settingsStore) snapshot() modelSettings {
	if s == nil {
		return modelSettings{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

func (c LLMConfig) empty() bool {
	return strings.TrimSpace(c.BaseURL) == "" && strings.TrimSpace(c.Model) == "" && strings.TrimSpace(c.APIKey) == ""
}

func normalizeSettingsConfig(c LLMConfig) (LLMConfig, error) {
	c.BaseURL = strings.TrimSpace(c.BaseURL)
	c.Model = strings.TrimSpace(c.Model)
	c.APIKey = strings.TrimSpace(c.APIKey)
	if c.empty() {
		return LLMConfig{}, nil
	}
	if err := c.validate(true); err != nil {
		return LLMConfig{}, err
	}
	c.BaseURL, _ = normalizeBaseURL(c.BaseURL)
	return c, nil
}

func sameSettingsURL(a, b string) bool {
	first, err := normalizeBaseURL(a)
	if err != nil {
		return false
	}
	second, err := normalizeBaseURL(b)
	return err == nil && first == second
}

func mergeSettingsConfig(input settingsConfigInput, previous LLMConfig) (LLMConfig, error) {
	cfg := LLMConfig{BaseURL: input.BaseURL, Model: input.Model}
	if len(input.APIKey) != 0 {
		var key *string
		if json.Unmarshal(input.APIKey, &key) != nil || key == nil {
			return LLMConfig{}, errors.New("api_key 必须是字符串")
		}
		cfg.APIKey = *key
	} else if previous.APIKey != "" {
		if !sameSettingsURL(cfg.BaseURL, previous.BaseURL) {
			return LLMConfig{}, errors.New("更改接口地址时请重新填写 api_key，清空配置时请显式清空 api_key")
		}
		cfg.APIKey = previous.APIKey
	}
	return normalizeSettingsConfig(cfg)
}

func (s *settingsStore) update(input settingsUpdate) (modelSettings, error) {
	if s == nil {
		return modelSettings{}, errSettingsUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.OnlyIfUninitialized && s.data.Initialized {
		return modelSettings{}, errSettingsInitialized
	}
	text, err := mergeSettingsConfig(input.Text, s.data.Text)
	if err != nil {
		return modelSettings{}, fmt.Errorf("%w：文本配置：%v", errInvalidSettings, err)
	}
	image, err := mergeSettingsConfig(input.Image, s.data.Image)
	if err != nil {
		return modelSettings{}, fmt.Errorf("%w：图片配置：%v", errInvalidSettings, err)
	}
	return s.saveLocked(modelSettings{Initialized: true, Text: text, Image: image})
}

func (s *settingsStore) clear() (modelSettings, error) {
	if s == nil {
		return modelSettings{}, errSettingsUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// 保留初始化标记，防止旧浏览器重新导入已清除的配置。
	return s.saveLocked(modelSettings{Initialized: true})
}

func (s *settingsStore) saveLocked(next modelSettings) (modelSettings, error) {
	data, err := json.Marshal(next)
	if err != nil {
		return modelSettings{}, errSettingsUnavailable
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return modelSettings{}, errSettingsUnavailable
	}
	file, err := os.CreateTemp(dir, ".xhs-settings-*.tmp")
	if err != nil {
		return modelSettings{}, errSettingsUnavailable
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return modelSettings{}, errSettingsUnavailable
	}
	if err := file.Sync(); err != nil {
		return modelSettings{}, errSettingsUnavailable
	}
	if err := file.Close(); err != nil {
		return modelSettings{}, errSettingsUnavailable
	}
	if err := os.Rename(file.Name(), s.path); err != nil {
		return modelSettings{}, errSettingsUnavailable
	}
	s.data = next
	return next, nil
}

func (s *AppServer) resolveLLMConfig(kind string, cfg LLMConfig) (LLMConfig, error) {
	saved := s.settings.snapshot()
	var stored LLMConfig
	switch kind {
	case "", "text":
		stored = saved.Text
	case "image":
		stored = saved.Image
	default:
		return LLMConfig{}, errInvalidSettingsKind
	}
	if cfg.empty() {
		return stored, nil
	}
	if strings.TrimSpace(cfg.APIKey) == "" && sameSettingsURL(cfg.BaseURL, stored.BaseURL) {
		cfg.APIKey = stored.APIKey
	}
	return cfg, nil
}
