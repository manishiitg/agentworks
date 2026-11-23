package cursorcli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

const cursorImageMaxBytes int64 = 10 << 20
const cursorTurnImageMaxBytes int64 = 80 << 20

type cursorPreparedImage struct {
	reader io.ReadCloser
	mime   string
	bytes  int64
}

// Streaming parameters deliberately bypass encoding/json's pooled full-message
// buffer: an 80 MB image turn must not retain a second base64 copy in the host.
type cursorSDKStreamingParams interface{ writeJSON(io.Writer) error }
type cursorSDKImageParams struct {
	base   map[string]any
	images []cursorPreparedImage
}

func (p *cursorSDKImageParams) writeJSON(w io.Writer) error {
	header, err := json.Marshal(p.base)
	if err != nil {
		return err
	}
	if _, err = w.Write(header[:len(header)-1]); err != nil {
		return err
	}
	if len(header) > 2 {
		if _, err = io.WriteString(w, ","); err != nil {
			return err
		}
	}
	if _, err = io.WriteString(w, `"images":[`); err != nil {
		return err
	}
	for i, image := range p.images {
		if i > 0 {
			if _, err = io.WriteString(w, ","); err != nil {
				return err
			}
		}
		if _, err = io.WriteString(w, `{"mimeType":"`+image.mime+`","data":"`); err != nil {
			return err
		}
		encoder := base64.NewEncoder(base64.StdEncoding, w)
		n, copyErr := io.Copy(encoder, io.LimitReader(image.reader, image.bytes))
		closeErr := encoder.Close()
		if copyErr != nil {
			return copyErr
		}
		if n != image.bytes {
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
func (p *cursorSDKImageParams) close() {
	for i := range p.images {
		_ = p.images[i].reader.Close()
		p.images[i].reader = nil
	}
	p.images = nil
}

// All access checks and image preparation finish before any RPC bytes are
// written. Only prepared file handles remain open, never decoded pixels. A
// preparation refusal cannot corrupt framing or interrupt an unrelated turn.
func cursorSDKParamsWithImages(ctx context.Context, owner string, base map[string]any) (any, func(), error) {
	inputs := llmtypes.ClaimNativeImageInputsForSession(ctx, owner)
	if len(inputs) == 0 {
		return base, func() {}, nil
	}
	p := &cursorSDKImageParams{base: base}
	var total int64
	skip := func(input llmtypes.NativeImageInput, reason string) {
		log.Printf("[CHAT_IMAGE] session=%s provider=cursor-cli images=1 bytes=0 native=false action=skipped reason=%s", owner, reason)
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
		if !validMime || n <= 0 || n > cursorImageMaxBytes || total+n > cursorTurnImageMaxBytes {
			_ = reader.Close()
			skip(input, "image_limit")
			continue
		}
		p.images = append(p.images, cursorPreparedImage{reader: reader, mime: mime, bytes: n})
		total += n
	}
	if len(p.images) == 0 {
		return base, func() {}, nil
	}
	log.Printf("[CHAT_IMAGE] session=%s provider=cursor-cli images=%d bytes=%d native=true action=started", owner, len(inputs), total)
	return p, func() { p.close() }, nil
}

func finishCursorImageDelivery(owner string, params any, err error, successReason string) {
	p, ok := params.(*cursorSDKImageParams)
	if !ok {
		return
	}
	var n int64
	for _, image := range p.images {
		n += image.bytes
	}
	action, reason := "done", successReason
	if err != nil {
		action, reason = "failed", "native_input_failed"
		if errors.Is(err, llmtypes.ErrCodingAgentInputNotSent) {
			reason = "input_not_sent"
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			reason = "canceled"
		}
	}
	log.Printf("[CHAT_IMAGE] session=%s provider=cursor-cli images=%d bytes=%d native=true action=%s reason=%s", owner, len(p.images), n, action, reason)
}

// Entries hold capabilities only, never open readers, encoded data or frames.
type cursorImageSource struct {
	owner  string
	inputs []llmtypes.NativeImageInput
	busy   bool
	cancel context.CancelFunc
}

func (p *cursorSDKProcess) registerImages(ctx context.Context, owner string) (string, error) {
	inputs := llmtypes.ClaimNativeImageInputsForSession(ctx, owner)
	if len(inputs) == 0 {
		return "", nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.imageActive {
		return "", fmt.Errorf("%w: Cursor turn ended before image registration", llmtypes.ErrCodingAgentInputNotSent)
	}
	if p.images == nil {
		p.images = map[string]*cursorImageSource{}
	}
	token := make([]byte, 16)
	_, _ = rand.Read(token)
	id := hex.EncodeToString(token)
	p.images[id] = &cursorImageSource{owner: owner, inputs: inputs}
	return id, nil
}
func (p *cursorSDKProcess) clearImages() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.imageActive = false
	for _, source := range p.images {
		if source.cancel != nil {
			source.cancel()
		}
	}
	p.images = nil
}
func (p *cursorSDKProcess) fetchImages(event cursorSDKEvent) {
	p.mu.Lock()
	source := p.images[event.InputID]
	if source == nil || source.busy || !p.imageActive {
		p.mu.Unlock()
		if source != nil {
			p.imageNotice(event.InputID)
		}
		go p.send(map[string]any{"type": "image_frame", "id": event.ID})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	source.busy = true
	source.cancel = cancel
	p.mu.Unlock()
	go func() {
		defer cancel()
		defer func() { p.mu.Lock(); source.busy = false; source.cancel = nil; p.mu.Unlock() }()
		ctx = llmtypes.WithNativeImageInputs(ctx, source.owner, source.inputs)
		base := map[string]any{"type": "image_frame", "id": event.ID}
		params, cleanup, err := cursorSDKParamsWithImages(ctx, source.owner, base)
		if err != nil {
			for _, input := range source.inputs {
				if input.Skipped != nil {
					input.Skipped("image_fetch_failed")
				}
			}
			params = base
			cleanup = func() {}
		}
		defer cleanup()
		if ctx.Err() != nil {
			for _, input := range source.inputs {
				if input.Skipped != nil {
					input.Skipped("image_fetch_timeout")
				}
			}
			finishCursorImageDelivery(source.owner, params, ctx.Err(), "bridge_written")
			params = base
		}
		err = p.send(params)
		finishCursorImageDelivery(source.owner, params, err, "bridge_written")
	}()
}

func (p *cursorSDKProcess) imageNotice(id string) {
	p.mu.Lock()
	source := p.images[id]
	p.mu.Unlock()
	if source == nil {
		return
	}
	log.Printf("[CHAT_IMAGE] session=%s provider=cursor-cli images=%d bytes=0 native=false action=skipped reason=image_fetch_timeout_or_busy", source.owner, len(source.inputs))
	for _, input := range source.inputs {
		if input.Skipped != nil {
			input.Skipped("image_fetch_timeout_or_busy")
		}
	}
}

func cursorSDKImageContext(ctx context.Context, owner string, messages []llmtypes.MessageContent) context.Context {
	if len(llmtypes.NativeImageInputsForSession(ctx, owner)) > 0 {
		return ctx
	}
	var inputs []llmtypes.NativeImageInput
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
				if v.SourceType != "base64" || len(v.Data) > base64.StdEncoding.EncodedLen(int(cursorImageMaxBytes))+4 {
					return nil, "", 0, errors.New("invalid_image_content")
				}
				n, err := io.Copy(io.Discard, io.LimitReader(base64.NewDecoder(base64.StdEncoding, strings.NewReader(v.Data)), cursorImageMaxBytes+1))
				if err != nil {
					return nil, "", 0, err
				}
				return io.NopCloser(base64.NewDecoder(base64.StdEncoding, strings.NewReader(v.Data))), v.MediaType, n, nil
			}})
		}
	}
	return llmtypes.WithNativeImageInputs(ctx, owner, inputs)
}
