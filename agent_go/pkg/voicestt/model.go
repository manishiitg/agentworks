package voicestt

import (
	"archive/tar"
	"compress/bzip2"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// DefaultModelURLs is the sherpa-onnx author's int8 export of NVIDIA's
// nemotron-speech-streaming-en-0.6b (English), the 2026-04-25 refresh with an
// explicit 560ms chunk — the accuracy-leaning end of the 80/160/560/1120ms
// range NVIDIA publishes. Chosen deliberately over the newer multilingual
// nemotron-3.5-asr-streaming (June 2026): NVIDIA's own card recommends the
// English model for English (7.9% vs 8.8% WER in auto-detect), and the Go
// binding has no language selector for streaming models yet. int8 keeps
// download + memory bounded without giving up accuracy.
var DefaultModelURLs = map[string]string{
	"encoder.int8.onnx": "https://huggingface.co/csukuangfj2/sherpa-onnx-nemotron-speech-streaming-en-0.6b-560ms-int8-2026-04-25/resolve/main/encoder.int8.onnx",
	"decoder.int8.onnx": "https://huggingface.co/csukuangfj2/sherpa-onnx-nemotron-speech-streaming-en-0.6b-560ms-int8-2026-04-25/resolve/main/decoder.int8.onnx",
	"joiner.int8.onnx":  "https://huggingface.co/csukuangfj2/sherpa-onnx-nemotron-speech-streaming-en-0.6b-560ms-int8-2026-04-25/resolve/main/joiner.int8.onnx",
	"tokens.txt":        "https://huggingface.co/csukuangfj2/sherpa-onnx-nemotron-speech-streaming-en-0.6b-560ms-int8-2026-04-25/resolve/main/tokens.txt",
}

// PunctuationArchiveURL is sherpa-onnx's English online punctuation model
// (a small CNN-BiLSTM, ~7.5MB int8, from frankyoujian/Edge-Punct-Casing),
// applied as a post-pass so transcripts arrive capitalized and punctuated.
// The project publishes it only as a tar.bz2 on GitHub releases; the two
// files below are extracted from it into PunctuationDirName.
const PunctuationArchiveURL = "https://github.com/k2-fsa/sherpa-onnx/releases/download/punctuation-models/sherpa-onnx-online-punct-en-2024-08-06.tar.bz2"

// PunctuationDirName is the subdirectory of the model dir holding the
// punctuation model files.
const PunctuationDirName = "punct-en-2024-08-06"

// PunctuationFiles are the archive members kept, by base name.
var PunctuationFiles = []string{"model.int8.onnx", "bpe.vocab"}

// punctuationArchiveBytes is the archive's size (30.7MB), so the progress bar
// can include it before the download's Content-Length is known.
const punctuationArchiveBytes = 30667839

// ModelSizeMB is the on-disk size of everything a first run downloads
// (speech model ~662MB plus the punctuation model and the VAD model), for
// install UIs that want to say what a first run costs before it starts.
const ModelSizeMB = 691

// VADModelFileName is the on-disk name of the Silero VAD model, alongside the
// speech model files in the same directory.
const VADModelFileName = "silero_vad.onnx"

// VADModelURL is k2-fsa's own Silero VAD export (629KB) — used to confirm an
// utterance has genuinely ended (real speech-probability, not just trailing
// audio silence) before the engine commits a transcript as final. Deliberately
// NOT part of DefaultModelURLs/fingerprintSources: it is independent of the
// pinned ASR model version, and folding it into the fingerprint would send
// every family who already has the ~630MB speech model installed into a
// brand-new, empty directory (a full re-download) just to pick up 629KB.
const VADModelURL = "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad.onnx"

// VADModelInstalled reports whether the VAD model file is present and
// non-empty in dir. Independent of ModelInstalled: an install from before
// this file existed still has a working (if less precise) speech model, and
// EnsureModelFiles fetches this file into that same directory going forward.
func VADModelInstalled(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, VADModelFileName))
	return err == nil && info.Size() > 0
}

// ModelLanguages is the human label for what the pinned model understands.
const ModelLanguages = "English"

