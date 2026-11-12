package main

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

// 页面随二进制分发，不依赖外部静态目录或 Node 构建。
//
//go:embed web/index.html
var webIndex []byte

//go:embed web/app.js
var webScript []byte

//go:embed web/style.css
var webStyle []byte

func registerWebRoutes(router *gin.Engine) {
	router.GET("/", webAsset("text/html; charset=utf-8", webIndex))
	router.GET("/web/app.js", webAsset("text/javascript; charset=utf-8", webScript))
	router.GET("/web/style.css", webAsset("text/css; charset=utf-8", webStyle))
}

func webAsset(contentType string, data []byte) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob: https:; connect-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		c.Data(http.StatusOK, contentType, data)
	}
}
