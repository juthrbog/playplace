package audit

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"playplace/internal/core"
)

// File appends JSON lines to a local file. History is then local to the
// machine that ran the command, which is the point: nothing depends on AWS.
type File struct {
	Path string
	mu   sync.Mutex
}

func (f *File) Where() string { return f.Path }
func (f *File) Close() error  { return nil }

func (f *File) Record(ctx context.Context, e core.AuditEvent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
		return err
	}
	fh, err := os.OpenFile(f.Path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer fh.Close()
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	// A write cut short earlier leaves a line without its newline; starting
	// on a fresh line keeps this record readable and confines the damage to
	// the torn one, which Query skips.
	if st, err := fh.Stat(); err == nil && st.Size() > 0 {
		last := make([]byte, 1)
		if _, err := fh.ReadAt(last, st.Size()-1); err == nil && last[0] != '\n' {
			line = append([]byte{'\n'}, line...)
		}
	}
	// One write per line keeps appends from different processes intact.
	_, err = fh.Write(append(line, '\n'))
	return err
}

func (f *File) Query(ctx context.Context, flt Filter) ([]core.AuditEvent, error) {
	page, err := f.Search(ctx, flt)
	return page.Events, err
}

func (f *File) Search(ctx context.Context, flt Filter) (Page, error) {
	if _, err := decodeCursor(flt); err != nil {
		return Page{}, err
	}
	if err := ctx.Err(); err != nil {
		return Page{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fh, err := os.Open(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return Page{Events: []core.AuditEvent{}}, nil
	}
	if err != nil {
		return Page{}, err
	}
	defer fh.Close()
	incomplete := false
	var out []core.AuditEvent
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return Page{}, err
		}
		var e core.AuditEvent
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil || e.At.IsZero() || e.Event == "" {
			// Keep readable entries, but never present a damaged log as complete.
			incomplete = true
			continue
		}
		if flt.matches(e) {
			out = append(out, e)
		}
	}
	if err := sc.Err(); err != nil {
		return Page{}, err
	}
	return paginate(out, flt, incomplete)
}
