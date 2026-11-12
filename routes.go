package main

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// setupRoutes 设置路由配置
func setupRoutes(appServer *AppServer) *gin.Engine {
	// 设置 Gin 模式
	gin.SetMode(gin.ReleaseMode)

	router := gin.New()
	router.Use(gin.Logger())
	router.Use(gin.Recovery())

	// 添加中间件
	router.Use(errorHandlingMiddleware())
	router.Use(corsMiddleware())

	// 健康检查
	router.GET("/health", healthHandler)

	registerWebRoutes(router)
	router.Any("/desktop/*path", gin.WrapH(appServer.desktop))

	// 业务 API 预检公开；未注册的协议路径仍返回 404。
	router.OPTIONS("/api/v1/*path", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	protected := router.Group("")
	protected.Use(authMiddleware(appServer.authToken))
	protected.Use(crossOriginMiddleware())

	// API 路由组
	api := protected.Group("/api/v1")
	{
		api.GET("/login/status", appServer.checkLoginStatusHandler)
		api.POST("/login/browser", appServer.openLoginBrowserHandler)
		api.DELETE("/login/browser", appServer.closeLoginBrowserHandler)
		api.DELETE("/login/cookies", appServer.deleteCookiesHandler)
		api.POST("/publish", appServer.publishHandler)
		api.POST("/publish_video", appServer.publishVideoHandler)
		api.GET("/feeds/list", appServer.listFeedsHandler)
		api.GET("/feeds/search", appServer.searchFeedsHandler)
		api.POST("/feeds/search", appServer.searchFeedsHandler)
		api.POST("/feeds/detail", appServer.getFeedDetailHandler)
		api.POST("/user/profile", appServer.userProfileHandler)
		api.POST("/feeds/comment", appServer.postCommentHandler)
		api.POST("/feeds/comment/reply", appServer.replyCommentHandler)
		api.POST("/feeds/like", appServer.likeFeedHandler)
		api.POST("/feeds/favorite", appServer.favoriteFeedHandler)
		api.GET("/user/me", appServer.myProfileHandler)
		api.GET("/notifications/unread", appServer.getUnreadCountHandler)
		api.GET("/notifications/list", appServer.listNotificationsHandler)
		api.POST("/notifications/list", appServer.listNotificationsHandler)
		api.POST("/notifications/reply", appServer.replyNotificationHandler)
		api.POST("/notifications/like", appServer.likeNotificationHandler)
		api.POST("/ai/models", appServer.aiModelsHandler)
		api.POST("/ai/compose", appServer.aiComposeHandler)
		api.POST("/ai/image", appServer.aiImageHandler)
		api.POST("/images/upload", appServer.uploadImagesHandler)
		api.GET("/images/:id", appServer.getImageHandler)
	}

	return router
}
