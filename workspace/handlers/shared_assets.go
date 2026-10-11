package handlers

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
	"github.com/spf13/viper"
)

// noSymlinks rejects links before opening a scoped asset. os.Root prevents
// escaping the root; this also keeps private symlink targets undiscoverable.
func noSymlinks(root *os.Root, p string) error {
	prefix := ""
	for _, part := range strings.Split(p, "/") {
		if part == "." || part == "" {
			continue
		}
		prefix = path.Join(prefix, part)
		st, err := root.Lstat(prefix)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return fs.ErrPermission
		}
	}
	return nil
}

// SharedAssets is service-token-only. The agent server authorizes the scope
// root. OpenRoot confines subsequent reads even while files are being changed.
func SharedAssets(c *gin.Context) {
	var req struct {
		Root      string          `json:"root"`
		Path      string          `json:"path"`
		Operation string          `json:"operation"`
		Guard     *wf.FolderGuard `json:"guard,omitempty"`
	}
	if json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)).Decode(&req) != nil {
		c.AbortWithStatus(400)
		return
	}
	rootPath, err := wf.CleanRelative(req.Root)
	if err != nil || rootPath == "." {
		c.AbortWithStatus(400)
		return
	}
	p, err := wf.CleanRelative(req.Path)
	if err != nil || wf.Private(p) || req.Guard.Validate() != nil || !req.Guard.Allows(p, false) {
		c.AbortWithStatus(403)
		return
	}
	base, err := os.OpenRoot(viper.GetString("docs-dir"))
	if err != nil {
		c.AbortWithStatus(503)
		return
	}
	defer base.Close()
	if noSymlinks(base, rootPath) != nil {
		c.AbortWithStatus(403)
		return
	}
	root, err := base.OpenRoot(rootPath)
	if err != nil {
		c.AbortWithStatus(404)
		return
	}
	defer root.Close()
	if noSymlinks(root, p) != nil {
		c.AbortWithStatus(403)
		return
	}
	switch req.Operation {
	case "stat", "read":
		f, err := root.Open(p)
		if err != nil {
			c.AbortWithStatus(404)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || (!info.Mode().IsRegular() && !info.IsDir()) {
			c.AbortWithStatus(400)
			return
		}
		if req.Operation == "stat" && info.IsDir() {
			c.JSON(200, gin.H{"path": p, "type": "folder", "modified_at": info.ModTime()})
			return
		}
		if info.IsDir() {
			c.AbortWithStatus(400)
			return
		}
		contentType := mime.TypeByExtension(path.Ext(p))
		if contentType == "" {
			buf := make([]byte, 512)
			n, _ := f.Read(buf)
			contentType = http.DetectContentType(buf[:n])
			f.Seek(0, io.SeekStart)
		}
		if req.Operation == "stat" {
			c.JSON(200, gin.H{"path": p, "type": "file", "size": info.Size(), "content_type": contentType, "modified_at": info.ModTime()})
			return
		}
		c.Header("Content-Type", contentType)
		c.Header("Accept-Ranges", "bytes")
		c.Request.Method = "GET"
		if c.GetHeader("X-Asset-Method") == "HEAD" {
			c.Request.Method = "HEAD"
			c.Request.Header.Del("Range")
		}
		http.ServeContent(c.Writer, c.Request, path.Base(p), info.ModTime(), f)
	case "list", "archive":
		items, err := sharedAssetEntries(c.Request.Context().Done(), root, p, req.Guard)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		if req.Operation == "list" {
			c.JSON(200, gin.H{"success": true, "data": items})
			return
		}
		// Build before sending headers so bounds/IO errors never return a corrupt
		// archive as a successful download. The temporary file is test/run-owned.
		temp, err := os.CreateTemp("", "agentworks-share-*.zip")
		if err != nil {
			c.AbortWithStatus(500)
			return
		}
		defer os.Remove(temp.Name())
		defer temp.Close()
		writer := zip.NewWriter(temp)
		var total int64
		for _, item := range items {
			if item.Type == "folder" {
				continue
			}
			if noSymlinks(root, item.Path) != nil {
				writer.Close()
				c.AbortWithStatus(403)
				return
			}
			file, err := root.Open(item.Path)
			if err != nil {
				writer.Close()
				c.AbortWithStatus(404)
				return
			}
			info, err := file.Stat()
			if err != nil || !info.Mode().IsRegular() {
				file.Close()
				writer.Close()
				c.AbortWithStatus(400)
				return
			}
			relative := strings.TrimPrefix(strings.TrimPrefix(item.Path, p), "/")
			if p == "." {
				relative = item.Path
			}
			entry, err := writer.Create(relative)
			if err == nil {
				var n int64
				n, err = io.Copy(entry, io.LimitReader(file, (512<<20)-total+1))
				total += n
			}
			file.Close()
			if err != nil || total > 512<<20 {
				writer.Close()
				c.JSON(413, gin.H{"error": "archive exceeds 512 MiB or could not be read"})
				return
			}
		}
		if writer.Close() != nil {
			c.AbortWithStatus(500)
			return
		}
		temp.Seek(0, io.SeekStart)
		c.Header("Content-Type", "application/zip")
		http.ServeContent(c.Writer, c.Request, "files.zip", time.Time{}, temp)
	default:
		c.AbortWithStatus(400)
	}
}

type sharedAssetEntry struct {
	Path     string    `json:"filepath"`
	Type     string    `json:"type"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"last_modified"`
}

func sharedAssetEntries(done <-chan struct{}, root *os.Root, p string, guard *wf.FolderGuard) ([]sharedAssetEntry, error) {
	st, err := root.Stat(p)
	if err != nil || !st.IsDir() {
		return nil, errors.New("folder not found")
	}
	result := []sharedAssetEntry{}
	scanned := 0
	err = fs.WalkDir(root.FS(), p, func(name string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		select {
		case <-done:
			return errors.New("request canceled")
		default:
		}
		scanned++
		if scanned > 10000 {
			return errors.New("folder exceeds 10000 entries; choose a smaller folder")
		}
		if d.Type()&os.ModeSymlink != 0 || wf.Private(name) || !guard.Allows(name, false) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if name == p {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		kind := "file"
		if info.IsDir() {
			kind = "folder"
		} else if !info.Mode().IsRegular() {
			return nil
		}
		result = append(result, sharedAssetEntry{name, kind, info.Size(), info.ModTime()})
		return nil
	})
	return result, err
}
