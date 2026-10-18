package server

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"unicode/utf8"
)

func (api *StreamingAPI) externalPublicationHistory(w http.ResponseWriter, r *http.Request, args map[string]any) {
	file, name, ok := api.openPublicationHistory(w, r, externalArg(args, "workspace_id"), externalArg(args, "agent_id"), externalArg(args, "kind"))
	if !ok {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		externalFailure(w, err)
		return
	}
	offset, limit := externalInt(args, "offset", 0), externalInt(args, "limit", 16384)
	if offset < 0 || int64(offset) > info.Size() || limit < 0 || limit > 65536 {
		externalError(w, 400, "invalid_arguments", "Use the returned next_offset and a limit from 0 to 65536 bytes; 0 reads only the saved-chat details.")
		return
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		externalFailure(w, err)
		return
	}
	if _, err = file.Seek(int64(offset), io.SeekStart); err != nil {
		externalFailure(w, err)
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)))
	if err != nil {
		externalFailure(w, err)
		return
	}
	encoding, content := "utf-8", string(data)
	if !utf8.Valid(data) {
		encoding, content = "base64", base64.StdEncoding.EncodeToString(data)
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]any{"workspace_id": externalArg(args, "workspace_id"), "agent_id": externalArg(args, "agent_id"), "filename": name, "total_bytes": info.Size(), "sha256": hex.EncodeToString(hash.Sum(nil)), "offset": offset, "next_offset": offset + len(data), "has_more": int64(offset+len(data)) < info.Size(), "encoding": encoding, "content": content})
}