// ModelInstalled reports whether every model file is present and non-empty in
// dir — the "installed on disk" half of readiness, independent of whether a
// recognizer has been loaded into memory.
func ModelInstalled(dir string) bool {
	for name := range DefaultModelURLs {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.Size() == 0 {
			return false
		}
	}
	return punctuationInstalled(dir)
}

func punctuationInstalled(dir string) bool {
	for _, name := range PunctuationFiles {
		info, err := os.Stat(filepath.Join(dir, PunctuationDirName, name))
		if err != nil || info.Size() == 0 {
			return false
		}
	}
	return true
}

// DownloadProgress is reported while EnsureModelFiles fetches. Total is the
// sum of Content-Length across the files still to fetch (0 when a server
// does not say), Got is bytes received so far across all of them.
type DownloadProgress struct {
	Got   int64
	Total int64
}

// EnsureModelFiles downloads any file in DefaultModelURLs missing from dir,
// verifying each download is non-empty before accepting it (an interrupted
// download silently truncated is worse than a clear "missing" error). A
// partial file is written beside its target and renamed only on completion,
// so a crash mid-download never leaves a half file that ModelInstalled would
// count. progress may be nil.
func EnsureModelFiles(dir string, progress func(DownloadProgress)) error {
	return EnsureModelFilesContext(context.Background(), dir, progress)
}

type pendingModel struct{ name, url, path string }

// pendingModelDownloads reports what EnsureModelFilesContext still needs to
// fetch into dir. Called twice (before and after acquiring the download
// lock) since a second process may finish the download while this one waits.
func pendingModelDownloads(dir string) (todo []pendingModel, needPunct bool) {
	for name, url := range DefaultModelURLs {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			continue
		}
		todo = append(todo, pendingModel{name, url, path})
	}
	if !VADModelInstalled(dir) {
		todo = append(todo, pendingModel{VADModelFileName, VADModelURL, filepath.Join(dir, VADModelFileName)})
	}
	return todo, !punctuationInstalled(dir)
}

