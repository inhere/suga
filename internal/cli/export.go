package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gookit/goutil/cflag/capp"
	appconfig "github.com/inhere/suga/internal/config"
	"github.com/inhere/suga/internal/personal"
	"github.com/inhere/suga/internal/personal/store"
)

// newExportCommand builds `suga inspect export`.
func newExportCommand(ctx context.Context, stdout, stderr io.Writer) *capp.Cmd {
	var redact, withOutputs bool
	var outFile string
	cmd := capp.NewCmd("export", "Export a session's entries and events as jsonl", func(c *capp.Cmd) error {
		return runExport(ctx, stdout, c.Arg("session").String(), exportOptions{redact: redact, withOutputs: withOutputs, outFile: outFile})
	})
	cmd.SetOutput(stderr)
	cmd.BoolVar(&redact, "redact", false, "drop tool call arguments")
	cmd.BoolVar(&withOutputs, "with-outputs", false, "append the session's externalised tool outputs (one output row per file)")
	cmd.StringVar(&outFile, "output", "", "write to this file (mode 0600) instead of stdout;;o")
	cmd.AddArg("session", "the session ID", true)
	return cmd
}

// exportOptions are the flags of `suga inspect export`.
type exportOptions struct {
	redact      bool
	withOutputs bool
	outFile     string
}

// runExport writes a session's entries and events (merged by time) as jsonl;
// with withOutputs the files under sessions/<id>/outputs/ follow as rows of
// kind "output".
func runExport(ctx context.Context, stdout io.Writer, sessionID string, opts exportOptions) error {
	repo, err := openCoreStore()
	if err != nil {
		return err
	}
	defer repo.Close()
	if _, err := repo.GetSession(ctx, sessionID); err != nil {
		return fmt.Errorf("export session %q: %w", sessionID, err)
	}
	entries, err := repo.ListEntries(ctx, sessionID, store.EntryFilter{})
	if err != nil {
		return err
	}
	events, err := repo.ListEvents(ctx, store.EventFilter{SessionID: sessionID})
	if err != nil {
		return err
	}
	var buf strings.Builder
	if err := writeExport(&buf, entries, events, opts.redact); err != nil {
		return err
	}
	if opts.withOutputs {
		if err := writeExportOutputs(&buf, sessionID); err != nil {
			return err
		}
	}
	if opts.outFile == "" {
		_, err = io.WriteString(stdout, buf.String())
		return err
	}
	return os.WriteFile(opts.outFile, []byte(buf.String()), 0o600)
}

// writeExportOutputs appends one {"kind":"output","ref",...,"content"} row per
// output of the session, sorted by name. Each file is read through
// personal.OpenOutput, which resolves the ref inside the session directory and
// decodes a compressed .txt.gz while it is read; the plain .txt wins when both
// forms exist, so one ref yields exactly one row. A session without outputs
// adds nothing.
func writeExportOutputs(w io.Writer, sessionID string) error {
	sessionsDir, err := appconfig.ResolveSessionsDir()
	if err != nil {
		return err
	}
	dir, ok := sessionDir(sessionsDir, sessionID)
	if !ok {
		return nil
	}
	root, err := os.OpenRoot(filepath.Join(dir, "outputs"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	f, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return err
	}
	names, err := f.Readdirnames(-1)
	_ = f.Close()
	_ = root.Close()
	if err != nil {
		return err
	}
	sort.Strings(names)
	done := map[string]bool{}
	for _, name := range names {
		stem, ok := personal.OutputStem(name)
		if !ok || done[stem] {
			continue
		}
		done[stem] = true
		ref := "out:" + sessionID + ":" + stem
		r, err := personal.OpenOutput(sessionsDir, ref)
		if err != nil {
			return fmt.Errorf("read output %s: %w", name, err)
		}
		if err := writeOutputRow(w, sessionID, ref, r); err != nil {
			_ = r.Close()
			return fmt.Errorf("read output %s: %w", name, err)
		}
		if err := r.Close(); err != nil {
			return fmt.Errorf("read output %s: %w", name, err)
		}
	}
	return nil
}

// writeOutputRow writes one output row, streaming the content through r so that
// a large (possibly compressed) output is not held in memory as a whole; bytes
// is the size of the decoded content, so a compressed file reports the same
// size as its plain form did.
func writeOutputRow(w io.Writer, sessionID, ref string, r io.Reader) error {
	session, err := json.Marshal(sessionID)
	if err != nil {
		return err
	}
	quotedRef, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "{\"kind\":\"output\",\"session_id\":%s,\"ref\":%s,\"content\":", session, quotedRef); err != nil {
		return err
	}
	n, err := writeJSONString(w, r)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, ",\"bytes\":%d}\n", n)
	return err
}

