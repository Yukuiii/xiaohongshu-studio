package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
	"github.com/sirupsen/logrus"
	"github.com/xpzouying/xiaohongshu-mcp/browser"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
	"github.com/xpzouying/xiaohongshu-mcp/cookies"
	"github.com/xpzouying/xiaohongshu-mcp/pkg/downloader"
	"github.com/xpzouying/xiaohongshu-mcp/pkg/xhsutil"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"
)

// XiaohongshuService 小红书业务服务
type XiaohongshuService struct {
	account *accountRuntime
	loginMu sync.Mutex
	logins  loginSessions
}

// NewXiaohongshuService 创建小红书服务实例
func NewXiaohongshuService() *XiaohongshuService {
	return &XiaohongshuService{}
}

// PublishRequest 发布请求
type PublishRequest struct {
	Title      string   `json:"title" binding:"required"`
	Content    string   `json:"content" binding:"required"`
	Images     []string `json:"images" binding:"required,min=1"`
	Tags       []string `json:"tags,omitempty"`
	AtUsers    []string `json:"at_users,omitempty"`    // 需要 @ 的用户昵称列表
	ScheduleAt string   `json:"schedule_at,omitempty"` // 定时发布时间，ISO8601格式，为空则立即发布
	IsOriginal bool     `json:"is_original,omitempty"` // 是否声明原创
	Visibility string   `json:"visibility,omitempty"`  // 可见范围: "公开可见"(默认), "仅自己可见", "仅互关好友可见"
	Products   []string `json:"products,omitempty"`    // 商品关键词列表，用于绑定带货商品
}

// LoginStatusResponse 登录状态响应
type LoginStatusResponse struct {
	AccountID  string                 `json:"account_id,omitempty"`
	SessionID  string                 `json:"session_id,omitempty"`
	ErrorCode  string                 `json:"error_code,omitempty"`
	IsLoggedIn bool                   `json:"is_logged_in"`
	Username   string                 `json:"username,omitempty"` // 当前登录账号的昵称
	UserID     string                 `json:"user_id,omitempty"`  // 用户唯一标识（个人主页 URL 中的 ID）
	Status     xiaohongshu.LoginPhase `json:"status"`
	Message    string                 `json:"message"`
}

// LoginBrowserResponse 可视化登录浏览器及当前会话进度。
type LoginBrowserResponse struct {
	LoginStatusResponse
	Timeout string `json:"timeout"`
}

// PublishResponse 发布响应
type PublishResponse struct {
	AccountID string   `json:"account_id,omitempty"`
	Title     string   `json:"title"`
	Content   string   `json:"content"`
	Images    int      `json:"images"`
	AtUsers   []string `json:"at_users,omitempty"`
	Status    string   `json:"status"`
}

// PublishVideoRequest 发布视频请求（仅支持本地单个视频文件）
type PublishVideoRequest struct {
	Title      string   `json:"title" binding:"required"`
	Content    string   `json:"content" binding:"required"`
	Video      string   `json:"video" binding:"required"`
	Tags       []string `json:"tags,omitempty"`
	ScheduleAt string   `json:"schedule_at,omitempty"` // 定时发布时间，ISO8601格式，为空则立即发布
	IsOriginal bool     `json:"is_original,omitempty"` // 是否声明原创
	Visibility string   `json:"visibility,omitempty"`  // 可见范围: "公开可见"(默认), "仅自己可见", "仅互关好友可见"
	Products   []string `json:"products,omitempty"`    // 商品关键词列表，用于绑定带货商品
}

