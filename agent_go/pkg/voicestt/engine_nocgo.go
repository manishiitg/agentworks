//go:build !cgo || novoice

package voicestt

import (
	"fmt"
)

// ModelFiles describes the files used by the native recognizer. It remains
// available in builds without speech so callers retain the same package API.
type ModelFiles struct {
	Encoder string
	Decoder string
	Joiner  string
	Tokens  string
}

const SampleRate = 16000

// Available is false here: NewEngine below always fails, so the composer must
// not offer a mic control in this build. See engine.go for the CGO value.
const Available = false

// Engine and Stream deliberately preserve the public API. NewEngine returns
// the diagnostic below before either can be used.
type Engine struct{}
type Stream struct{}

type Result struct {
	Text           string
	EndOfUtterance bool
}

// NewEngine makes the capability failure explicit instead of preventing an
// otherwise rootless Linux deployment from starting. The HTTP route turns this
// into a 503 only for profiles that opt into voice input.
func NewEngine(string) (*Engine, error) {
	return nil, fmt.Errorf("voice input is unavailable in this build")
}

func (*Engine) Close()                          {}
func (*Engine) Punctuate(text string) string    { return text }
func (*Engine) NewStream() *Stream              { return &Stream{} }
func (*Stream) AcceptWaveform([]float32) Result { return Result{} }
func (*Stream) Finish() Result                  { return Result{EndOfUtterance: true} }
func (*Stream) Close()                          {}
