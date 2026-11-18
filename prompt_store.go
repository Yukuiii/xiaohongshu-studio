package main

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxPromptBytes = 64 << 10

var (
	errPromptNotFound    = errors.New("提示词模板不存在")
	errPromptUnavailable = errors.New("提示词存储不可用")
	errInvalidPrompt     = errors.New("提示词模板无效")
)

// promptSpec 定义可编辑模板及运行时可用的变量；变量同时也是必填项。
type promptSpec struct {
	key       string
	variables []string
}

var promptSpecs = []promptSpec{
	{key: "compose_system", variables: []string{"publication_type", "title_limit", "content_limit"}},
	{key: "compose_user", variables: []string{"topic", "instructions", "sources"}},
	{key: "schedule_system", variables: []string{"publication_type", "title_limit", "content_limit"}},
	{key: "schedule_user", variables: []string{"keyword", "instructions", "recent_notes"}},
}

func promptSpecFor(key string) (promptSpec, bool) {
	for _, spec := range promptSpecs {
		if spec.key == key {
			return spec, true
		}
	}
	return promptSpec{}, false
}

type promptTemplateView struct {
	Key            string     `json:"key"`
	Content        string     `json:"content"`
	DefaultContent string     `json:"default_content"`
	Customized     bool       `json:"customized"`
	Variables      []string   `json:"variables"`
	UpdatedAt      *time.Time `json:"updated_at,omitempty"`
}

// promptStore 保存用户覆盖的模板；没有记录时使用编译时默认值。
type promptStore struct {
	mu       sync.RWMutex
	db       *sql.DB
	contents map[string]string
	updated  map[string]time.Time
}

func openPromptStore(database *sql.DB) (*promptStore, error) {
	if database == nil {
		return nil, errPromptUnavailable
	}
	store := &promptStore{
		db:       database,
		contents: make(map[string]string),
		updated:  make(map[string]time.Time),
	}
	rows, err := database.Query(`SELECT name, content, updated_at FROM prompt_templates`)
	if err != nil {
		return nil, errPromptUnavailable
	}
	defer rows.Close()
	for rows.Next() {
		var key, content string
		var updated int64
		if err := rows.Scan(&key, &content, &updated); err != nil {
			return nil, errPromptUnavailable
		}
		spec, ok := promptSpecFor(key)
		if !ok {
			return nil, fmt.Errorf("数据库包含未知提示词模板: %s", key)
		}
		if err := validatePromptContent(spec, content); err != nil {
			return nil, fmt.Errorf("提示词 %s 数据无效: %w", key, err)
		}
		store.contents[key] = content
		store.updated[key] = time.UnixMilli(updated).UTC()
	}
	if err := rows.Err(); err != nil {
		return nil, errPromptUnavailable
	}
	return store, nil
}

// snapshot 返回当前生效的完整模板集合；nil store 使用内置默认值。
func (s *promptStore) snapshot() promptSet {
	set := defaultPromptSet()
	if s == nil {
		return set
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if content, ok := s.contents["compose_system"]; ok {
		set.ComposeSystem = content
	}
	if content, ok := s.contents["compose_user"]; ok {
		set.ComposeUser = content
	}
	if content, ok := s.contents["schedule_system"]; ok {
		set.ScheduleSystem = content
	}
	if content, ok := s.contents["schedule_user"]; ok {
		set.ScheduleUser = content
	}
	return set
}

func (s *promptStore) view() []promptTemplateView {
	result := make([]promptTemplateView, 0, len(promptSpecs))
	if s == nil {
		for _, spec := range promptSpecs {
			result = append(result, defaultPromptView(spec))
		}
		return result
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, spec := range promptSpecs {
		result = append(result, s.viewLocked(spec))
	}
	return result
}

func (s *promptStore) save(key, content string) (promptTemplateView, error) {
	spec, ok := promptSpecFor(key)
	if !ok {
		return promptTemplateView{}, fmt.Errorf("%w: %s", errPromptNotFound, key)
	}
	if err := validatePromptContent(spec, content); err != nil {
		return promptTemplateView{}, err
	}
	if s == nil || s.db == nil {
		return promptTemplateView{}, errPromptUnavailable
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(
		`INSERT INTO prompt_templates (name, content, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET content = excluded.content, updated_at = excluded.updated_at`,
		key, content, now.UnixMilli(),
	); err != nil {
		return promptTemplateView{}, errPromptUnavailable
	}
	s.contents[key] = content
	s.updated[key] = now
	return s.viewLocked(spec), nil
}

func (s *promptStore) reset(key string) (promptTemplateView, error) {
	spec, ok := promptSpecFor(key)
	if !ok {
		return promptTemplateView{}, fmt.Errorf("%w: %s", errPromptNotFound, key)
	}
	if s == nil || s.db == nil {
		return promptTemplateView{}, errPromptUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.db.Exec(`DELETE FROM prompt_templates WHERE name = ?`, key); err != nil {
		return promptTemplateView{}, errPromptUnavailable
	}
	delete(s.contents, key)
	delete(s.updated, key)
	return s.viewLocked(spec), nil
}

func (s *promptStore) viewLocked(spec promptSpec) promptTemplateView {
	content, customized := s.contents[spec.key]
	defaultContent, _ := defaultPromptSet().content(spec.key)
	if !customized {
		content = defaultContent
	}
	view := promptTemplateView{
		Key:            spec.key,
		Content:        content,
		DefaultContent: defaultContent,
		Customized:     customized,
		Variables:      append([]string(nil), spec.variables...),
	}
	if customized {
		updated := s.updated[spec.key]
		view.UpdatedAt = &updated
	}
	return view
}

func defaultPromptView(spec promptSpec) promptTemplateView {
	content, _ := defaultPromptSet().content(spec.key)
	return promptTemplateView{
		Key:            spec.key,
		Content:        content,
		DefaultContent: content,
		Variables:      append([]string(nil), spec.variables...),
	}
}

func (p promptSet) content(key string) (string, bool) {
	switch key {
	case "compose_system":
		return p.ComposeSystem, true
	case "compose_user":
		return p.ComposeUser, true
	case "schedule_system":
		return p.ScheduleSystem, true
	case "schedule_user":
		return p.ScheduleUser, true
	default:
		return "", false
	}
}

func validatePromptContent(spec promptSpec, content string) error {
	if !utf8.ValidString(content) {
		return fmt.Errorf("%w：提示词必须是有效 UTF-8", errInvalidPrompt)
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("%w：提示词不能为空", errInvalidPrompt)
	}
	if len(content) > maxPromptBytes {
		return fmt.Errorf("%w：提示词不能超过 64 KiB", errInvalidPrompt)
	}
	seen := make(map[string]bool)
	for _, match := range promptVariable.FindAllStringSubmatch(content, -1) {
		name := strings.TrimSpace(match[1])
		if !slices.Contains(spec.variables, name) {
			return fmt.Errorf("%w：包含不可用变量 {{%s}}", errInvalidPrompt, name)
		}
		seen[name] = true
	}
	var missing []string
	for _, name := range spec.variables {
		if !seen[name] {
			missing = append(missing, "{{"+name+"}}")
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w：缺少必需变量 %s", errInvalidPrompt, strings.Join(missing, "、"))
	}
	return nil
}
