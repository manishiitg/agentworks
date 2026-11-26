package codexcli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const codexImageMaxBytes int64 = 10 << 20
const codexTurnImageMaxBytes int64 = 80 << 20

type codexPreparedImage struct {
	reader io.ReadCloser
	mime   string
	bytes  int64
}

// Streaming parameters deliberately bypass encoding/json's pooled full-message
// buffer: an 80 MB image turn must not retain a second base64 copy in the host.
type codexStreamingParams interface{ writeJSON(io.Writer) error }
type codexImageParams struct {
	base   map[string]any
	images []codexPreparedImage
}

func writeCodexJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
func (p *codexImageParams) writeJSON(w io.Writer) error {
	if _, err := io.WriteString(w, "{"); err != nil {
		return err
	}
	first := true
	for key, value := range p.base {
		if key == "input" {
			continue
		}
		if !first {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		first = false
		if err := writeCodexJSON(w, key); err != nil {
			return err
		}
		if _, err := io.WriteString(w, ":"); err != nil {
			return err
		}
		if err := writeCodexJSON(w, value); err != nil {
			return err
		}
	}
	if !first {
		if _, err := io.WriteString(w, ","); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, `"input":[`); err != nil {
		return err
	}
	// Text is small and already escaped by the ordinary parameter builder.
	input, err := json.Marshal(p.base["input"])
	if err != nil {
		return err
	}
	hasText := len(input) > 2 && string(input) != "null"
	if hasText {
		if _, err = w.Write(input[1 : len(input)-1]); err != nil {
			return err
		}
	}
	for i, img := range p.images {
		if hasText || i > 0 {
			if _, err = io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if _, err = io.WriteString(w, `{"type":"image","url":"data:`+img.mime+`;base64,`); err != nil {
			return err
		}
		encoder := base64.NewEncoder(base64.StdEncoding, w)
		n, copyErr := io.Copy(encoder, io.LimitReader(img.reader, img.bytes))
		closeErr := encoder.Close()
		if copyErr != nil {
			return copyErr
		}
		if n != img.bytes {
			return io.ErrUnexpectedEOF
		}
		if closeErr != nil {
			return closeErr
		}
		if _, err = io.WriteString(w, `"}`); err != nil {
			return err
		}
	}
	_, err = io.WriteString(w, "]}")
	return err
}
func (p *codexImageParams) close() {
	for i := range p.images {
		_ = p.images[i].reader.Close()
		p.images[i].reader = nil
	}
	p.images = nil
}

// All access checks and image preparation finish before any RPC bytes are
// written. Only prepared file handles remain open, never decoded pixels. A
// preparation refusal cannot corrupt framing or interrupt an unrelated turn.
func codexParamsWithImages(ctx context.Context, owner string, base map[string]any, messages []llmtypes.MessageContent) (any, func(), error) {
	inputs := llmtypes.ClaimNativeImageInputsForSession(ctx, owner)
	if len(inputs) == 0 {
		// Direct callers use the same pending-human boundary as their text.
		// The app uses admitted factories instead, so no image is saved in history.
		for _, message := range llmtypes.PendingHumanMessages(messages) {
			for _, part := range message.Parts {
				var image *llmtypes.ImageContent
				switch v := part.(type) {
				case llmtypes.ImageContent:
					image = &v
				case *llmtypes.ImageContent:
					image = v
				}
				if image == nil {
					continue
				}
				v := *image
				if v.SourceType != "base64" {
					return nil, func() {}, errors.New("Codex native images require base64 image content; URLs are not fetched")
				}
				inputs = append(inputs, llmtypes.NativeImageInput{Open: func(context.Context) (io.ReadCloser, string, int64, error) {
					if int64(len(v.Data)) > int64(base64.StdEncoding.EncodedLen(int(codexImageMaxBytes))+4) {
						return nil, "", 0, errors.New("image_too_large")
					}
					n, err := io.Copy(io.Discard, io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(v.Data)), codexImageMaxBytes+1))
					if err != nil {
						return nil, "", 0, errors.New("invalid_image_base64")
					}
					return io.NopCloser(base64.NewDecoder(base64.StdEncoding, strings.NewReader(v.Data))), v.MediaType, n, nil
				}})
			}
		}
	}
	if len(inputs) == 0 {
		return base, func() {}, nil
	}
	p := &codexImageParams{base: base}
	var total int64
	skip := func(input llmtypes.NativeImageInput, reason string) {
		log.Printf("[CHAT_IMAGE] session=%s provider=codex-cli images=1 bytes=0 native=false action=skipped reason=%s", owner, reason)
		if input.Skipped != nil {
			input.Skipped(reason)
		}
	}
	for _, input := range inputs {
		if err := ctx.Err(); err != nil {
			p.close()
			return nil, func() {}, err
		}
		if len(p.images) >= 16 {
			skip(input, "image_count")
			continue
		}
		if input.Open == nil {
			skip(input, "image_unavailable")
			continue
		}
		reader, mime, n, err := input.Open(ctx)
		if err != nil || reader == nil {
			if reader != nil {
				_ = reader.Close()
			}
			skip(input, "image_unavailable")
			continue
		}
		validMime := mime == "image/png" || mime == "image/jpeg" || mime == "image/webp" || mime == "image/gif"
		if !validMime || n <= 0 || n > codexImageMaxBytes || total+n > codexTurnImageMaxBytes {
			_ = reader.Close()
			skip(input, "image_limit")
			continue
		}
		p.images = append(p.images, codexPreparedImage{reader: reader, mime: mime, bytes: n})
		total += n
	}
	if len(p.images) == 0 {
		return base, func() {}, nil
	}
	log.Printf("[CHAT_IMAGE] session=%s provider=codex-cli images=%d bytes=%d native=true action=started", owner, len(inputs), total)
	return p, func() { p.close() }, nil
}

func finishCodexImageDelivery(owner string, params any, err error) {
	p, ok := params.(*codexImageParams)
	if !ok {
		return
	}
	var n int64
	for _, image := range p.images {
		n += image.bytes
	}
	action, reason := "done", "accepted"
	if err != nil {
		action, reason = "failed", "native_rpc_failed"
	}
	log.Printf("[CHAT_IMAGE] session=%s provider=codex-cli images=%d bytes=%d native=true action=%s reason=%s", owner, len(p.images), n, action, reason)
}
