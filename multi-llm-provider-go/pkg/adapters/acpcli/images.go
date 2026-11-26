package acpcli

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

const imageMaxBytes int64 = 10 << 20
const turnImageMaxBytes int64 = 80 << 20

type preparedImage struct {
	reader io.ReadCloser
	mime   string
	bytes  int64
}

// Streaming parameters deliberately bypass encoding/json's pooled full-message
// buffer: an 80 MB image turn must not retain a second base64 copy in the host.
type streamingParams interface{ writeJSON(io.Writer) error }
type imageParams struct {
	base     map[string]any
	provider string
	owner    string
	writeCtx context.Context
	wrote    chan error
	images   []preparedImage
}

func writeJSONValue(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
func (p *imageParams) writeJSON(w io.Writer) error {
	if _, err := io.WriteString(w, "{"); err != nil {
		return err
	}
	first := true
	for key, value := range p.base {
		if key == "prompt" {
			continue
		}
		if !first {
			if _, err := io.WriteString(w, ","); err != nil {
				return err
			}
		}
		first = false
		if err := writeJSONValue(w, key); err != nil {
			return err
		}
		if _, err := io.WriteString(w, ":"); err != nil {
			return err
		}
		if err := writeJSONValue(w, value); err != nil {
			return err
		}
	}
	if !first {
		if _, err := io.WriteString(w, ","); err != nil {
			return err
		}
	}
	if _, err := io.WriteString(w, `"prompt":[`); err != nil {
		return err
	}
	// Text is small and already escaped by the ordinary parameter builder.
	input, err := json.Marshal(p.base["prompt"])
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
		if _, err = io.WriteString(w, `{"type":"image","mimeType":"`+img.mime+`","data":"`); err != nil {
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
func (p *imageParams) close() {
	for i := range p.images {
		_ = p.images[i].reader.Close()
		p.images[i].reader = nil
	}
	p.images = nil
}

// All access checks and image preparation finish before any RPC bytes are
// written. Only prepared file handles remain open, never decoded pixels. A
// preparation refusal cannot corrupt framing or interrupt an unrelated turn.
func paramsWithImages(ctx context.Context, provider, owner string, base map[string]any, messages []llmtypes.MessageContent, allowed bool) (any, func(), error) {
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
				inputs = append(inputs, llmtypes.NativeImageInput{Open: func(context.Context) (io.ReadCloser, string, int64, error) {
					if v.SourceType != "base64" {
						return nil, "", 0, errors.New("image_source_unavailable") // Never fetch URLs.
					}
					if int64(len(v.Data)) > int64(base64.StdEncoding.EncodedLen(int(imageMaxBytes))+4) {
						return nil, "", 0, errors.New("image_too_large")
					}
					n, err := io.Copy(io.Discard, io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(v.Data)), imageMaxBytes+1))
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
	p := &imageParams{base: base, provider: provider, owner: owner, writeCtx: ctx, wrote: make(chan error, 1)}
	var total int64
	skip := func(input llmtypes.NativeImageInput, reason string) {
		log.Printf("[CHAT_IMAGE] session=%s provider=%s images=1 bytes=0 native=false action=skipped reason=%s", owner, provider, reason)
		if input.Skipped != nil {
			input.Skipped(reason)
		}
	}
	if !allowed {
		for _, input := range inputs {
			skip(input, "unsupported_image_capability")
		}
		return base, func() {}, nil
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
		validMime := mime == "image/png" || mime == "image/jpeg" || mime == "image/webp" || mime == "image/bmp"
		if !validMime || n <= 0 || n > imageMaxBytes || total+n > turnImageMaxBytes {
			_ = reader.Close()
			skip(input, "image_limit")
			continue
		}
		p.images = append(p.images, preparedImage{reader: reader, mime: mime, bytes: n})
		total += n
	}
	if len(p.images) == 0 {
		return base, func() {}, nil
	}
	log.Printf("[CHAT_IMAGE] session=%s provider=%s images=%d bytes=%d native=true action=started", owner, provider, len(p.images), total)
	return p, func() { p.close() }, nil
}

func finishImageDelivery(params any, err error) {
	p, ok := params.(*imageParams)
	if !ok {
		return
	}
	var n int64
	for _, image := range p.images {
		n += image.bytes
	}
	action, reason := "done", "bridge_written"
	if err != nil {
		action, reason = "failed", "native_rpc_failed"
		if errors.Is(err, llmtypes.ErrCodingAgentInputNotSent) {
			reason = "input_not_sent"
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			reason = "canceled"
		}
	}
	log.Printf("[CHAT_IMAGE] session=%s provider=%s images=%d bytes=%d native=true action=%s reason=%s", p.owner, p.provider, len(p.images), n, action, reason)
}

// The outer RPC metadata is small; image bytes are streamed inside params.
type imageRequest struct {
	header map[string]any
	params *imageParams
}

func (r *imageRequest) writeJSON(w io.Writer) error {
	b, err := json.Marshal(r.header)
	if err != nil {
		return err
	}
	if _, err = w.Write(b[:len(b)-1]); err != nil {
		return err
	}
	if _, err = io.WriteString(w, `,"params":`); err != nil {
		return err
	}
	if err = r.params.writeJSON(w); err != nil {
		return err
	}
	_, err = io.WriteString(w, "}")
	return err
}

type turnImages struct {
	owner    string
	messages []llmtypes.MessageContent
}

func hasPendingImages(ctx context.Context, owner string, messages []llmtypes.MessageContent) bool {
	if len(llmtypes.NativeImageInputsForSession(ctx, owner)) > 0 {
		return true
	}
	for _, m := range llmtypes.PendingHumanMessages(messages) {
		for _, p := range m.Parts {
			switch p.(type) {
			case llmtypes.ImageContent, *llmtypes.ImageContent:
				return true
			}
		}
	}
	return false
}
