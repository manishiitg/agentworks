// Package chatterminal carries one explicitly selected Claude journal. It never
// accepts a provider home, account settings or an arbitrary destination path.
package chatterminal

import (
	"archive/tar"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

type Reference struct {
	Server  string `json:"server"`
	Key     string `json:"key"`
	Project string `json:"project"`
}

func (r Reference) String() string {
	b, _ := json.Marshal(r)
	return base64.RawURLEncoding.EncodeToString(b)
}

func ParseReference(s string) (Reference, error) {
	var r Reference
	if len(s) > 8192 {
		return r, fmt.Errorf("choose the command from this chat's menu")
	}
	b, e := base64.RawURLEncoding.DecodeString(s)
	if e != nil || json.Unmarshal(b, &r) != nil || r.Server == "" || r.Key == "" || r.Project == "" {
		return r, fmt.Errorf("choose the command from this chat's menu")
	}
	return r, nil
}

type Transfer struct {
	LocalFolder string                  `json:"local_folder,omitempty"`
	Reference   Reference               `json:"chat"`
	Receipt     string                  `json:"receipt"`
	Session     string                  `json:"session"`
	Bundle      *sessiontransfer.Bundle `json:"native"`
}

func (t Transfer) Validate() error {
	if t.Receipt == "" || t.Session == "" || t.Bundle == nil || t.Bundle.Provider != "claude-code" || t.Bundle.Format != "claude-jsonl" || len(t.Bundle.Files) != 1 {
		return fmt.Errorf("this handoff must contain exactly one Claude session")
	}
	return t.Bundle.Validate()
}

func Write(w io.Writer, t Transfer) error {
	if e := t.Validate(); e != nil {
		return e
	}
	if e := t.Bundle.ValidateFiles(); e != nil {
		return e
	}
	data, e := json.Marshal(t)
	if e != nil {
		return e
	}
	wc := tar.NewWriter(w)
	if e = wc.WriteHeader(&tar.Header{Name: "handoff.json", Mode: 0600, Size: int64(len(data))}); e != nil {
		return e
	}
	if _, e = wc.Write(data); e != nil {
		return e
	}
	f, e := os.Open(filepath.Join(t.Bundle.Root, "session.jsonl"))
	if e != nil {
		return e
	}
	defer f.Close()
	if e = wc.WriteHeader(&tar.Header{Name: "session.jsonl", Mode: 0600, Size: t.Bundle.Files["session.jsonl"].Size}); e != nil {
		return e
	}
	if _, e = io.Copy(wc, f); e != nil {
		return e
	}
	return wc.Close()
}

// dir is a newly created private staging directory chosen by the receiver,
// never a path from the archive. A complete validated journal precedes import.
func Read(r io.Reader, dir string) (Transfer, error) {
	var t Transfer
	tr := tar.NewReader(r)
	h, e := tr.Next()
	if e != nil {
		return t, e
	}
	if h.Name != "handoff.json" || h.Typeflag != tar.TypeReg || h.Size <= 0 || h.Size > 64<<10 {
		return t, fmt.Errorf("invalid handoff descriptor")
	}
	data, e := io.ReadAll(tr)
	if e != nil {
		return t, e
	}
	if e = json.Unmarshal(data, &t); e != nil {
		return t, e
	}
	if e = t.Validate(); e != nil {
		return t, e
	}
	h, e = tr.Next()
	if e != nil {
		return t, e
	}
	if h.Name != "session.jsonl" || h.Typeflag != tar.TypeReg || h.Size != t.Bundle.Files["session.jsonl"].Size {
		return t, fmt.Errorf("invalid Claude journal payload")
	}
	f, e := os.OpenFile(filepath.Join(dir, "session.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return t, e
	}
	_, e = io.CopyN(f, tr, h.Size)
	ce := f.Close()
	if e != nil {
		return t, e
	}
	if ce != nil {
		return t, ce
	}
	if _, e = tr.Next(); e != io.EOF {
		return t, fmt.Errorf("unexpected files in Claude handoff")
	}
	t.Bundle.Root = dir
	if e = t.Bundle.ValidateFiles(); e != nil {
		return t, e
	}
	if e = t.Bundle.ValidateJournals(); e != nil {
		return t, e
	}
	return t, nil
}
