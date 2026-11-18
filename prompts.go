package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Yukuiii/xiaohongshu-studio/accounts"
)

//go:embed prompt/compose-system.md
var composeSystemTemplate string

//go:embed prompt/compose-user.md
var composeUserTemplate string

//go:embed prompt/schedule-system.md
var scheduleSystemTemplate string

//go:embed prompt/schedule-user.md
var scheduleUserTemplate string

//go:embed prompt/image.md
var imagePromptTemplate string

var promptVariable = regexp.MustCompile(`\{\{\s*([a-z_]+)\s*\}\}`)

// renderPrompt 只替换模板变量，不再次解释资料中出现的双花括号。
func renderPrompt(template string, values map[string]string) (string, error) {
	var missing string
	result := promptVariable.ReplaceAllStringFunc(template, func(match string) string {
		key := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(match, "{{"), "}}"))
		value, ok := values[key]
		if !ok {
			missing = key
		}
		return value
	})
	if missing != "" {
		return "", fmt.Errorf("提示词模板缺少变量: %s", missing)
	}
	return result, nil
}

// composeDraft 根据手动选取的完整参考创作。
func composeDraft(ctx context.Context, cfg LLMConfig, topic, instructions string, sources []composeSource) (composedDraft, error) {
	if strings.TrimSpace(topic) == "" || len(sources) < 1 || len(sources) > 5 {
		return composedDraft{}, fmt.Errorf("请填写选题并提供 1 至 5 篇完整参考")
	}
	for _, source := range sources {
		if strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.Content) == "" {
			return composedDraft{}, fmt.Errorf("参考资料缺少笔记标识或正文")
		}
	}
	if err := cfg.validate(true); err != nil {
		return composedDraft{}, err
	}
	system, err := renderPrompt(composeSystemTemplate, map[string]string{"publication_type": "图文", "title_limit": "20", "content_limit": "1000"})
	if err != nil {
		return composedDraft{}, err
	}
	encodedTopic, _ := json.Marshal(topic)
	encodedInstructions, _ := json.Marshal(instructions)
	encodedSources, err := json.Marshal(sources)
	if err != nil {
		return composedDraft{}, err
	}
	user, err := renderPrompt(composeUserTemplate, map[string]string{
		"topic": string(encodedTopic), "instructions": string(encodedInstructions), "sources": string(encodedSources),
	})
	if err != nil {
		return composedDraft{}, err
	}
	text, err := newLLMClient().chat(ctx, cfg, system, user)
	if err != nil {
		return composedDraft{}, err
	}
	return parseComposedDraft(text)
}

// composeKeywordDraft 让定时任务围绕关键词自主选择主题并创作。
func composeKeywordDraft(ctx context.Context, cfg LLMConfig, keyword, instructions, publicationType string, recentNotes []accounts.PublishedNote) (composedDraft, error) {
	if strings.TrimSpace(keyword) == "" {
		return composedDraft{}, fmt.Errorf("请填写创作关键词")
	}
	if err := cfg.validate(true); err != nil {
		return composedDraft{}, err
	}
	publicationName, contentLimit := "图文", "1000"
	if publicationType == "long_text" {
		publicationName, contentLimit = "长文", "10000"
	}
	system, err := renderPrompt(scheduleSystemTemplate, map[string]string{"publication_type": publicationName, "title_limit": "20", "content_limit": contentLimit})
	if err != nil {
		return composedDraft{}, err
	}
	encodedKeyword, _ := json.Marshal(keyword)
	encodedInstructions, _ := json.Marshal(instructions)
	encodedRecentNotes, err := json.Marshal(prepareRecentNotes(recentNotes))
	if err != nil {
		return composedDraft{}, err
	}
	user, err := renderPrompt(scheduleUserTemplate, map[string]string{
		"keyword": string(encodedKeyword), "instructions": string(encodedInstructions), "recent_notes": string(encodedRecentNotes),
	})
	if err != nil {
		return composedDraft{}, err
	}
	text, err := newLLMClient().chat(ctx, cfg, system, user)
	if err != nil {
		return composedDraft{}, err
	}
	return parseComposedDraft(text)
}

type recentPromptNote struct {
	Title     string `json:"title"`
	Content   string `json:"content"`
	Truncated bool   `json:"truncated,omitempty"`
}

func prepareRecentNotes(notes []accounts.PublishedNote) []recentPromptNote {
	count := len(notes)
	if count > 5 {
		count = 5
	}
	prepared := make([]recentPromptNote, count)
	for i, note := range notes[:count] {
		title, titleTruncated := truncateRecentNoteText(note.Title, 80, false)
		content, contentTruncated := truncateRecentNoteText(note.Content, 1200, true)
		prepared[i] = recentPromptNote{Title: title, Content: content, Truncated: titleTruncated || contentTruncated}
	}
	return prepared
}

// truncateRecentNoteText 按字符截断；正文给省略标记留出预算并保留结尾。
func truncateRecentNoteText(text string, limit int, keepTail bool) (string, bool) {
	const marker = "\n……（省略）……\n"
	headBudget, tailBudget := limit, 0
	if keepTail {
		available := limit - utf8.RuneCountInString(marker)
		headBudget = available * 3 / 4
		tailBudget = available - headBudget
	}
	headEnd, count, truncated := len(text), 0, false
	for index := range text {
		if count == headBudget {
			headEnd = index
		}
		if count == limit {
			truncated = true
			break
		}
		count++
	}
	if !truncated {
		return text, false
	}
	if !keepTail {
		return text[:headEnd], true
	}
	tailStart := len(text)
	for range tailBudget {
		_, size := utf8.DecodeLastRuneInString(text[:tailStart])
		tailStart -= size
	}
	return text[:headEnd] + marker + text[tailStart:], true
}
