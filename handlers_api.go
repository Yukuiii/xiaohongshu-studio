package main

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/xpzouying/xiaohongshu-mcp/cookies"
	"github.com/xpzouying/xiaohongshu-mcp/xiaohongshu"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// respondError 返回错误响应
func respondError(c *gin.Context, statusCode int, code, message string, details any) {
	if err, ok := details.(error); ok {
		if failure := accountFailure(err); failure != nil {
			statusCode, code, message, details = failure.Status, failure.Code, failure.Message, nil
		} else {
			details = err.Error()
		}
	}
	response := ErrorResponse{
		Error:   message,
		Code:    code,
		Details: details,
	}

	// 4xx 是调用方传错，5xx 才是服务端故障，用日志级别区分开。
	// 否则鉴权开启后被扫描器打，401 会把 ERROR 刷满。
	if statusCode < http.StatusInternalServerError {
		logrus.Warnf("%s %s %d", c.Request.Method, c.Request.URL.Path, statusCode)
	} else {
		logrus.Errorf("%s %s %d", c.Request.Method, c.Request.URL.Path, statusCode)
	}

	c.JSON(statusCode, response)
}

// respondSuccess 返回成功响应
func respondSuccess(c *gin.Context, data any, message string) {
	response := SuccessResponse{
		Success: true,
		Data:    data,
		Message: message,
	}

	logrus.Infof("%s %s %d", c.Request.Method, c.Request.URL.Path, http.StatusOK)

	c.JSON(http.StatusOK, response)
}

// loginSessionHandler 读取登录弹窗进度，不主动核验账号。
func (s *AppServer) loginSessionHandler(c *gin.Context) {
	status, err := s.service(c).LoginSessionStatus()
	if err != nil {
		respondError(c, http.StatusInternalServerError, "STATUS_CHECK_FAILED",
			"读取登录会话失败", err)
		return
	}

	respondSuccess(c, status, "读取登录会话成功")
}

// accountVerifyHandler 手动核验账号登录态，并更新缓存。
func (s *AppServer) accountVerifyHandler(c *gin.Context) {
	status, err := s.service(c).VerifyLoginStatus(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "STATUS_CHECK_FAILED",
			"检查登录状态失败", err)
		return
	}

	respondSuccess(c, status, "检查登录状态成功")
}

// deleteCookiesHandler 删除 cookies，重置登录状态
func (s *AppServer) deleteCookiesHandler(c *gin.Context) {
	err := s.service(c).DeleteCookies(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "DELETE_COOKIES_FAILED",
			"删除 cookies 失败", err)
		return
	}

	if r := requestAccount(c); r != nil {
		respondSuccess(c, gin.H{"account_id": r.id}, "该账号的 Cookie 已清除，其他浏览器数据保留")
		return
	}
	cookiePath := cookies.GetCookiesFilePath()
	respondSuccess(c, map[string]interface{}{
		"cookie_path": cookiePath,
		"message":     "Cookies 已成功删除，登录状态已重置。下次操作时需要重新登录。",
	}, "删除 cookies 成功")
}

// publishHandler 发布内容
func (s *AppServer) publishHandler(c *gin.Context) {
	var req PublishRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", err)
		return
	}

	result, err := s.service(c).PublishContent(c.Request.Context(), &req)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "PUBLISH_FAILED",
			"发布失败", err)
		return
	}

	respondSuccess(c, result, "发布成功")
}

// publishVideoHandler 发布视频内容
func (s *AppServer) publishVideoHandler(c *gin.Context) {
	var req PublishVideoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", err)
		return
	}

	result, err := s.service(c).PublishVideo(c.Request.Context(), &req)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "PUBLISH_VIDEO_FAILED",
			"视频发布失败", err)
		return
	}

	respondSuccess(c, result, "视频发布成功")
}

// listFeedsHandler 获取Feeds列表
func (s *AppServer) listFeedsHandler(c *gin.Context) {
	result, err := s.service(c).ListFeeds(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "LIST_FEEDS_FAILED",
			"获取Feeds列表失败", err)
		return
	}

	respondSuccess(c, result, "获取Feeds列表成功")
}

