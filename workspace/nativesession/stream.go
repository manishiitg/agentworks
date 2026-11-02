// Package nativesession streams one selected journal between the service and
// its owner's slot helper. It never includes provider accounts or global state.
package nativesession

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

const HeaderLimit = 8 << 20

func names(b *sessiontransfer.Bundle) []string {
	out := make([]string, 0, len(b.Files))
	for n := range b.Files {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Payload preserves decoder read-ahead and removes only the protocol newline.
func Payload(d *json.Decoder, r io.Reader) (io.Reader, error) {
	buffered := bufio.NewReader(io.MultiReader(d.Buffered(), r))
	c, err := buffered.ReadByte()
	if err != nil || c != '\n' {
		return nil, fmt.Errorf("invalid native stream framing")
	}
	return buffered, nil
}

func WriteFiles(w io.Writer, b *sessiontransfer.Bundle) error {
	if err := b.ValidateFiles(); err != nil {
		return err
	}
	for _, name := range names(b) {
		p, err := sessiontransfer.Path(b.Root, name, false)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		_, err = io.CopyN(w, f, b.Files[name].Size)
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func ReadFiles(r io.Reader, b *sessiontransfer.Bundle, root string) error {
	if err := b.Validate(); err != nil {
		return err
	}
	b.Root = root
	for _, name := range names(b) {
		p, err := sessiontransfer.Path(root, name, true)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = io.CopyN(f, r, b.Files[name].Size)
		closed := f.Close()
		if err != nil {
			return err
		}
		if closed != nil {
			return closed
		}
	}
	return b.ValidateFiles()
}

func ReadBundle(r io.Reader, root string) (*sessiontransfer.Bundle, error) {
	decoder := json.NewDecoder(io.LimitReader(r, HeaderLimit+1))
	decoder.DisallowUnknownFields()
	b := &sessiontransfer.Bundle{}
	if err := decoder.Decode(b); err != nil {
		return nil, err
	}
	payload, err := Payload(decoder, r)
	if err != nil {
		return nil, err
	}
	return b, ReadFiles(payload, b, root)
}
