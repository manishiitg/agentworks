package musecli

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

const museImageMaxBytes int64 = 10 << 20
const museTurnImageMaxBytes int64 = 80 << 20

type musePreparedImage struct {
	reader io.ReadCloser
	mime   string
	bytes  int64
}

// Streaming parameters deliberately bypass encoding/json's pooled full-message
// buffer: an 80 MB image turn must not retain a second base64 copy in the host.
type museStreamingParams interface{ writeJSON(io.Writer) error }
type museImageParams struct {
	base    map[string]any
	images  []musePreparedImage
	sources []llmtypes.NativeImageInput
}

func writeMuseJSON(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
func (p *museImageParams) writeJSON(w io.Writer) error {
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
		if err := writeMuseJSON(w, key); err != nil {
			return err
		}
		if _, err := io.WriteString(w, ":"); err != nil {
			return err
		}
		if err := writeMuseJSON(w, value); err != nil {
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
		if _, err = io.WriteString(w, `{"type":"image","mediaType":"`+img.mime+`","base64Data":"`); err != nil {
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
func (p *museImageParams) close() {
	for i := range p.images {
		_ = p.images[i].reader.Close()
		p.images[i].reader = nil
	}
	p.images = nil
	p.sources = nil
}

// All access checks and image preparation finish before any RPC bytes are
// written. Only prepared file handles remain open, never decoded pixels. A
// preparation refusal cannot corrupt framing or interrupt an unrelated turn.
func museParamsWithImages(ctx context.Context, owner string, base map[string]any, messages []llmtypes.MessageContent) (any, func(), error) {
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
					return nil, func() {}, errors.New("Muse native images require base64 image content; URLs are not fetched")
				}
				inputs = append(inputs, llmtypes.NativeImageInput{Open: func(context.Context) (io.ReadCloser, string, int64, error) {
					if int64(len(v.Data)) > int64(base64.StdEncoding.EncodedLen(int(museImageMaxBytes))+4) {
						return nil, "", 0, errors.New("image_too_large")
					}
					n, err := io.Copy(io.Discard, io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(v.Data)), museImageMaxBytes+1))
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
	p := &museImageParams{base: base}
	var total int64
	skip := func(input llmtypes.NativeImageInput, reason string) {
		log.Printf("[CHAT_IMAGE] session=%s provider=muse-cli images=1 bytes=0 native=false action=skipped reason=%s", owner, reason)
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
		if !validMime || n <= 0 || n > museImageMaxBytes || total+n > museTurnImageMaxBytes {
			_ = reader.Close()
			skip(input, "image_limit")
			continue
		}
		p.images = append(p.images, musePreparedImage{reader: reader, mime: mime, bytes: n})
		p.sources = append(p.sources, input)
		total += n
	}
	if len(p.images) == 0 {
		return base, func() {}, nil
	}
	log.Printf("[CHAT_IMAGE] session=%s provider=muse-cli images=%d bytes=%d native=true action=started", owner, len(inputs), total)
	return p, func() { p.close() }, nil
}

func finishMuseImageDelivery(owner string, params any, err error) {
	p, ok := params.(*museImageParams)
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
		if errors.Is(err, llmtypes.ErrCodingAgentInputNotSent) {
			reason = "input_not_sent"
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			reason = "canceled"
		}
	}
	log.Printf("[CHAT_IMAGE] session=%s provider=muse-cli images=%d bytes=%d native=true action=%s reason=%s", owner, len(p.images), n, action, reason)
}

func museMSPHasPendingImages(ctx context.Context, owner string, messages []llmtypes.MessageContent) bool {
	if len(llmtypes.NativeImageInputsForSession(ctx, owner)) > 0 {
		return true
	}
	for _, message := range llmtypes.PendingHumanMessages(messages) {
		for _, part := range message.Parts {
			switch part.(type) {
			case llmtypes.ImageContent, *llmtypes.ImageContent:
				return true
			}
		}
	}
	return false
}

// MSP text and images share the pending-human boundary. Text-only tmux/exec
// keep their existing parser/refusal. Only fresh native chats get old history.
func museMSPPrompt(ctx context.Context, owner string, messages []llmtypes.MessageContent, resume bool) ([]string, string, error) {
	var systems, pending []string
	text := func(message llmtypes.MessageContent) (string, error) {
		var parts []string
		for _, part := range message.Parts {
			switch v := part.(type) {
			case llmtypes.TextContent:
				parts = append(parts, v.Text)
			case *llmtypes.TextContent:
				if v != nil {
					parts = append(parts, v.Text)
				}
			case llmtypes.ImageContent, *llmtypes.ImageContent:
			default:
				return "", errors.New("Muse MSP input contains an unsupported content part")
			}
		}
		return strings.Join(parts, ""), nil
	}
	for _, m := range messages {
		if m.Role == llmtypes.ChatMessageTypeSystem {
			s, err := text(m)
			if err != nil {
				return nil, "", err
			}
			if strings.TrimSpace(s) != "" {
				systems = append(systems, s)
			}
		}
	}
	for _, m := range llmtypes.PendingHumanMessages(messages) {
		s, err := text(m)
		if err != nil {
			return nil, "", err
		}
		if s != "" {
			pending = append(pending, s)
		}
	}
	human := strings.Join(pending, "\n\n")
	if strings.TrimSpace(human) == "" && !museMSPHasPendingImages(ctx, owner, messages) {
		return nil, "", errors.New("Muse MSP needs text or an image")
	}
	if !resume {
		lastAssistant := -1
		for i, m := range messages {
			if m.Role == llmtypes.ChatMessageTypeAI {
				lastAssistant = i
			}
		}
		history := append([]llmtypes.MessageContent(nil), messages[:lastAssistant+1]...)
		history = append(history, llmtypes.TextPart(llmtypes.ChatMessageTypeHuman, human))
		human = museFreshHistoryPrompt(history, human)
	}
	return systems, human, nil
}

// Only this outer envelope is marshaled: image bytes stream inside params.
type museImageRequest struct {
	header map[string]any
	params *museImageParams
}

func (r *museImageRequest) writeJSON(w io.Writer) error {
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
