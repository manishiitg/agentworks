package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// The Code admin audit log (docs/design/code_product.md, "Admin inspection")
// is append-only: entries are only ever added with O_APPEND, never
// rewritten, and browsers cannot write config/ files through the proxy.

var codeAdminAuditMonth = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}$`)

var codeAdminAuditMu sync.Mutex

type appendCodeAdminAuditRequest struct {
	Month string `json:"month"`
	Entry string `json:"entry"`
}

// AppendCodeAdminAudit is POST /api/audit/code-admin/append. It appends one
// JSON object as a line to config/code-admin-audit/<month>.jsonl.
func AppendCodeAdminAudit(c *gin.Context) {
	var req appendCodeAdminAuditRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	entry := strings.TrimSpace(req.Entry)
	var object map[string]any
	if !codeAdminAuditMonth.MatchString(req.Month) || strings.ContainsAny(entry, "\r\n") || json.Unmarshal([]byte(entry), &object) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "month must be YYYY-MM and entry one JSON object on one line"})
		return
	}
	if err := appendCodeAdminAuditLine(viper.GetString("docs-dir"), req.Month, entry); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func appendCodeAdminAuditLine(docsDir, month, entry string) error {
	codeAdminAuditMu.Lock()
	defer codeAdminAuditMu.Unlock()
	dir := filepath.Join(docsDir, "config", "code-admin-audit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, p := range []string{filepath.Join(docsDir, "config"), dir} {
		if info, err := os.Lstat(p); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("audit folder is not a real folder")
		}
	}
	file := filepath.Join(dir, month+".jsonl")
	if info, err := os.Lstat(file); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return fmt.Errorf("audit log is not a regular file")
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(entry + "\n"); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
