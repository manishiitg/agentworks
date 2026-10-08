package handlers

import (
	"encoding/base64"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// GetChatAttachment is read-only and holds each directory open. Neither file
// symlinks nor replaced parent directories may escape the chat upload root.
func GetChatAttachment(c *gin.Context) {
	full, err := resolveUserPath(c, c.Param("filepath"))
	if err != nil {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	relative, err := filepath.Rel(viper.GetString("docs-dir"), full)
	if err != nil || relative == "." || strings.HasPrefix(relative, "..") {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) < 6 || parts[len(parts)-4] != "uploads" || parts[len(parts)-3] != "chats" {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	key := parts[len(parts)-2]
	sid, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil || len(sid) == 0 || len(sid) > 128 || base64.RawURLEncoding.EncodeToString(sid) != key {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	root, err := os.OpenRoot(viper.GetString("docs-dir"))
	if err != nil {
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	defer func() { root.Close() }()
	for _, part := range parts[:len(parts)-1] {
		info, err := root.Lstat(part)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		next, err := root.OpenRoot(part)
		if err != nil {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		held, err := next.Stat(".")
		if err != nil || !os.SameFile(info, held) {
			next.Close()
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		root.Close()
		root = next
	}
	// A substituted FIFO must not block the handler before the regular-file check.
	file, err := root.OpenFile(parts[len(parts)-1], os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, 10<<20+1))
	if err != nil || len(data) > 10<<20 {
		c.AbortWithStatus(http.StatusRequestEntityTooLarge)
		return
	}
	ext := strings.ToLower(filepath.Ext(full))
	image := ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".webp" || ext == ".gif" || ext == ".bmp" || ext == ".svg" || ext == ".ico"
	result := gin.H{"is_image": image, "mime_type": mime.TypeByExtension(ext), "size": len(data)}
	if image {
		result["data"] = base64.StdEncoding.EncodeToString(data)
	} else {
		if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
			c.AbortWithStatus(http.StatusUnsupportedMediaType)
			return
		}
		truncated := len(data) > 64<<10
		if truncated {
			data = data[:64<<10]
			for !utf8.Valid(data) {
				data = data[:len(data)-1]
			}
		}
		result["content"], result["truncated"] = string(data), truncated
	}
	c.JSON(http.StatusOK, result)
}
