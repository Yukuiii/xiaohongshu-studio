package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

func TestRunFeedBodyBatchLimitsConcurrencyAndKeepsOrder(t *testing.T) {
	items := make([]FeedBodyRequest, 5)
	for index := range items {
		items[index] = FeedBodyRequest{FeedID: fmt.Sprintf("feed%018d", index), XsecToken: "token"}
	}
	started := make(chan string, len(items))
	release := make(chan struct{})
	done := make(chan []FeedBodyResult, 1)
	go func() {
		done <- runFeedBodyBatch(context.Background(), items, func(_ context.Context, feedID, _ string) (xiaohongshu.FeedBody, error) {
			started <- feedID
			<-release
			if feedID == items[2].FeedID {
				return xiaohongshu.FeedBody{}, fmt.Errorf("该篇正文不可用")
			}
			return xiaohongshu.FeedBody{FeedID: feedID, Title: "标题" + feedID, Content: "正文", Author: "作者", URL: "https://example.invalid/" + feedID}, nil
		})
	}()

	for range feedBodyConcurrency {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("并发读取未启动")
		}
	}
	select {
	case <-started:
		t.Fatal("同时读取超过并发上限")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)

	var results []FeedBodyResult
	select {
	case results = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("批量读取未结束")
	}
	require.Len(t, results, len(items))
	for index, result := range results {
		require.Equal(t, items[index].FeedID, result.FeedID, "结果必须保持输入顺序")
	}
	require.Equal(t, "标题"+items[0].FeedID, results[0].Title)
	require.Equal(t, "正文", results[0].Content)
	require.Equal(t, "作者", results[0].Author)
	require.Equal(t, "该篇正文不可用", results[2].Error)
	require.Empty(t, results[2].Content)
	require.Equal(t, "正文", results[4].Content)
}

func TestRunFeedBodyBatchReportsCancellationPerItem(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	items := []FeedBodyRequest{{FeedID: "feed000000000001", XsecToken: "token"}, {FeedID: "feed000000000002", XsecToken: "token"}}
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	results := runFeedBodyBatch(ctx, items, func(ctx context.Context, _, _ string) (xiaohongshu.FeedBody, error) {
		<-ctx.Done()
		return xiaohongshu.FeedBody{}, ctx.Err()
	})
	require.Len(t, results, 2)
	for _, result := range results {
		require.Equal(t, context.Canceled.Error(), result.Error)
	}
}