// searchFeedsHandler 搜索Feeds
func (s *AppServer) searchFeedsHandler(c *gin.Context) {
	var keyword string
	var filters xiaohongshu.FilterOption

	switch c.Request.Method {
	case http.MethodPost:
		// 对于POST请求，从JSON中获取keyword
		var searchReq SearchFeedsRequest
		if err := c.ShouldBindJSON(&searchReq); err != nil {
			respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
				"请求参数错误", err)
			return
		}
		keyword = searchReq.Keyword
		filters = searchReq.Filters
	default:
		keyword = c.Query("keyword")
	}

	if keyword == "" {
		respondError(c, http.StatusBadRequest, "MISSING_KEYWORD",
			"缺少关键词参数", "keyword parameter is required")
		return
	}

	result, err := s.service(c).SearchFeeds(c.Request.Context(), keyword, filters)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "SEARCH_FEEDS_FAILED",
			"搜索Feeds失败", err)
		return
	}

	respondSuccess(c, result, "搜索Feeds成功")
}

// getFeedDetailHandler 获取Feed详情
func (s *AppServer) getFeedDetailHandler(c *gin.Context) {
	var req FeedDetailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", err)
		return
	}

	var result *FeedDetailResponse
	var err error

	if req.CommentConfig != nil {
		config := xiaohongshu.CommentLoadConfig{
			ClickMoreReplies:    req.CommentConfig.ClickMoreReplies,
			MaxRepliesThreshold: req.CommentConfig.MaxRepliesThreshold,
			MaxCommentItems:     req.CommentConfig.MaxCommentItems,
			ScrollSpeed:         req.CommentConfig.ScrollSpeed,
		}
		result, err = s.service(c).GetFeedDetailWithConfig(c.Request.Context(), req.FeedID, req.XsecToken, req.LoadAllComments, config)
	} else {
		result, err = s.service(c).GetFeedDetail(c.Request.Context(), req.FeedID, req.XsecToken, req.LoadAllComments)
	}

	if err != nil {
		respondError(c, http.StatusInternalServerError, "GET_FEED_DETAIL_FAILED",
			"获取Feed详情失败", err)
		return
	}

	respondSuccess(c, result, "获取Feed详情成功")
}

// getFeedBodiesHandler 批量读取参考笔记的基础正文，供写稿使用。
func (s *AppServer) getFeedBodiesHandler(c *gin.Context) {
	var req FeedBodiesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", err)
		return
	}
	if len(req.Items) == 0 || len(req.Items) > 5 {
		respondError(c, http.StatusBadRequest, "INVALID_SOURCES",
			"每次请选择 1 至 5 篇参考笔记", nil)
		return
	}

	seen := make(map[string]bool, len(req.Items))
	items := make([]FeedBodyRequest, 0, len(req.Items))
	for _, item := range req.Items {
		item.FeedID = strings.TrimSpace(item.FeedID)
		item.XsecToken = strings.TrimSpace(item.XsecToken)
		if err := xiaohongshu.ValidateFeedRef(item.FeedID, item.XsecToken); err != nil {
			respondError(c, http.StatusBadRequest, "INVALID_SOURCES", err.Error(), nil)
			return
		}
		if seen[item.FeedID] {
			continue
		}
		seen[item.FeedID] = true
		items = append(items, item)
	}

	results, err := s.service(c).FetchFeedBodies(c.Request.Context(), items)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "GET_FEED_BODIES_FAILED",
			"读取参考正文失败", err)
		return
	}
	respondSuccess(c, gin.H{"items": results}, "参考正文已读取")
}

// userProfileHandler 用户主页
func (s *AppServer) userProfileHandler(c *gin.Context) {
	var req UserProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", err)
		return
	}

	result, err := s.service(c).UserProfile(c.Request.Context(), req.UserID, req.XsecToken, req.Tab)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "GET_USER_PROFILE_FAILED",
			"获取用户主页失败", err)
		return
	}

	respondSuccess(c, map[string]any{"data": result}, "result.Message")
}

// postCommentHandler 发表评论到Feed
func (s *AppServer) postCommentHandler(c *gin.Context) {
	var req PostCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", err)
		return
	}

	// 发表评论
	result, err := s.service(c).PostCommentToFeed(c.Request.Context(), req.FeedID, req.XsecToken, req.Content)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "POST_COMMENT_FAILED",
			"发表评论失败", err)
		return
	}

	respondSuccess(c, result, result.Message)
}

// replyCommentHandler 回复指定评论
func (s *AppServer) replyCommentHandler(c *gin.Context) {
	var req ReplyCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", err)
		return
	}

	result, err := s.service(c).ReplyCommentToFeed(c.Request.Context(), req.FeedID, req.XsecToken, req.CommentID, req.UserID, req.Content)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "REPLY_COMMENT_FAILED",
			"回复评论失败", err)
		return
	}

	respondSuccess(c, result, result.Message)
}

