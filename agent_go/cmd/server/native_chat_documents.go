package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"path/filepath"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #3189: a PDF attached to a Claude chat is also given to Claude as a document, as an image is. Other programs (and
// other document types) keep only the saved file path in the message, which they read themselves.
func nativeChatDocumentPath(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".pdf"
}

func nativeChatDocumentsSupported(provider string) bool {
	return provider == "claude-code"
}

// The PDF is sent as saved: no preparation, but the same admitted-file access check as an image, a real PDF header,
// and no size change since it was attached.
func (api *StreamingAPI) nativeChatDocumentInput(ctx context.Context, session, provider string, ref admittedChatAttachment) llmtypes.NativeImageInput {
	return llmtypes.NativeImageInput{ID: ref.Hash, Skipped: func(reason string) {
		api.recordChatImageNotice(session, "A PDF could not be sent directly. Its file path is still available.")
	}, Open: func(deliveryCtx context.Context) (io.ReadCloser, string, int64, error) {
		if err := deliveryCtx.Err(); err != nil {
			return nil, "", 0, err
		}
		file, err := api.openAdmittedChatImage(context.WithoutCancel(ctx), ref)
		if err != nil {
			log.Printf("[CHAT_DOCUMENT] session=%s provider=%s document=%s bytes=%d native=false action=failed reason=access_refused", session, provider, ref.Hash, ref.Bytes)
			return nil, "", 0, err
		}
		stat, err := file.Stat()
		header := make([]byte, 5)
		if err == nil {
			_, err = io.ReadFull(file, header)
		}
		if err == nil && !bytes.Equal(header, []byte("%PDF-")) {
			err = errors.New("document_not_pdf")
		}
		if err == nil && (stat.Size() <= 0 || stat.Size() != ref.Bytes) {
			err = errors.New("document_changed_reattach")
		}
		if err == nil {
			_, err = file.Seek(0, io.SeekStart)
		}
		if err != nil {
			file.Close()
			log.Printf("[CHAT_DOCUMENT] session=%s provider=%s document=%s bytes=%d native=false action=failed reason=%v", session, provider, ref.Hash, ref.Bytes, err)
			return nil, "", 0, err
		}
		log.Printf("[CHAT_DOCUMENT] session=%s provider=%s document=%s bytes=%d native=true action=done reason=opened", session, provider, ref.Hash, stat.Size())
		return file, "application/pdf", stat.Size(), nil
	}}
}