// writeJSONString writes the bytes of r as one JSON string literal (quotes
// included) and returns how many bytes it wrote. The stream is escaped chunk by
// chunk and a chunk that ends in the middle of a rune holds its tail back for
// the next one, so the content never has to be held in memory as a whole and no
// rune is split into a replacement character.
func writeJSONString(w io.Writer, r io.Reader) (int64, error) {
	if _, err := w.Write([]byte{'"'}); err != nil {
		return 0, err
	}
	var (
		total   int64
		pending []byte
		buf     = make([]byte, 64<<10)
	)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if len(pending) > 0 {
				// The previous chunk ended in the middle of a rune: glue it on.
				chunk = append(append(make([]byte, 0, len(pending)+n), pending...), chunk...)
			}
			pending = pending[:0]
			if cut := completeRunes(chunk); cut < len(chunk) {
				pending = append(pending, chunk[cut:]...)
				chunk = chunk[:cut]
			}
			if len(chunk) > 0 {
				escaped, err := json.Marshal(string(chunk))
				if err != nil {
					return total, err
				}
				if _, err := w.Write(escaped[1 : len(escaped)-1]); err != nil {
					return total, err
				}
				total += int64(len(chunk))
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				return total, readErr
			}
			break
		}
	}
	if len(pending) > 0 {
		escaped, err := json.Marshal(string(pending))
		if err != nil {
			return total, err
		}
		if _, err := w.Write(escaped[1 : len(escaped)-1]); err != nil {
			return total, err
		}
		total += int64(len(pending))
	}
	if _, err := w.Write([]byte{'"'}); err != nil {
		return total, err
	}
	return total, nil
}

// completeRunes returns the length of the longest prefix of b that ends on a
// UTF-8 rune boundary; an unfinished rune at the end is left out so that
// escaping it later cannot turn it into a replacement character.
func completeRunes(b []byte) int {
	if utf8.Valid(b) {
		return len(b)
	}
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if !utf8.RuneStart(b[i]) {
			continue
		}
		if r, size := utf8.DecodeRune(b[i:]); r == utf8.RuneError && size <= 1 {
			return i // needs the bytes of the next chunk
		}
		return len(b)
	}
	return len(b)
}

// secretArgKeys are event attr keys that may hold tool-call arguments.
var secretArgKeys = []string{"arguments", "args", "input", "params", "parameters"}

// writeExport merges entries and events by time (entries first on ties, and
// entries keep seq order, events keep id order) and writes one JSON object per
// line.
func writeExport(w io.Writer, entries []store.Entry, events []store.Event, redact bool) error {
	enc := json.NewEncoder(w)
	i, j := 0, 0
	for i < len(entries) || j < len(events) {
		if j >= len(events) || (i < len(entries) && !entries[i].CreatedAt.After(events[j].StartedAt)) {
			e := entries[i]
			i++
			body := e.JSON
			if redact {
				body = redactEntryJSON(body)
			}
			if err := enc.Encode(map[string]any{
				"kind": "entry", "session_id": e.SessionID, "seq": e.Seq, "turn_id": e.TurnID,
				"type": e.Type, "created_at": e.CreatedAt.UTC().Format(time.RFC3339Nano), "json": json.RawMessage(body),
			}); err != nil {
				return err
			}
			continue
		}
		e := events[j]
		j++
		attrs := e.Attrs
		if redact {
			attrs = make(map[string]any, len(e.Attrs))
			for k, v := range e.Attrs {
				attrs[k] = v
			}
			for _, k := range secretArgKeys {
				delete(attrs, k)
			}
		}
		if err := enc.Encode(map[string]any{
			"kind": "event", "id": e.ID, "session_id": e.SessionID, "turn_id": e.TurnID,
			"event_kind": e.Kind, "name": e.Name, "status": e.Status,
			"started_at":  e.StartedAt.UTC().Format(time.RFC3339Nano),
			"duration_ms": e.DurationMs, "ttft_ms": e.TTFTMs,
			"input_tokens": e.InputTokens, "output_tokens": e.OutputTokens,
			"cache_read_tokens": e.CacheReadTokens, "cache_write_tokens": e.CacheWriteTokens,
			"error": e.Error, "attrs": attrs,
		}); err != nil {
			return err
		}
	}
	return nil
}

// redactEntryJSON removes ToolCall.Arguments from a serialized agentcore
// Message (ToolCall has no json tags, so the key is "Arguments"; a lowercase
// spelling is handled too). Unparseable bodies are returned as-is.
func redactEntryJSON(raw json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	for _, key := range []string{"ToolCall", "tool_call"} {
		var tc map[string]json.RawMessage
		if json.Unmarshal(m[key], &tc) != nil || tc == nil {
			continue
		}
		delete(tc, "Arguments")
		delete(tc, "arguments")
		b, err := json.Marshal(tc)
		if err != nil {
			return raw
		}
		m[key] = b
	}
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return b
}
