package main

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/xpzouying/xiaohongshu-mcp/configs"
)

const maxImageBytes = 10 << 20

var workbenchImageID = regexp.MustCompile(`^[a-f0-9]{32}\.(jpg|png|webp|gif)$`)

type workbenchImage struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	URL  string `json:"url"`
}

func imageStorageDir() (string, error) {
	return filepath.Abs(filepath.Join(configs.GetImagesPath(), "web"))
}

func imageExtension(data []byte) (string, error) {
	if len(data) == 0 || len(data) > maxImageBytes {
		return "", errors.New("每张图片必须非空且不超过 10MiB")
	}
	var ext string
	switch http.DetectContentType(data) {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		// WebP 使用 RIFF 容器，核对长度及首个图像块，拒绝只有魔数的伪造文件。
		if len(data) < 30 || uint64(binary.LittleEndian.Uint32(data[4:8]))+8 != uint64(len(data)) {
			return "", errors.New("WebP 图片内容无效")
		}
		chunk := string(data[12:16])
		if chunk != "VP8 " && chunk != "VP8L" && chunk != "VP8X" {
			return "", errors.New("WebP 图片内容无效")
		}
		if uint64(binary.LittleEndian.Uint32(data[16:20]))+20 > uint64(len(data)) {
			return "", errors.New("WebP 图片内容无效")
		}
		return ".webp", nil
	default:
		return "", errors.New("只支持 JPEG、PNG、WebP 或 GIF 图片，请检查实际文件内容")
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 1 || cfg.Height < 1 {
		return "", errors.New("图片内容损坏或不是有效图片")
	}
	return ext, nil
}

func saveWorkbenchImage(data []byte, name string) (workbenchImage, error) {
	ext, err := imageExtension(data)
	if err != nil {
		return workbenchImage{}, err
	}
	dir, err := imageStorageDir()
	if err != nil {
		return workbenchImage{}, errors.New("无法确定图片保存位置")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return workbenchImage{}, errors.New("无法创建图片目录")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return workbenchImage{}, errors.New("无法创建图片标识")
	}
	id := hex.EncodeToString(random[:]) + ext
	path := filepath.Join(dir, id)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return workbenchImage{}, errors.New("无法保存图片")
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(path)
		return workbenchImage{}, errors.New("保存图片失败")
	}
	return workbenchImage{ID: id, Name: filepath.Base(strings.ReplaceAll(name, "\\", "/")), Path: path, URL: "/api/v1/images/" + id}, nil
}

func (s *AppServer) uploadImagesHandler(c *gin.Context) {
	// 额外空间只供 multipart 边界与表单头使用，每张图片另行限制。
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 9*maxImageBytes+(1<<20))
	if err := c.Request.ParseMultipartForm(1 << 20); err != nil {
		if c.Request.MultipartForm != nil {
			c.Request.MultipartForm.RemoveAll()
		}
		respondError(c, http.StatusBadRequest, "INVALID_UPLOAD", "上传格式错误或总大小超出限制", nil)
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	files := c.Request.MultipartForm.File["files"]
	if len(files) < 1 || len(files) > 9 || len(c.Request.MultipartForm.File) != 1 {
		respondError(c, http.StatusBadRequest, "INVALID_UPLOAD", "请选择 1 至 9 张图片", nil)
		return
	}
	images := make([]workbenchImage, 0, len(files))
	committed := false
	defer func() {
		if !committed {
			for _, image := range images {
				os.Remove(image.Path)
			}
		}
	}()
	for _, header := range files {
		if header.Size > maxImageBytes {
			respondError(c, http.StatusBadRequest, "INVALID_IMAGE", "每张图片不能超过 10MiB", nil)
			return
		}
		file, err := header.Open()
		if err != nil {
			respondError(c, http.StatusBadRequest, "INVALID_IMAGE", "无法读取上传图片", nil)
			return
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxImageBytes+1))
		file.Close()
		if readErr != nil {
			respondError(c, http.StatusBadRequest, "INVALID_IMAGE", "无法读取上传图片", nil)
			return
		}
		image, err := saveWorkbenchImage(data, header.Filename)
		if err != nil {
			respondError(c, http.StatusBadRequest, "INVALID_IMAGE", err.Error(), nil)
			return
		}
		images = append(images, image)
	}
	committed = true
	respondSuccess(c, gin.H{"images": images}, "图片已上传")
}

func (s *AppServer) getImageHandler(c *gin.Context) {
	id := c.Param("id")
	if !workbenchImageID.MatchString(id) {
		respondError(c, http.StatusBadRequest, "INVALID_IMAGE_ID", "图片标识无效", nil)
		return
	}
	dir, err := imageStorageDir()
	if err != nil {
		respondError(c, http.StatusInternalServerError, "IMAGE_READ_FAILED", "无法读取图片", nil)
		return
	}
	// OpenRoot 同时阻止符号链接逃逸到图片目录以外。
	root, err := os.OpenRoot(dir)
	if err != nil {
		respondError(c, http.StatusNotFound, "IMAGE_NOT_FOUND", "图片不存在，请重新上传", nil)
		return
	}
	defer root.Close()
	file, err := root.Open(id)
	if err != nil {
		respondError(c, http.StatusNotFound, "IMAGE_NOT_FOUND", "图片不存在，请重新上传", nil)
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > maxImageBytes {
		respondError(c, http.StatusNotFound, "IMAGE_NOT_FOUND", "图片不可用，请重新上传", nil)
		return
	}
	types := map[string]string{".jpg": "image/jpeg", ".png": "image/png", ".gif": "image/gif", ".webp": "image/webp"}
	c.Header("Content-Type", types[filepath.Ext(id)])
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "private, no-store")
	http.ServeContent(c.Writer, c.Request, id, stat.ModTime(), file)
}
