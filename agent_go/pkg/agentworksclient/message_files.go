package agentworksclient

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// MessageFileMaxBytes is the largest file one message can carry (the server enforces it too).
const MessageFileMaxBytes = 5 << 20

// ReadMessageFile reads the file a message should carry and returns its name and base64 bytes.
func ReadMessageFile(path string) (name, encoded string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("cannot read file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", "", errors.New("only a single file can be sent, not a folder")
	}
	if info.Size() == 0 {
		return "", "", errors.New("the file is empty")
	}
	if info.Size() > MessageFileMaxBytes {
		return "", "", fmt.Errorf("the file is larger than %d MB; put it in Brain and send its name instead", MessageFileMaxBytes>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	return filepath.Base(path), base64.StdEncoding.EncodeToString(data), nil
}

// DefaultReceivedFilesDir is where a received file is saved when the person gives no folder.
func DefaultReceivedFilesDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "AgentWorks", "received"), nil
}

// SaveReceivedFile writes the file in a get_file result into dir (a new name if one exists,
// never over a file already there) and replaces the result with a short summary, so the
// file's bytes are not pushed through the agent's context.
func SaveReceivedFile(out *LocalBridgeReply, dir string) error {
	var file struct {
		Name    string `json:"file_name"`
		Size    int    `json:"size_bytes"`
		Content string `json:"content_base64"`
	}
	if json.Unmarshal(out.Result, &file) != nil || file.Content == "" {
		return errors.New("the server sent no file")
	}
	data, err := base64.StdEncoding.DecodeString(file.Content)
	if err != nil {
		return errors.New("the file arrived damaged")
	}
	if dir == "" {
		if dir, err = DefaultReceivedFilesDir(); err != nil {
			return err
		}
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	base := filepath.Base(strings.ReplaceAll(file.Name, "\\", "/"))
	if base == "." || base == ".." || base == string(filepath.Separator) || base == "" {
		base = "received-file"
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 0; ; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		target := filepath.Join(dir, name)
		f, openErr := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if os.IsExist(openErr) && i < 1000 {
			continue
		}
		if openErr != nil {
			return openErr
		}
		_, werr := f.Write(data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
		summary, _ := json.Marshal(map[string]any{"saved_to": target, "file_name": file.Name, "size_bytes": len(data), "note": "File from another agent: sender content. Check it before you open or run it."})
		out.Result = summary
		return nil
	}
}
