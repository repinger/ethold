package ethol

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ansiReset   = "\033[0m"
	ansiDim     = "\033[2m"
	ansiCyan    = "\033[36m"
	ansiYellow  = "\033[33m"
	ansiRed     = "\033[1;31m"
	ansiMagenta = "\033[35m"
	ansiGray    = "\033[90m"
)

var bufPool = sync.Pool{
	New: func() any {
		return new(bytes.Buffer)
	},
}

// PrettyHandlerOptions configures the PrettyHandler.
type PrettyHandlerOptions struct {
	Level      slog.Leveler
	NoColor    bool
	ForceColor bool
}

// PrettyHandler is an slog.Handler that formats records neatly for CLI readability.
type PrettyHandler struct {
	opts   PrettyHandlerOptions
	w      io.Writer
	mu     *sync.Mutex
	attrs  []slog.Attr
	groups []string
	prefix string
}

// NewPrettyHandler returns a new PrettyHandler writing to w.
func NewPrettyHandler(w io.Writer, opts *PrettyHandlerOptions) *PrettyHandler {
	var opt PrettyHandlerOptions
	if opts != nil {
		opt = *opts
	}
	if opt.Level == nil {
		opt.Level = slog.LevelInfo
	}
	// ponytail: basic TTY and NO_COLOR check; upgrade if complex terminal capabilities needed
	if !opt.ForceColor && (opt.NoColor || !isTerminal(w)) {
		opt.NoColor = true
	}

	return &PrettyHandler{
		opts: opt,
		w:    w,
		mu:   &sync.Mutex{},
	}
}

func isTerminal(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// Enabled reports whether the handler handles records at the given level.
func (h *PrettyHandler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return level >= minLevel
}

// Handle formats and outputs the slog.Record.
func (h *PrettyHandler) Handle(_ context.Context, r slog.Record) error {
	buf := bufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bufPool.Put(buf)

	// Timestamp
	var timeBuf [8]byte
	timeFormatted := r.Time.AppendFormat(timeBuf[:0], "15:04:05")
	if h.opts.NoColor {
		buf.Write(timeFormatted)
	} else {
		buf.WriteString(ansiGray)
		buf.Write(timeFormatted)
		buf.WriteString(ansiReset)
	}
	buf.WriteByte(' ')

	// Level badge
	buf.WriteString(formatLevel(r.Level, h.opts.NoColor))
	buf.WriteByte(' ')

	// Message
	buf.WriteString(r.Message)

	// Attached attributes
	for _, attr := range h.attrs {
		h.appendAttr(buf, "", attr)
	}

	// Record attributes
	r.Attrs(func(attr slog.Attr) bool {
		h.appendAttr(buf, h.prefix, attr)
		return true
	})

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf.Bytes())
	return err
}

// WithAttrs returns a new handler with the given attributes added.
func (h *PrettyHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	prefix := h.prefix
	newAttrs := make([]slog.Attr, len(h.attrs), len(h.attrs)+len(attrs))
	copy(newAttrs, h.attrs)
	for _, a := range attrs {
		a.Key = prefix + a.Key
		newAttrs = append(newAttrs, a)
	}

	return &PrettyHandler{
		opts:   h.opts,
		w:      h.w,
		mu:     h.mu,
		attrs:  newAttrs,
		groups: h.groups,
		prefix: h.prefix,
	}
}

// WithGroup returns a new handler with the given group name appended.
func (h *PrettyHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	newGroups := make([]string, len(h.groups)+1)
	copy(newGroups, h.groups)
	newGroups[len(h.groups)] = name

	return &PrettyHandler{
		opts:   h.opts,
		w:      h.w,
		mu:     h.mu,
		attrs:  h.attrs,
		groups: newGroups,
		prefix: strings.Join(newGroups, ".") + ".",
	}
}

func (h *PrettyHandler) appendAttr(buf *bytes.Buffer, groupPrefix string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}

	buf.WriteByte(' ')
	key := groupPrefix + attr.Key
	if h.opts.NoColor {
		buf.WriteString(key)
		buf.WriteByte('=')
	} else {
		buf.WriteString(ansiDim)
		buf.WriteString(key)
		buf.WriteString("=")
		buf.WriteString(ansiReset)
	}

	appendValue(buf, attr.Value)
}

func appendValue(buf *bytes.Buffer, v slog.Value) {
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		if needsQuoting(s) {
			buf.WriteString(strconv.Quote(s))
		} else {
			buf.WriteString(s)
		}
	case slog.KindTime:
		buf.WriteString(v.Time().Format(time.RFC3339))
	case slog.KindDuration:
		buf.WriteString(v.Duration().String())
	default:
		s := fmt.Sprint(v.Any())
		if needsQuoting(s) {
			buf.WriteString(strconv.Quote(s))
		} else {
			buf.WriteString(s)
		}
	}
}

func needsQuoting(s string) bool {
	if len(s) == 0 {
		return true
	}
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] == '=' || s[i] == '"' {
			return true
		}
	}
	return false
}

func formatLevel(l slog.Level, noColor bool) string {
	if noColor {
		switch {
		case l >= slog.LevelError:
			return "[ERROR]"
		case l >= slog.LevelWarn:
			return "[WARN ]"
		case l >= slog.LevelInfo:
			return "[INFO ]"
		default:
			return "[DEBUG]"
		}
	}
	switch {
	case l >= slog.LevelError:
		return ansiRed + "[ERROR]" + ansiReset
	case l >= slog.LevelWarn:
		return ansiYellow + "[WARN ]" + ansiReset
	case l >= slog.LevelInfo:
		return ansiCyan + "[INFO ]" + ansiReset
	default:
		return ansiMagenta + "[DEBUG]" + ansiReset
	}
}