// PublishVideoResponse 发布视频响应
type PublishVideoResponse struct {
	AccountID string `json:"account_id,omitempty"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Video     string `json:"video"`
	Status    string `json:"status"`
}

// FeedsListResponse Feeds列表响应
type FeedsListResponse struct {
	Feeds []xiaohongshu.Feed `json:"feeds"`
	Count int                `json:"count"`
}

// UserProfileResponse 用户主页响应
type UserProfileResponse struct {
	UserBasicInfo xiaohongshu.UserBasicInfo      `json:"userBasicInfo"`
	Interactions  []xiaohongshu.UserInteractions `json:"interactions"`
	Feeds         []xiaohongshu.Feed             `json:"feeds"`
}

// DeleteCookies 删除 cookies 文件，用于登录重置
func (s *XiaohongshuService) DeleteCookies(ctx context.Context) error {
	if s.account != nil {
		b, err := s.account.borrowBrowser(ctx, false)
		if err != nil {
			return err
		}
		defer b.Close()
		page := b.NewPage()
		defer page.Close()
		if err := (proto.NetworkClearBrowserCookies{}).Call(page.Context(ctx)); err != nil {
			return err
		}
		return nil
	}
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	s.logins.close()
	cookiePath := cookies.GetCookiesFilePath()
	cookieLoader := cookies.NewLoadCookie(cookiePath)
	return cookieLoader.DeleteCookies()
}

// CheckLoginStatus 活动期间只读取可视化会话，结束后重新检查真实登录态。
func (s *XiaohongshuService) CheckLoginStatus(ctx context.Context) (*LoginStatusResponse, error) {
	if s.account != nil {
		return s.account.loginStatus(ctx)
	}
	state, active := s.logins.snapshot()
	phase := state.phase
	if active {
		return loginSessionResponse(state), nil
	}

	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	loginAction := xiaohongshu.NewLogin(page)
	isLoggedIn, err := loginAction.CheckLoginStatus(ctx)
	if err != nil {
		return nil, err
	}

	if isLoggedIn {
		phase = xiaohongshu.LoginLoggedIn
	} else if phase != xiaohongshu.LoginExpired && phase != xiaohongshu.LoginFailed {
		phase = xiaohongshu.LoginNotLoggedIn
	}
	response := loginProgressResponse(phase)

	// 已登录时从当前页读取真实账号信息；读不到只记 warn，不影响状态返回。
	if isLoggedIn {
		if user, err := loginAction.CurrentUser(ctx); err != nil {
			logrus.Warnf("failed to get current user info: %v", err)
		} else {
			response.Username = user.Nickname
			response.UserID = user.UserID
		}
	}

	return response, nil
}

func loginProgressResponse(phase xiaohongshu.LoginPhase) *LoginStatusResponse {
	return &LoginStatusResponse{
		IsLoggedIn: phase == xiaohongshu.LoginLoggedIn,
		Status:     phase,
		Message:    loginPhaseMessage(phase),
	}
}

func loginSessionResponse(state loginSessionSnapshot) *LoginStatusResponse {
	response := loginProgressResponse(state.phase)
	response.Username = state.user.Nickname
	response.UserID = state.user.UserID
	return response
}

func loginPhaseMessage(phase xiaohongshu.LoginPhase) string {
	switch phase {
	case xiaohongshu.LoginWaitingScan:
		return "请使用手机小红书扫描二维码"
	case xiaohongshu.LoginWaitingConfirmation:
		return "扫码成功，请在手机小红书上确认本次网页登录"
	case xiaohongshu.LoginVerificationRequired:
		return "请在登录浏览器中通过账号安全验证，使用已登录该账号的小红书 APP 扫码验证身份"
	case xiaohongshu.LoginSaving:
		return "手机已确认，正在保存登录信息，请稍候"
	case xiaohongshu.LoginLoggedIn:
		return "登录成功"
	case xiaohongshu.LoginExpired:
		return "登录浏览器会话已到期，请重新打开登录浏览器"
	case xiaohongshu.LoginFailed:
		return "登录状态检查或登录信息保存失败，请结束登录窗口后重试"
	default:
		return "尚未登录，请打开登录浏览器并完成登录和账号验证"
	}
}

const loginBrowserTimeout = 20 * time.Minute

// OpenLoginBrowser 复用活动浏览器，不导航、不刷新二维码，也不延长会话期限。
func (s *XiaohongshuService) OpenLoginBrowser(ctx context.Context) (response *LoginBrowserResponse, err error) {
	if s.account != nil {
		return s.account.openLogin(ctx)
	}
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	state, active := s.logins.snapshot()
	if active && time.Now().Before(state.expiresAt) {
		return loginBrowserResponse(state), nil
	}
	if active {
		s.logins.expire(state.seq)
		s.logins.close()
	}
	if runtime.GOOS == "linux" && os.Getenv("DISPLAY") == "" {
		return nil, fmt.Errorf("可视化登录需要 DISPLAY，请使用包含内嵌桌面的应用镜像启动服务")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var b *browser.Browser
	handedOff := false
	defer func() {
		if recovered := recover(); recovered != nil {
			response = nil
			err = fmt.Errorf("打开登录浏览器失败: %v", recovered)
		}
	}()
	defer func() {
		if b != nil && !handedOff {
			b.Close()
		}
	}()
	b = browser.NewBrowser(false,
		browser.WithFingerprintSeed(configs.FingerprintSeed()),
		browser.WithProxy(configs.Proxy()),
	)
	page := b.NewPage()
	if err := page.Context(ctx).Timeout(5 * time.Second).SetWindow(&proto.BrowserBounds{WindowState: proto.BrowserWindowStateMaximized}); err != nil {
		logrus.Warnf("无法自动最大化登录窗口，可在浏览器中手动调整: %v", err)
	}
	loginAction := xiaohongshu.NewLogin(page)
	if err := loginAction.Open(ctx); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.startLoginSession(loginAction, page, b.Close, loginBrowserTimeout)
	handedOff = true
	state, _ = s.logins.snapshot()
	return loginBrowserResponse(state), nil
}

func loginBrowserResponse(state loginSessionSnapshot) *LoginBrowserResponse {
	remaining := time.Until(state.expiresAt).Round(time.Second)
	if remaining < 0 {
		remaining = 0
	}
	return &LoginBrowserResponse{
		LoginStatusResponse: *loginSessionResponse(state),
		Timeout:             remaining.String(),
	}
}

// CloseLoginBrowser 只结束可视化会话，已保存的 cookie 保持不变。
func (s *XiaohongshuService) CloseLoginBrowser() {
	if s.account != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := s.account.closeLogin(ctx, ""); err != nil {
			logrus.Warnf("结束登录窗口失败: %v", err)
		}
		return
	}
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	s.logins.close()
}

func (s *XiaohongshuService) startLoginSession(
	loginAction *xiaohongshu.LoginAction, page *rod.Page, closeBrowser func(), timeout time.Duration,
) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	expiresAt, _ := ctx.Deadline()
	var closeOnce sync.Once
	var seq uint64
	stop := func() {
		cancel()
		closeOnce.Do(func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					s.logins.setPhase(seq, xiaohongshu.LoginFailed)
					logrus.Errorf("关闭登录浏览器失败，会话 #%d: %v", seq, recovered)
				}
			}()
			closeBrowser()
		})
	}
	seq = s.logins.start(stop, expiresAt)
	logrus.Infof("已打开登录浏览器，会话 #%d，超时 %s", seq, timeout)

	go func() {
		setPhase := func(phase xiaohongshu.LoginPhase) bool {
			if !s.logins.setPhase(seq, phase) {
				return false
			}
			logrus.Infof("%s，会话 #%d", loginPhaseMessage(phase), seq)
			return true
		}
		defer s.logins.finish(seq)
		defer stop()
		defer func() {
			if recovered := recover(); recovered != nil && setPhase(xiaohongshu.LoginFailed) {
				logrus.Errorf("登录会话 #%d 异常: %v", seq, recovered)
			}
		}()

		err := loginAction.MonitorLogin(ctx, func(phase xiaohongshu.LoginPhase) error {
			if phase != xiaohongshu.LoginLoggedIn {
				setPhase(phase)
				return nil
			}
			state, active := s.logins.snapshot()
			if !active || state.seq != seq || state.phase == xiaohongshu.LoginLoggedIn {
				return nil
			}
			if state.phase != xiaohongshu.LoginSaving && !setPhase(xiaohongshu.LoginSaving) {
				return nil
			}
			saveCtx, cancelSave := context.WithTimeout(ctx, 10*time.Second)
			defer cancelSave()
			data, err := readLoginCookies(saveCtx, page)
			if err != nil {
				return err
			}
			var user xiaohongshu.CurrentUser
			if current, err := loginAction.CurrentUser(saveCtx); err != nil {
				logrus.Warnf("读取登录账号信息失败，会话 #%d: %v", seq, err)
			} else {
				user = *current
			}
			// 读取 cookie 和账号信息期间可能出现验证；落盘前重新检查真实页面。
			current, err := loginAction.CurrentPhase(saveCtx)
			if err != nil {
				return err
			}
			if current != xiaohongshu.LoginLoggedIn {
				setPhase(current)
				return nil
			}
			saved, err := s.logins.save(seq, saveCtx, user, func() error {
				loader := cookies.NewLoadCookie(cookies.GetCookiesFilePath())
				return loader.SaveCookies(data)
			})
			if saved {
				logrus.Infof("登录信息已保存，会话 #%d；自动关闭登录浏览器", seq)
				cancel()
			}
			return err
		})
		switch {
		case errors.Is(err, context.DeadlineExceeded) && errors.Is(ctx.Err(), context.DeadlineExceeded):
			s.logins.expire(seq)
		case errors.Is(err, context.Canceled):
			// 保存成功、显式关闭或替换后结束监控，保留最终登录状态。
		case err != nil:
			if setPhase(xiaohongshu.LoginFailed) {
				logrus.Errorf("检查或保存登录状态失败，会话 #%d: %v", seq, err)
			}
		}
	}()
}

// PublishContent 发布内容
func (s *XiaohongshuService) PublishContent(ctx context.Context, req *PublishRequest) (*PublishResponse, error) {
	// 验证标题长度（小红书限制：最大20个字）
	if xhsutil.CalcTitleLength(req.Title) > 20 {
		return nil, fmt.Errorf("标题长度超过限制")
	}

	imagePaths, err := s.processImages(req.Images)
	if err != nil {
		return nil, err
	}

	var scheduleTime *time.Time
	if req.ScheduleAt != "" {
		t, err := time.Parse(time.RFC3339, req.ScheduleAt)
		if err != nil {
			return nil, fmt.Errorf("定时发布时间格式错误，请使用 ISO8601 格式: %v", err)
		}

		// 校验定时发布时间范围：1小时至14天
		now := time.Now()
		minTime := now.Add(1 * time.Hour)
		maxTime := now.Add(14 * 24 * time.Hour)

		if t.Before(minTime) {
			return nil, fmt.Errorf("定时发布时间必须至少在1小时后，当前设置: %s，最早可选: %s",
				t.Format("2006-01-02 15:04"), minTime.Format("2006-01-02 15:04"))
		}
		if t.After(maxTime) {
			return nil, fmt.Errorf("定时发布时间不能超过14天，当前设置: %s，最晚可选: %s",
				t.Format("2006-01-02 15:04"), maxTime.Format("2006-01-02 15:04"))
		}

		scheduleTime = &t
		logrus.Infof("设置定时发布时间: %s", t.Format("2006-01-02 15:04"))
	}

	content := xiaohongshu.PublishImageContent{
		Title:        req.Title,
		Content:      req.Content,
		Tags:         req.Tags,
		AtUsers:      req.AtUsers,
		ImagePaths:   imagePaths,
		ScheduleTime: scheduleTime,
		IsOriginal:   req.IsOriginal,
		Visibility:   req.Visibility,
		Products:     req.Products,
	}

	if err := s.publishContent(ctx, content); err != nil {
		logrus.Errorf("发布内容失败: title=%s %v", content.Title, err)
		return nil, err
	}

	response := &PublishResponse{
		AccountID: s.accountID(),
		Title:     req.Title,
		Content:   req.Content,
		Images:    len(imagePaths),
		AtUsers:   req.AtUsers,
		Status:    "发布完成",
	}

	return response, nil
}

// processImages 处理图片列表，支持URL下载和本地路径
func (s *XiaohongshuService) processImages(images []string) ([]string, error) {
	if s.account != nil {
		paths, err := s.account.imagePaths(images)
		if err != nil {
			return nil, err
		}
		processor := downloader.NewImageProcessor(s.account.manager.store.DownloadPath(s.account.id))
		return processor.ProcessImages(paths)
	}
	processor := downloader.NewImageProcessor()
	return processor.ProcessImages(images)
}

// publishContent 执行内容发布
func (s *XiaohongshuService) publishContent(ctx context.Context, content xiaohongshu.PublishImageContent) error {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action, err := xiaohongshu.NewPublishImageAction(page)
	if err != nil {
		return err
	}

	return action.Publish(ctx, content)
}

// PublishVideo 发布视频（本地文件）
func (s *XiaohongshuService) PublishVideo(ctx context.Context, req *PublishVideoRequest) (*PublishVideoResponse, error) {
	// 标题长度校验（小红书限制：最大20个字）
	if xhsutil.CalcTitleLength(req.Title) > 20 {
		return nil, fmt.Errorf("标题长度超过限制")
	}

	// 本地视频文件校验
	if s.account != nil && req.Video != "" {
		paths, err := s.account.imagePaths([]string{req.Video})
		if err != nil {
			return nil, err
		}
		req.Video = paths[0]
	}
	if req.Video == "" {
		return nil, fmt.Errorf("必须提供本地视频文件")
	}
	if _, err := os.Stat(req.Video); err != nil {
		return nil, fmt.Errorf("视频文件不存在或不可访问: %v", err)
	}

	var scheduleTime *time.Time
	if req.ScheduleAt != "" {
		t, err := time.Parse(time.RFC3339, req.ScheduleAt)
		if err != nil {
			return nil, fmt.Errorf("定时发布时间格式错误，请使用 ISO8601 格式: %v", err)
		}

		// 校验定时发布时间范围：1小时至14天
		now := time.Now()
		minTime := now.Add(1 * time.Hour)
		maxTime := now.Add(14 * 24 * time.Hour)

		if t.Before(minTime) {
			return nil, fmt.Errorf("定时发布时间必须至少在1小时后，当前设置: %s，最早可选: %s",
				t.Format("2006-01-02 15:04"), minTime.Format("2006-01-02 15:04"))
		}
		if t.After(maxTime) {
			return nil, fmt.Errorf("定时发布时间不能超过14天，当前设置: %s，最晚可选: %s",
				t.Format("2006-01-02 15:04"), maxTime.Format("2006-01-02 15:04"))
		}

		scheduleTime = &t
		logrus.Infof("设置定时发布时间: %s", t.Format("2006-01-02 15:04"))
	}

	content := xiaohongshu.PublishVideoContent{
		Title:        req.Title,
		Content:      req.Content,
		Tags:         req.Tags,
		VideoPath:    req.Video,
		ScheduleTime: scheduleTime,
		IsOriginal:   req.IsOriginal,
		Visibility:   req.Visibility,
		Products:     req.Products,
	}

	if err := s.publishVideo(ctx, content); err != nil {
		return nil, err
	}

	resp := &PublishVideoResponse{
		AccountID: s.accountID(),
		Title:     req.Title,
		Content:   req.Content,
		Video:     req.Video,
		Status:    "发布完成",
	}
	return resp, nil
}

// publishVideo 执行视频发布
func (s *XiaohongshuService) publishVideo(ctx context.Context, content xiaohongshu.PublishVideoContent) error {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action, err := xiaohongshu.NewPublishVideoAction(page)
	if err != nil {
		return err
	}

	return action.PublishVideo(ctx, content)
}

// ListFeeds 获取Feeds列表
func (s *XiaohongshuService) ListFeeds(ctx context.Context) (*FeedsListResponse, error) {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewFeedsListAction(page)

	feeds, err := action.GetFeedsList(ctx)
	if err != nil {
		logrus.Errorf("获取 Feeds 列表失败: %v", err)
		return nil, err
	}

	response := &FeedsListResponse{
		Feeds: feeds,
		Count: len(feeds),
	}

	return response, nil
}

func (s *XiaohongshuService) SearchFeeds(ctx context.Context, keyword string, filters ...xiaohongshu.FilterOption) (*FeedsListResponse, error) {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewSearchAction(page)

	feeds, err := action.Search(ctx, keyword, filters...)
	if err != nil {
		return nil, err
	}

	response := &FeedsListResponse{
		Feeds: feeds,
		Count: len(feeds),
	}

	return response, nil
}

// GetFeedDetail 获取Feed详情
func (s *XiaohongshuService) GetFeedDetail(ctx context.Context, feedID, xsecToken string, loadAllComments bool) (*FeedDetailResponse, error) {
	return s.GetFeedDetailWithConfig(ctx, feedID, xsecToken, loadAllComments, xiaohongshu.DefaultCommentLoadConfig())
}

// GetFeedDetailWithConfig 使用配置获取Feed详情
func (s *XiaohongshuService) GetFeedDetailWithConfig(ctx context.Context, feedID, xsecToken string, loadAllComments bool, config xiaohongshu.CommentLoadConfig) (*FeedDetailResponse, error) {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewFeedDetailAction(page)

	result, err := action.GetFeedDetailWithConfig(ctx, feedID, xsecToken, loadAllComments, config)
	if err != nil {
		return nil, err
	}

	response := &FeedDetailResponse{
		FeedID: feedID,
		Data:   result,
	}

	return response, nil
}

// UserProfile 获取用户信息
func (s *XiaohongshuService) UserProfile(ctx context.Context, userID, xsecToken, tab string) (*UserProfileResponse, error) {
	parsed, err := xiaohongshu.ParseProfileTab(tab)
	if err != nil {
		return nil, err
	}

	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewUserProfileAction(page)

	result, err := action.UserProfile(ctx, userID, xsecToken, parsed)
	if err != nil {
		return nil, err
	}
	response := &UserProfileResponse{
		UserBasicInfo: result.UserBasicInfo,
		Interactions:  result.Interactions,
		Feeds:         result.Feeds,
	}

	return response, nil

}

// PostCommentToFeed 发表评论到Feed
func (s *XiaohongshuService) PostCommentToFeed(ctx context.Context, feedID, xsecToken, content string) (*PostCommentResponse, error) {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewCommentFeedAction(page)

	if err := action.PostComment(ctx, feedID, xsecToken, content); err != nil {
		return nil, err
	}

	return &PostCommentResponse{FeedID: feedID, Success: true, Message: "评论发表成功"}, nil
}

// LikeFeed 点赞笔记
func (s *XiaohongshuService) LikeFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewLikeAction(page)
	if err := action.Like(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "点赞成功或已点赞"}, nil
}

// UnlikeFeed 取消点赞笔记
func (s *XiaohongshuService) UnlikeFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewLikeAction(page)
	if err := action.Unlike(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "取消点赞成功或未点赞"}, nil
}

// FavoriteFeed 收藏笔记
func (s *XiaohongshuService) FavoriteFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewFavoriteAction(page)
	if err := action.Favorite(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "收藏成功或已收藏"}, nil
}

// UnfavoriteFeed 取消收藏笔记
func (s *XiaohongshuService) UnfavoriteFeed(ctx context.Context, feedID, xsecToken string) (*ActionResult, error) {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewFavoriteAction(page)
	if err := action.Unfavorite(ctx, feedID, xsecToken); err != nil {
		return nil, err
	}
	return &ActionResult{FeedID: feedID, Success: true, Message: "取消收藏成功或未收藏"}, nil
}

// ReplyCommentToFeed 回复指定评论
func (s *XiaohongshuService) ReplyCommentToFeed(ctx context.Context, feedID, xsecToken, commentID, userID, content string) (*ReplyCommentResponse, error) {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	action := xiaohongshu.NewCommentFeedAction(page)

	if err := action.ReplyToComment(ctx, feedID, xsecToken, commentID, userID, content); err != nil {
		return nil, err
	}

	return &ReplyCommentResponse{
		FeedID:          feedID,
		TargetCommentID: commentID,
		TargetUserID:    userID,
		Success:         true,
		Message:         "评论回复成功",
	}, nil
}

// GetUnreadCount 获取通知未读数
func (s *XiaohongshuService) GetUnreadCount(ctx context.Context) (*xiaohongshu.NotificationCount, error) {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	return xiaohongshu.NewNotificationAction(page).UnreadCount(ctx)
}

// ListNotifications 获取指定分区的通知列表
func (s *XiaohongshuService) ListNotifications(ctx context.Context, tab string, limit int) (*xiaohongshu.NotificationList, error) {
	parsed, err := xiaohongshu.ParseNotificationTab(tab)
	if err != nil {
		return nil, err
	}

	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	return xiaohongshu.NewNotificationAction(page).List(ctx, parsed, limit)
}

// LikeNotification 给通知里的评论点赞或取消点赞
func (s *XiaohongshuService) LikeNotification(ctx context.Context, commentID string, unlike bool) (*xiaohongshu.NotificationLikeResult, error) {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	return xiaohongshu.NewNotificationAction(page).Like(ctx, commentID, unlike)
}

// ReplyNotification 在通知页就地回复评论
func (s *XiaohongshuService) ReplyNotification(ctx context.Context, commentID, content string) (*xiaohongshu.NotificationReplyResult, error) {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	return xiaohongshu.NewNotificationAction(page).Reply(ctx, commentID, content)
}

func (s *XiaohongshuService) newBrowser(ctx context.Context) (*browserLease, error) {
	if s.account != nil {
		return s.account.borrowBrowser(ctx, true)
	}
	b := browser.NewBrowser(configs.IsHeadless(),
		browser.WithFingerprintSeed(configs.FingerprintSeed()),
		browser.WithProxy(configs.Proxy()),
	)
	return &browserLease{accountBrowser: b, release: b.Close}, nil
}

func readLoginCookies(ctx context.Context, page *rod.Page) ([]byte, error) {
	cks, err := page.Browser().Context(ctx).GetCookies()
	if err != nil {
		return nil, err
	}
	return json.Marshal(cks)
}

// withBrowserPage 执行需要浏览器页面的操作的通用函数
func (s *XiaohongshuService) withBrowserPage(ctx context.Context, fn func(*rod.Page) error) error {
	b, err := s.newBrowser(ctx)
	if err != nil {
		return err
	}
	defer b.Close()

	page := b.NewPage()
	defer page.Close()

	return fn(page)
}

// GetMyProfile 获取当前登录用户的个人信息
func (s *XiaohongshuService) GetMyProfile(ctx context.Context, tab string) (*UserProfileResponse, error) {
	parsed, err := xiaohongshu.ParseProfileTab(tab)
	if err != nil {
		return nil, err
	}

	var result *xiaohongshu.UserProfileResponse

	err = s.withBrowserPage(ctx, func(page *rod.Page) error {
		action := xiaohongshu.NewUserProfileAction(page)
		result, err = action.GetMyProfileViaSidebar(ctx, parsed)
		return err
	})

	if err != nil {
		return nil, err
	}

	response := &UserProfileResponse{
		UserBasicInfo: result.UserBasicInfo,
		Interactions:  result.Interactions,
		Feeds:         result.Feeds,
	}

	return response, nil
}
