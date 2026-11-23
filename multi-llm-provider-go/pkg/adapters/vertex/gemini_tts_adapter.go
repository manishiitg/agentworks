package vertex

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"

	"google.golang.org/genai"
)

const (
	defaultTTSVoiceName  = "Kore"
	defaultTTSSampleRate = 24000
	defaultTTSChannels   = 1
	defaultTTSBitDepth   = 16
)

// GeminiTTSAdapter implements llmtypes.AudioGenerationModel using Gemini native TTS.
type GeminiTTSAdapter struct {
	client  *genai.Client
	modelID string
	logger  interfaces.Logger
}

// NewGeminiTTSAdapter creates a new Gemini TTS adapter.
// The client must be initialized with genai.BackendGeminiAPI and a valid API key.
func NewGeminiTTSAdapter(client *genai.Client, modelID string, logger interfaces.Logger) *GeminiTTSAdapter {
	return &GeminiTTSAdapter{
		client:  client,
		modelID: modelID,
		logger:  logger,
	}
}

// GenerateAudio implements llmtypes.AudioGenerationModel using GenerateContent.
func (a *GeminiTTSAdapter) GenerateAudio(ctx context.Context, prompt string, options ...llmtypes.AudioGenerationOption) (*llmtypes.AudioGenerationResponse, error) {
	opts := &llmtypes.AudioGenerationOptions{
		VoiceName: defaultTTSVoiceName,
	}
	for _, opt := range options {
		opt(opts)
	}
	if strings.TrimSpace(opts.VoiceName) == "" {
		opts.VoiceName = defaultTTSVoiceName
	}

	config := &genai.GenerateContentConfig{
		ResponseModalities: []string{"AUDIO"},
		SpeechConfig:       buildSpeechConfig(opts),
	}

	a.logger.Infof("Generating audio with Gemini TTS model %s, prompt: %q", a.modelID, prompt)
	resp, err := a.client.Models.GenerateContent(ctx, a.modelID, genai.Text(prompt), config)
	if err != nil {
		return nil, fmt.Errorf("gemini TTS GenerateContent failed: %w", err)
	}
	if resp == nil || len(resp.Candidates) == 0 {
		return nil, fmt.Errorf("gemini TTS returned no candidates")
	}

	result := &llmtypes.AudioGenerationResponse{
		Audio: make([]llmtypes.GeneratedAudio, 0),
	}
	for _, candidate := range resp.Candidates {
		if candidate.Content == nil {
			continue
		}
		for _, part := range candidate.Content.Parts {
			if part.InlineData == nil || len(part.InlineData.Data) == 0 {
				continue
			}
			data := part.InlineData.Data
			mimeType := strings.ToLower(strings.TrimSpace(part.InlineData.MIMEType))
			if isRawPCMMIMEType(mimeType) {
				data = pcm16ToWAV(data, defaultTTSSampleRate, defaultTTSChannels)
				mimeType = "audio/wav"
			}
			result.Audio = append(result.Audio, llmtypes.GeneratedAudio{
				Data:     data,
				MimeType: mimeType,
			})
		}
	}

	if len(result.Audio) == 0 {
		return nil, fmt.Errorf("gemini TTS returned no audio data")
	}

	a.logger.Infof("Generated %d audio item(s) successfully via Gemini TTS", len(result.Audio))
	return result, nil
}

func isRawPCMMIMEType(mimeType string) bool {
	return mimeType == "" ||
		strings.HasPrefix(mimeType, "audio/pcm") ||
		strings.HasPrefix(mimeType, "audio/l16")
}

func buildSpeechConfig(opts *llmtypes.AudioGenerationOptions) *genai.SpeechConfig {
	cfg := &genai.SpeechConfig{
		LanguageCode: strings.TrimSpace(opts.LanguageCode),
	}
	if len(opts.SpeakerVoiceConfigs) > 0 {
		speakers := make([]*genai.SpeakerVoiceConfig, 0, len(opts.SpeakerVoiceConfigs))
		for _, speaker := range opts.SpeakerVoiceConfigs {
			speakerName := strings.TrimSpace(speaker.Speaker)
			voiceName := strings.TrimSpace(speaker.VoiceName)
			if speakerName == "" {
				continue
			}
			if voiceName == "" {
				voiceName = defaultTTSVoiceName
			}
			speakers = append(speakers, &genai.SpeakerVoiceConfig{
				Speaker: speakerName,
				VoiceConfig: &genai.VoiceConfig{
					PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{VoiceName: voiceName},
				},
			})
		}
		if len(speakers) > 0 {
			cfg.MultiSpeakerVoiceConfig = &genai.MultiSpeakerVoiceConfig{
				SpeakerVoiceConfigs: speakers,
			}
			return cfg
		}
	}

	cfg.VoiceConfig = &genai.VoiceConfig{
		PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{VoiceName: strings.TrimSpace(opts.VoiceName)},
	}
	return cfg
}

func pcm16ToWAV(pcm []byte, sampleRate, channels int) []byte {
	var buf bytes.Buffer
	byteRate := sampleRate * channels * defaultTTSBitDepth / 8
	blockAlign := channels * defaultTTSBitDepth / 8
	dataSize := uint32(len(pcm))
	chunkSize := uint32(36 + len(pcm))

	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, chunkSize)
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(&buf, binary.LittleEndian, uint16(defaultTTSBitDepth))
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, dataSize)
	buf.Write(pcm)
	return buf.Bytes()
}
