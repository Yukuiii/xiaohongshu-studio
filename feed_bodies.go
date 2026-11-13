package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	xproxy "golang.org/x/net/proxy"

	"github.com/xpzouying/xiaohongshu-mcp/configs"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

// feedBodyConcurrency 控制一批正文请求的并发数，兼顾等待时长和请求集中度。
const feedBodyConcurrency = 3

// FetchFeedBodies 借用账号浏览器核验一次身份，再用同一会话并发读取参考正文。
func (s *XiaohongshuService) FetchFeedBodies(ctx context.Context, items []FeedBodyRequest) ([]FeedBodyResult, error) {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	userAgent, cookies, err := b.Session(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取账号会话失败: %w", err)
	}
	client, err := newFeedBodyHTTPClient()
	if err != nil {
		return nil, err
	}
	fetcher, err := xiaohongshu.NewFeedBodyFetcher(client, userAgent, cookies)
	if err != nil {
		return nil, fmt.Errorf("准备正文请求失败: %w", err)
	}

	return runFeedBodyBatch(ctx, items, fetcher.Fetch), nil
}

type feedBodyFetchFunc func(context.Context, string, string) (xiaohongshu.FeedBody, error)

// runFeedBodyBatch 限制并发并保持输入顺序，单篇失败只写回该篇。
func runFeedBodyBatch(ctx context.Context, items []FeedBodyRequest, fetch feedBodyFetchFunc) []FeedBodyResult {
	results := make([]FeedBodyResult, len(items))
	slots := make(chan struct{}, feedBodyConcurrency)
	var wait sync.WaitGroup
	for index, item := range items {
		wait.Add(1)
		go func(index int, item FeedBodyRequest) {
			defer wait.Done()
			result := FeedBodyResult{FeedID: item.FeedID}
			select {
			case slots <- struct{}{}:
				defer func() { <-slots }()
			case <-ctx.Done():
				result.Error = "请求已取消"
				results[index] = result
				return
			}
			body, err := fetch(ctx, item.FeedID, item.XsecToken)
			if err != nil {
				result.Error = err.Error()
				results[index] = result
				return
			}
			result.Title = body.Title
			result.Content = body.Content
			result.Author = body.Author
			result.URL = body.URL
			results[index] = result
		}(index, item)
	}
	wait.Wait()
	return results
}

// newFeedBodyHTTPClient 构造与浏览器一致的代理配置，供只读正文请求使用。
func newFeedBodyHTTPClient() (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	proxyValue := strings.TrimSpace(configs.Proxy())
	if proxyValue == "" {
		return &http.Client{Transport: transport}, nil
	}
	proxyURL, err := url.Parse(proxyValue)
	if err != nil || proxyURL.Host == "" {
		return nil, fmt.Errorf("代理地址无效: %s", proxyValue)
	}

	switch proxyURL.Scheme {
	case "http", "https":
		transport.Proxy = http.ProxyURL(proxyURL)
	case "socks5", "socks5h":
		var auth *xproxy.Auth
		if proxyURL.User != nil {
			password, _ := proxyURL.User.Password()
			auth = &xproxy.Auth{User: proxyURL.User.Username(), Password: password}
		}
		dialer, err := xproxy.SOCKS5("tcp", proxyURL.Host, auth, xproxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("准备代理失败: %w", err)
		}
		contextDialer, ok := dialer.(xproxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("代理不支持可取消的拨号")
		}
		transport.Proxy = nil
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return contextDialer.DialContext(ctx, network, address)
		}
	default:
		return nil, fmt.Errorf("不支持的代理协议: %s", proxyURL.Scheme)
	}
	return &http.Client{Transport: transport}, nil
}
