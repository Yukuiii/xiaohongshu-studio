package main

import (
	"embed"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Vite 产物随二进制分发；构建 Go 前必须先构建前端，运行时不依赖 Node。
//
//go:embed web/dist/index.html
var webIndex []byte

//go:embed web/dist/assets
var webAssets embed.FS

func registerWebRoutes(router *gin.Engine) {
	router.GET("/", func(c *gin.Context) {
		setWebHeaders(c)
		c.Data(http.StatusOK, "text/html; charset=utf-8", webIndex)
	})
	router.GET("/web/assets/*asset", func(c *gin.Context) {
		setWebHeaders(c)
		name := strings.TrimPrefix(c.Param("asset"), "/")
		if !fs.ValidPath(name) {
			c.Status(http.StatusNotFound)
			return
		}
		file, err := webAssets.Open("web/dist/assets/" + name)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || info.IsDir() {
			c.Status(http.StatusNotFound)
			return
		}
		contentType := mime.TypeByExtension(path.Ext(name))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		c.Header("Content-Type", contentType)
		// embed.FS 的普通文件实现 io.ReadSeeker，直接流式响应，避免复制资源。
		http.ServeContent(c.Writer, c.Request, info.Name(), time.Time{}, file.(io.ReadSeeker))
	})
}

func setWebHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob: https:; connect-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
}
