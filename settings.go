package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
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
	db   *sql.DB
	data modelSettings
}

// openSettingsStore 从 SQLite 读取模型配置；没有配置行时返回空配置。
func openSettingsStore(database *sql.DB) (*settingsStore, error) {
	if database == nil {
		return nil, errSettingsUnavailable
	}
	store := &settingsStore{db: database}
	var (
		initialized int
		text        LLMConfig
		image       LLMConfig
	)
	err := database.QueryRow(
		`SELECT initialized, text_base_url, text_model, text_api_key,
		 image_base_url, image_model, image_api_key FROM settings WHERE id = 1`,
	).Scan(
		&initialized, &text.BaseURL, &text.Model, &text.APIKey,
		&image.BaseURL, &image.Model, &image.APIKey,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return store, nil
	}
	if err != nil {
		return nil, errors.New("无法读取模型配置")
	}
	text, textErr := normalizeSettingsConfig(text)
	image, imageErr := normalizeSettingsConfig(image)
	if textErr != nil || imageErr != nil || (initialized == 0 && (!text.empty() || !image.empty())) {
		return nil, errors.New("模型配置数据无效，请检查数据库")
	}
	store.data = modelSettings{Initialized: initialized != 0, Text: text, Image: image}
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
	initialized := 0
	if next.Initialized {
		initialized = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO settings (
			id, initialized, text_base_url, text_model, text_api_key,
			image_base_url, image_model, image_api_key, updated_at
		) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			initialized = excluded.initialized,
			text_base_url = excluded.text_base_url,
			text_model = excluded.text_model,
			text_api_key = excluded.text_api_key,
			image_base_url = excluded.image_base_url,
			image_model = excluded.image_model,
			image_api_key = excluded.image_api_key,
			updated_at = excluded.updated_at`,
		initialized, next.Text.BaseURL, next.Text.Model, next.Text.APIKey,
		next.Image.BaseURL, next.Image.Model, next.Image.APIKey, time.Now().UnixMilli(),
	)
	if err != nil {
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