// acquireModelDownloadLock serializes first-time model downloads across
// processes sharing dir (e.g. two desktop instances running
// side by side both warm the voice engine on launch): an O_EXCL lock file
// beside dir means the second process waits for the first to finish instead
// of writing the same partial files concurrently.
func acquireModelDownloadLock(ctx context.Context, dir string) (func(), error) {
	lockPath := dir + ".lock"
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // G304: lockPath is derived from an internal model dir, not user input.
		if err == nil {
			_, _ = f.WriteString(strconv.Itoa(os.Getpid()))
			_ = f.Close()
			return func() { _ = os.Remove(lockPath) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("acquire model download lock: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// EnsureModelFilesContext is EnsureModelFiles with cancellation.
func EnsureModelFilesContext(ctx context.Context, dir string, progress func(DownloadProgress)) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	todo, needPunct := pendingModelDownloads(dir)
	if len(todo) == 0 && !needPunct {
		return nil
	}
	release, err := acquireModelDownloadLock(ctx, dir)
	if err != nil {
		return err
	}
	defer release()
	// Re-check: another process may have completed the download while this
	// one was waiting for the lock.
	todo, needPunct = pendingModelDownloads(dir)
	if len(todo) == 0 && !needPunct {
		return nil
	}
	client := &http.Client{Timeout: 30 * time.Minute}
	// Size everything first so the progress bar has a real denominator from
	// the first byte rather than jumping as each file starts.
	var total int64
	for _, item := range todo {
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, item.url, nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("size %s: %w", item.name, err)
		}
		resp.Body.Close()
		if resp.ContentLength > 0 {
			total += resp.ContentLength
		}
	}
	if needPunct {
		total += punctuationArchiveBytes
	}
	var got int64
	report := func() {
		if progress != nil {
			progress(DownloadProgress{Got: got, Total: total})
		}
	}
	report()
	for _, item := range todo {
		if err := downloadFile(ctx, client, item.url, item.path, func(n int64) {
			got += n
			report()
		}); err != nil {
			return fmt.Errorf("download %s: %w", item.name, err)
		}
	}
	if needPunct {
		if err := ensurePunctuationFiles(ctx, client, dir, func(n int64) {
			got += n
			report()
		}); err != nil {
			return fmt.Errorf("download punctuation model: %w", err)
		}
	}
	return nil
}

// ensurePunctuationFiles downloads PunctuationArchiveURL to a temp file and
// extracts PunctuationFiles into dir/PunctuationDirName. The archive is
// bzip2 tar; both are Go stdlib, so no external tool is needed.
func ensurePunctuationFiles(ctx context.Context, client *http.Client, dir string, onBytes func(int64)) error {
	target := filepath.Join(dir, PunctuationDirName)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "punct-*.tar.bz2")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	if err := downloadFile(ctx, client, PunctuationArchiveURL, tmpPath, onBytes); err != nil {
		return err
	}
	return ExtractArchiveMembers(tmpPath, target, PunctuationFiles)
}

// ExtractArchiveMembers pulls the named members (matched by base name) out
// of a .tar.bz2 into dir, writing each beside a .partial and renaming on
// completion so a crash never leaves a truncated file that counts as
// installed.
func ExtractArchiveMembers(archivePath, dir string, names []string) error {
	wanted := map[string]bool{}
	for _, n := range names {
		wanted[n] = true
	}
	f, err := os.Open(archivePath) //nolint:gosec // G304: path is one this package just wrote.
	if err != nil {
		return err
	}
	defer f.Close()
	tr := tar.NewReader(bzip2.NewReader(f))
	found := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		base := filepath.Base(hdr.Name)
		if hdr.Typeflag != tar.TypeReg || !wanted[base] {
			continue
		}
		dest := filepath.Join(dir, base)
		out, err := os.Create(dest + ".partial") //nolint:gosec // G304: dest derived from a fixed member list.
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil { //nolint:gosec // G110: bounded, trusted release archive.
			out.Close()
			os.Remove(dest + ".partial")
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		if err := os.Rename(dest+".partial", dest); err != nil {
			return err
		}
		found[base] = true
	}
	for _, n := range names {
		if !found[n] {
			return fmt.Errorf("archive has no member %q", n)
		}
	}
	return nil
}

func downloadFile(ctx context.Context, client *http.Client, url, dest string, onBytes func(int64)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req) //nolint:gosec // G107: fixed, hardcoded HuggingFace URLs, not user input.
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	tmp := dest + ".partial"
	f, err := os.Create(tmp) //nolint:gosec // G304: dest is derived from a fixed internal file list, not user input.
	if err != nil {
		return err
	}
	buf := make([]byte, 256*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := f.Write(buf[:n]); writeErr != nil {
				f.Close()
				os.Remove(tmp)
				return writeErr
			}
			onBytes(int64(n))
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			f.Close()
			os.Remove(tmp)
			return readErr
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// RemoveModelFiles deletes the model directory. Callers must have unloaded any
// Engine first; the native recognizer keeps the files mapped while loaded.
func RemoveModelFiles(dir string) error {
	return os.RemoveAll(dir)
}

// modelDirFingerprint is a short, filesystem-safe identifier for a model
// directory derived from its source URLs — used so a shared cache path stays
// stable across process restarts without needing a config file to name it.
func modelDirFingerprint(urls map[string]string) string {
	h := sha256.New()
	// Deterministic order: map iteration is not.
	names := make([]string, 0, len(urls))
	for name := range urls {
		names = append(names, name)
	}
	sortStrings(names)
	for _, name := range names {
		h.Write([]byte(name))
		h.Write([]byte(urls[name]))
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// DefaultModelDir returns a stable, user-scoped cache location for the model
// files, keyed by DefaultModelURLs so a change to the pinned model version
// naturally lands in a fresh directory instead of colliding with stale files.
// It is deliberately the same path for every AgentWorks binary on a machine
// (the agent server and both desktop apps), so one
// ~630MB download serves all of them.
func DefaultModelDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".agentworks", "voice-models", "nemotron-streaming-"+modelDirFingerprint(fingerprintSources()))
}

// fingerprintSources is every URL the model dir is built from, so a change to
// the punctuation model relocates the directory just like a speech-model
// change does.
func fingerprintSources() map[string]string {
	all := map[string]string{PunctuationDirName: PunctuationArchiveURL}
	for k, v := range DefaultModelURLs {
		all[k] = v
	}
	return all
}
