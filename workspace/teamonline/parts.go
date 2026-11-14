package teamonline

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/manishiitg/coding-agent-loop/workspace/publication"
)

type partWriter struct {
	root  *os.Root
	file  *os.File
	used  int64
	parts []string
}

func (w *partWriter) Write(data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		if w.file == nil || w.used == publication.MaxFileBytes {
			if w.file != nil {
				if err := w.file.Close(); err != nil {
					return written, err
				}
			}
			if len(w.parts) >= publication.MaxFiles {
				return written, fmt.Errorf("too many dataset parts")
			}
			name := fmt.Sprintf("data/%06d.jsonl", len(w.parts)+1)
			file, err := w.root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return written, err
			}
			w.file = file
			w.used = 0
			w.parts = append(w.parts, name)
		}
		size := min(int64(len(data)), publication.MaxFileBytes-w.used)
		n, err := w.file.Write(data[:size])
		w.used += int64(n)
		written += n
		data = data[n:]
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func ExportParts(ctx context.Context, source *sql.DB, wanted Dataset, root *os.Root) (Schema, []string, error) {
	if err := root.Mkdir("data", 0700); err != nil && !os.IsExist(err) {
		return Schema{}, nil, err
	}
	writer := &partWriter{root: root}
	schema, err := Export(ctx, source, wanted, writer)
	if writer.file != nil {
		closeErr := writer.file.Close()
		if err == nil {
			err = closeErr
		}
	}
	return schema, writer.parts, err
}

type partReader struct {
	root    *os.Root
	parts   []string
	current *os.File
}

func (r *partReader) Read(out []byte) (int, error) {
	for {
		if r.current == nil {
			if len(r.parts) == 0 {
				return 0, io.EOF
			}
			p := r.parts[0]
			r.parts = r.parts[1:]
			if _, err := publication.CleanPath(p); err != nil || !strings.HasPrefix(p, "data/") {
				return 0, fmt.Errorf("invalid dataset part")
			}
			file, err := r.root.Open(p)
			if err != nil {
				return 0, err
			}
			info, err := file.Stat()
			if err != nil || !info.Mode().IsRegular() || info.Size() > publication.MaxFileBytes {
				file.Close()
				return 0, fmt.Errorf("invalid dataset part size or type")
			}
			r.current = file
		}
		n, err := r.current.Read(out)
		if err == io.EOF {
			r.current.Close()
			r.current = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}
func (r *partReader) Close() error {
	if r.current != nil {
		return r.current.Close()
	}
	return nil
}
func DescribeParts(root *os.Root, parts []string) (Schema, error) {
	reader := &partReader{root: root, parts: parts}
	defer reader.Close()
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), int(publication.MaxFileBytes))
	if !scanner.Scan() {
		if scanner.Err() != nil {
			return Schema{}, scanner.Err()
		}
		return Schema{}, fmt.Errorf("dataset schema is missing")
	}
	var header record
	if err := json.Unmarshal(scanner.Bytes(), &header); err != nil {
		return Schema{}, err
	}
	if header.Kind != "schema" || header.Schema == nil {
		return Schema{}, fmt.Errorf("dataset schema is missing")
	}
	return *header.Schema, nil
}
func ImportParts(ctx context.Context, destination *sql.DB, root *os.Root, parts []string, seed bool) error {
	if !seed {
		return nil
	} // Do not inspect/open old data parts on a republish.
	if len(parts) == 0 || len(parts) > publication.MaxFiles {
		return fmt.Errorf("dataset parts are missing or exceed the publication limit")
	}
	reader := &partReader{root: root, parts: parts}
	defer reader.Close()
	return Import(ctx, destination, reader, true)
}

// InspectParts lets the common receiver check actual cell bytes against its
// authorized secret values. Scanning the transport's base64/escaped JSON alone
// would miss a value in a blob or non-UTF8 text. Nothing is logged or returned
// except a plain refusal, and this function grants no Vault access itself.
func InspectParts(root *os.Root, parts []string, containsSecret func([]byte) bool) error {
	if containsSecret == nil {
		return fmt.Errorf("the item secret check is unavailable")
	}
	reader := &partReader{root: root, parts: parts}
	defer reader.Close()
	scanner := bufio.NewScanner(io.LimitReader(reader, publication.MaxBytes+1))
	scanner.Buffer(make([]byte, 64<<10), int(publication.MaxFileBytes))
	var total int64
	for scanner.Scan() {
		total += int64(len(scanner.Bytes()) + 1)
		if total > publication.MaxBytes {
			return fmt.Errorf("dataset exceeds publication limit")
		}
		if containsSecret(scanner.Bytes()) {
			return fmt.Errorf("this dataset contains a secret value; select secret references instead")
		}
		var row record
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&row); err != nil {
			return err
		}
		if row.Schema != nil {
			for _, object := range row.Schema.Objects {
				if containsSecret([]byte(object.SQL)) {
					return fmt.Errorf("this dataset schema contains a secret value; select secret references instead")
				}
			}
		}
		for _, value := range row.Values {
			var raw []byte
			switch value.Kind {
			case "text":
				raw = []byte(value.Text)
				if value.Blob != nil {
					raw = value.Blob
				}
			case "blob":
				raw = value.Blob
			}
			if len(raw) > 0 && containsSecret(raw) {
				return fmt.Errorf("this dataset contains a secret value; select secret references instead")
			}
		}
	}
	return scanner.Err()
}
