package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Yukuiii/xiaohongshu-studio/accounts"
)

func TestPrepareRecentNotesUnicodeBoundaries(t *testing.T) {
	titleAtLimit := strings.Repeat("中", 79) + "🙂"
	contentAtLimit := strings.Repeat("文", 1199) + "🌿"
	for _, tc := range []struct {
		name      string
		note      accounts.PublishedNote
		truncated bool
	}{
		{name: "empty"},
		{name: "below limits", note: accounts.PublishedNote{Title: "中文🙂", Content: "正文🌿"}},
		{name: "exact limits", note: accounts.PublishedNote{Title: titleAtLimit, Content: contentAtLimit}},
		{name: "title over limit", note: accounts.PublishedNote{Title: titleAtLimit + "末", Content: "正文"}, truncated: true},
		{name: "content over limit", note: accounts.PublishedNote{Title: "标题", Content: contentAtLimit + "末"}, truncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := prepareRecentNotes([]accounts.PublishedNote{tc.note})[0]
			if got.Truncated != tc.truncated {
				t.Fatalf("truncated = %v, want %v", got.Truncated, tc.truncated)
			}
			if !utf8.ValidString(got.Title) || !utf8.ValidString(got.Content) {
				t.Fatal("truncation split a UTF-8 character")
			}
			if utf8.RuneCountInString(got.Title) > 80 || utf8.RuneCountInString(got.Content) > 1200 {
				t.Fatal("truncation exceeded character budget")
			}
			if !tc.truncated && (got.Title != tc.note.Title || got.Content != tc.note.Content) {
				t.Fatal("text within budget changed")
			}
			if tc.name == "title over limit" && (got.Title != titleAtLimit || got.Content != tc.note.Content) {
				t.Fatal("title truncation did not preserve the exact 80-character prefix")
			}
			if tc.name == "content over limit" && (utf8.RuneCountInString(got.Content) != 1200 || !strings.HasSuffix(got.Content, "🌿末")) {
				t.Fatal("body truncation did not fill its budget and preserve the ending")
			}
		})
	}
}

func TestPrepareRecentNotesPreservesLongBodyEnding(t *testing.T) {
	const marker = "\n……（省略）……\n"
	head := strings.Repeat("中", 891) + "🙂"
	tail := "🌿" + strings.Repeat("尾", 297)
	content := head + strings.Repeat("省", 10000) + tail
	got := prepareRecentNotes([]accounts.PublishedNote{{Title: "长文", Content: content}})[0]
	if got.Content != head+marker+tail {
		t.Fatal("long body did not preserve the expected three-quarter prefix and one-quarter suffix")
	}
	if !got.Truncated || utf8.RuneCountInString(got.Content) != 1200 || !utf8.ValidString(got.Content) {
		t.Fatal("long body must be marked truncated and occupy exactly 1200 valid Unicode characters including the marker")
	}
}

func TestPrepareRecentNotesOrderLimitAndInputUnchanged(t *testing.T) {
	notes := []accounts.PublishedNote{
		{Title: strings.Repeat("一", 81), Content: strings.Repeat("首", 1500)},
		{Title: "二", Content: "第二篇"},
		{Title: "三", Content: "第三篇"},
		{Title: "四", Content: "第四篇"},
		{Title: "五", Content: "第五篇"},
		{Title: "六", Content: "第六篇"},
	}
	original := append([]accounts.PublishedNote(nil), notes...)
	got := prepareRecentNotes(notes)
	if len(got) != 5 {
		t.Fatalf("got %d notes, want 5", len(got))
	}
	if got[0].Title != strings.Repeat("一", 80) || !got[0].Truncated {
		t.Fatal("first note was lost or not truncated")
	}
	for i, note := range got[1:] {
		if note.Title != notes[i+1].Title || note.Content != notes[i+1].Content || note.Truncated {
			t.Fatalf("note %d changed order or content", i+1)
		}
	}
	if !reflect.DeepEqual(notes, original) {
		t.Fatal("preparing history mutated its input")
	}
	got[0].Title = "修改输出"
	if !reflect.DeepEqual(notes, original) {
		t.Fatal("prepared history shares mutable storage with its input")
	}
}

func TestPrepareRecentNotesEmptyArray(t *testing.T) {
	for _, notes := range [][]accounts.PublishedNote{nil, {}} {
		encoded, err := json.Marshal(prepareRecentNotes(notes))
		if err != nil {
			t.Fatal(err)
		}
		if string(encoded) != "[]" {
			t.Fatalf("empty history encoded as %s, want []", encoded)
		}
	}
}
