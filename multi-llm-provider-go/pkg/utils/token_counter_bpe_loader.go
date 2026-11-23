package utils

import (
	"time"

	"github.com/pkoukk/tiktoken-go"
)

// bpeLoadTimeout bounds tiktoken-go's own vocab download. tiktoken-go's default
// loader (load.go's readFile) is a bare http.Get with no timeout and no
// context, on the FIRST-ever call for an encoding on a machine with no
// populated TIKTOKEN_CACHE_DIR / DATA_GYM_CACHE_DIR -- every fresh container,
// CI runner, or dev machine. If that request stalls, tiktoken.GetEncoding
// never returns.
//
// Measured live 2026-08-19: a bare-agent-loop test against Vertex/Gemini hung
// for the full 5-minute test timeout, entirely inside
// crypto/tls.(*Conn).Read <- tiktoken-go's readFile <- GetEncoding("o200k_base"),
// called from this package's own countTokensWithEncoding on the very first
// turn (agent/tool_output_handler.go -> multi-llm-provider-go TokenCounter).
// That call sits on the synchronous path of every turn for every provider,
// not just OpenAI -- CountTokensForModel resolves Vertex/Gemini models to
// "o200k_base" too (see modelIDToEncoding above).
//
// The graceful degradation this already assumed already exists:
// countTokensWithEncoding falls back to a len(content)/4 approximation
// whenever getCachedEncoding returns an error. That fallback is fine for a
// token count -- it is only ever used for budgeting/truncation decisions, not
// billing -- but it can only run if the failing call actually FAILS instead of
// hanging forever.
const bpeLoadTimeout = 10 * time.Second

// timeoutBpeLoader wraps tiktoken-go's real loader (an unbounded network
// fetch) so it returns an error within bpeLoadTimeout instead of blocking
// forever, letting the caller's existing approximation fallback engage.
//
// The underlying HTTP request itself is NOT cancelled on timeout -- readFile
// takes no context, so there is nothing to cancel -- it is abandoned to
// finish or fail in its own goroutine while this returns. That goroutine leak
// is bounded (one per distinct encoding name actually requested, not one per
// call: tiktoken.GetEncoding's own encoding-name-keyed init guards against a
// concurrent second attempt) and is the trade a package with no cancellable
// HTTP client leaves available; it is a strict improvement over the prior
// behaviour of blocking the caller indefinitely on every such stall.
type timeoutBpeLoader struct {
	inner tiktoken.BpeLoader
}

func (l timeoutBpeLoader) LoadTiktokenBpe(tiktokenBpeFile string) (map[string]int, error) {
	type result struct {
		bpe map[string]int
		err error
	}
	done := make(chan result, 1)
	go func() {
		bpe, err := l.inner.LoadTiktokenBpe(tiktokenBpeFile)
		done <- result{bpe, err}
	}()

	select {
	case r := <-done:
		return r.bpe, r.err
	case <-time.After(bpeLoadTimeout):
		return nil, errBpeLoadTimedOut
	}
}

var errBpeLoadTimedOut = &bpeLoadTimeoutError{}

type bpeLoadTimeoutError struct{}

func (*bpeLoadTimeoutError) Error() string {
	return "tiktoken BPE vocab load timed out (network stall fetching the encoding file); falling back to approximate token counting"
}

func init() {
	tiktoken.SetBpeLoader(timeoutBpeLoader{inner: tiktoken.NewDefaultBpeLoader()})
}
