package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

//go:embed prompt/compose-system.md
var composeSystemTemplate string

//go:embed prompt/compose-user.md
var composeUserTemplate string

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

// composeDraft 供手动和定时创作共用，完整保留每篇参考正文。
func composeDraft(ctx context.Context, cfg LLMConfig, topic, instructions, publicationType string, sources []composeSource) (composedDraft, error) {
	if strings.TrimSpace(topic) == "" || len(sources) < 1 || len(sources) > 20 {
		return composedDraft{}, fmt.Errorf("请填写选题并提供 1 至 20 篇完整参考")
	}
	for _, source := range sources {
		if strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.Content) == "" {
			return composedDraft{}, fmt.Errorf("参考资料缺少笔记标识或正文")
		}
	}
	if err := cfg.validate(true); err != nil {
		return composedDraft{}, err
	}
	publicationName, contentLimit := "图文", "1000"
	if publicationType == "long_text" {
		publicationName, contentLimit = "长文", "10000"
	}
	system, err := renderPrompt(composeSystemTemplate, map[string]string{"publication_type": publicationName, "title_limit": "20", "content_limit": contentLimit})
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
