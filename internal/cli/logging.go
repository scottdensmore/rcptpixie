package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
)

func newLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(&cliHandler{w: w, level: level, mu: new(sync.Mutex)})
}

func levelFor(verbose, quiet bool) slog.Level {
	switch {
	case quiet:
		return slog.LevelError
	case verbose:
		return slog.LevelDebug
	}
	return slog.LevelWarn
}

// cliHandler writes "warn: could not read page  file=x.pdf page=3". slog's
// TextHandler prefixes every line with time= and level=, which reads as machine
// output in an interactive tool; the timestamp appears only at Debug. w is
// always stderr in production.
type cliHandler struct {
	w      io.Writer
	level  slog.Level
	mu     *sync.Mutex
	attrs  []slog.Attr
	prefix string // group prefix for record attrs, e.g. "http."
}

func (h *cliHandler) Enabled(_ context.Context, l slog.Level) bool { return l >= h.level }

func (h *cliHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	if h.level <= slog.LevelDebug && !r.Time.IsZero() {
		b.WriteString(r.Time.Format("15:04:05.000 "))
	}
	b.WriteString(label(r.Level))
	b.WriteString(": ")
	b.WriteString(r.Message)
	for _, a := range h.attrs {
		writeAttr(&b, "", a)
	}
	r.Attrs(func(a slog.Attr) bool {
		writeAttr(&b, h.prefix, a)
		return true
	})
	b.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := io.WriteString(h.w, b.String())
	return err
}

func (h *cliHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	c := *h
	c.attrs = make([]slog.Attr, 0, len(h.attrs)+len(attrs))
	c.attrs = append(c.attrs, h.attrs...)
	for _, a := range attrs {
		a.Key = h.prefix + a.Key
		c.attrs = append(c.attrs, a)
	}
	return &c
}

func (h *cliHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	c := *h
	c.prefix = h.prefix + name + "."
	return &c
}

func label(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l >= slog.LevelInfo:
		return "info"
	}
	return "debug"
}

func writeAttr(b *strings.Builder, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	key := prefix + a.Key
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		if a.Key != "" {
			key += "."
		} else {
			key = prefix
		}
		for _, sub := range group {
			writeAttr(b, key, sub)
		}
		return
	}
	fmt.Fprintf(b, "  %s=%s", key, quoted(a.Value.String()))
}

func quoted(s string) string {
	if s == "" || strings.ContainsAny(s, " \t\n\"") {
		return fmt.Sprintf("%q", s)
	}
	return s
}