// likeFeedHandler 点赞/取消点赞
func (s *AppServer) likeFeedHandler(c *gin.Context) {
	var req LikeFeedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", err)
		return
	}

	var result *ActionResult
	var err error
	if req.Unlike {
		result, err = s.service(c).UnlikeFeed(c.Request.Context(), req.FeedID, req.XsecToken)
	} else {
		result, err = s.service(c).LikeFeed(c.Request.Context(), req.FeedID, req.XsecToken)
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "LIKE_FEED_FAILED",
			"点赞操作失败", err)
		return
	}

	respondSuccess(c, result, result.Message)
}

// favoriteFeedHandler 收藏/取消收藏
func (s *AppServer) favoriteFeedHandler(c *gin.Context) {
	var req FavoriteFeedRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", err)
		return
	}

	var result *ActionResult
	var err error
	if req.Unfavorite {
		result, err = s.service(c).UnfavoriteFeed(c.Request.Context(), req.FeedID, req.XsecToken)
	} else {
		result, err = s.service(c).FavoriteFeed(c.Request.Context(), req.FeedID, req.XsecToken)
	}
	if err != nil {
		respondError(c, http.StatusInternalServerError, "FAVORITE_FEED_FAILED",
			"收藏操作失败", err)
		return
	}

	respondSuccess(c, result, result.Message)
}

// healthHandler 健康检查
func healthHandler(c *gin.Context) {
	respondSuccess(c, map[string]any{
		"status":    "healthy",
		"service":   "xiaohongshu-studio",
		"version":   version,
		"timestamp": "now",
	}, "服务正常")
}

// myProfileHandler 我的信息
func (s *AppServer) myProfileHandler(c *gin.Context) {
	// 获取当前登录用户信息
	result, err := s.service(c).GetMyProfile(c.Request.Context(), c.Query("tab"))
	if err != nil {
		respondError(c, http.StatusInternalServerError, "GET_MY_PROFILE_FAILED",
			"获取我的主页失败", err)
		return
	}

	respondSuccess(c, map[string]any{"data": result}, "获取我的主页成功")
}

// getUnreadCountHandler 获取通知未读数
func (s *AppServer) getUnreadCountHandler(c *gin.Context) {
	result, err := s.service(c).GetUnreadCount(c.Request.Context())
	if err != nil {
		respondError(c, http.StatusInternalServerError, "GET_UNREAD_COUNT_FAILED",
			"获取未读数失败", err)
		return
	}

	respondSuccess(c, map[string]any{"data": result}, "获取未读数成功")
}

// listNotificationsHandler 获取通知列表。两个参数都可选，因此 GET 走 query、POST 走 JSON。
func (s *AppServer) listNotificationsHandler(c *gin.Context) {
	var req ListNotificationsRequest

	if c.Request.Method == http.MethodPost {
		if err := c.ShouldBindJSON(&req); err != nil {
			respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
				"请求参数错误", err)
			return
		}
	} else {
		req.Tab = c.Query("tab")
		if limit, err := strconv.Atoi(c.Query("limit")); err == nil {
			req.Limit = limit
		}
	}

	result, err := s.service(c).ListNotifications(c.Request.Context(), req.Tab, req.Limit)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "LIST_NOTIFICATIONS_FAILED",
			"获取通知列表失败", err)
		return
	}

	respondSuccess(c, map[string]any{"data": result}, "获取通知列表成功")
}

// replyNotificationHandler 回复通知里的评论
func (s *AppServer) replyNotificationHandler(c *gin.Context) {
	var req ReplyNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", err)
		return
	}

	result, err := s.service(c).ReplyNotification(c.Request.Context(), req.CommentID, req.Content)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "REPLY_NOTIFICATION_FAILED",
			"回复通知失败", err)
		return
	}

	respondSuccess(c, map[string]any{"data": result}, "回复成功")
}

// likeNotificationHandler 给通知里的评论点赞/取消点赞
func (s *AppServer) likeNotificationHandler(c *gin.Context) {
	var req LikeNotificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		respondError(c, http.StatusBadRequest, "INVALID_REQUEST",
			"请求参数错误", err)
		return
	}

	result, err := s.service(c).LikeNotification(c.Request.Context(), req.CommentID, req.Unlike)
	if err != nil {
		respondError(c, http.StatusInternalServerError, "LIKE_NOTIFICATION_FAILED",
			"点赞失败", err)
		return
	}

	respondSuccess(c, map[string]any{"data": result}, "操作成功")
}
