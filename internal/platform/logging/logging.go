// Package logging builds the application logger: log/slog, JSON to stdout,
// with a PII-masking handler in front of it.
//
// The masking exists because the system this replaces kept a live Telegram bot
// token as a source constant, and Telegram download URLs embed that token in
// the path. A logger that prints request URLs would leak it on every import.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"regexp"
	"strings"
)

// Redacted is what replaces a masked value.
const Redacted = "***"

// sensitiveKeys are attribute names whose value never reaches the log.
var sensitiveKeys = []string{
	"password", "passwd", "pass", "token", "api_key", "apikey", "secret",
	"authorization", "cookie", "set-cookie", "session", "bootstrap_password",
}

var (
	// Telegram bot tokens: <bot id>:<35 char secret>. Matched anywhere in a value,
	// so a getFile URL is masked even though its key is only "url".
	//
	// No leading \b: in "…/file/bot123456789:AAE…" there is no word boundary
	// between "bot" and the digits, which is exactly where the token appears.
	telegramToken = regexp.MustCompile(`[0-9]{6,12}:[A-Za-z0-9_-]{30,}`)
	// key=value / key: value pairs inside free text or a query string.
	inlineSecret = regexp.MustCompile(`(?i)\b(password|passwd|token|api_key|apikey|secret)\b(\s*[:=]\s*)("?)([^\s,;}"]+)("?)`)
)

// Options configures the logger.
type Options struct {
	Level   string // debug | info | warn | error
	Format  string // json | text
	MaskPII bool
}

// New builds a *slog.Logger writing to w.
func New(w io.Writer, opts Options) *slog.Logger {
	handlerOpts := &slog.HandlerOptions{Level: parseLevel(opts.Level)}
	var h slog.Handler
	if strings.EqualFold(opts.Format, "text") {
		h = slog.NewTextHandler(w, handlerOpts)
	} else {
		h = slog.NewJSONHandler(w, handlerOpts)
	}
	if opts.MaskPII {
		h = NewMaskingHandler(h)
	}
	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// MaskingHandler redacts secrets before they reach the wrapped handler.
type MaskingHandler struct{ inner slog.Handler }

// NewMaskingHandler wraps inner.
func NewMaskingHandler(inner slog.Handler) *MaskingHandler { return &MaskingHandler{inner: inner} }

// Enabled implements slog.Handler.
func (h *MaskingHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

// Handle implements slog.Handler.
func (h *MaskingHandler) Handle(ctx context.Context, r slog.Record) error {
	masked := slog.NewRecord(r.Time, r.Level, MaskString(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		masked.AddAttrs(maskAttr(a))
		return true
	})
	return h.inner.Handle(ctx, masked)
}

// WithAttrs implements slog.Handler.
func (h *MaskingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, maskAttr(a))
	}
	return &MaskingHandler{inner: h.inner.WithAttrs(out)}
}

// WithGroup implements slog.Handler.
func (h *MaskingHandler) WithGroup(name string) slog.Handler {
	return &MaskingHandler{inner: h.inner.WithGroup(name)}
}

func maskAttr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		group := v.Group()
		out := make([]any, 0, len(group))
		for _, g := range group {
			out = append(out, maskAttr(g))
		}
		return slog.Group(a.Key, out...)
	}
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, Redacted)
	}
	// Values are scanned too: a Telegram file URL carries the bot token in its
	// path, and a logged struct carries its field names into the output.
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, MaskString(v.String()))
	case slog.KindAny:
		return slog.Any(a.Key, redactValue(v.Any(), 0))
	default:
		return slog.Attr{Key: a.Key, Value: v}
	}
}

// maxRedactDepth bounds the walk. A log line is not the place to recurse forever.
const maxRedactDepth = 6

// redactValue walks an arbitrary value and redacts anything held under a sensitive
// field or map key.
//
// The alternative — rendering the value with %+v and running the text through
// MaskString — would work but flattens every struct in the logs. Walking keeps the
// structure and still catches the secret.
func redactValue(v any, depth int) any {
	if v == nil || depth > maxRedactDepth {
		return v
	}
	switch typed := v.(type) {
	case string:
		return MaskString(typed)
	case error:
		return MaskString(typed.Error())
	case fmt.Stringer:
		return MaskString(typed.String())
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nil
		}
		return redactValue(rv.Elem().Interface(), depth+1)
	case reflect.Struct:
		out := make(map[string]any, rv.NumField())
		t := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			if isSensitiveKey(field.Name) {
				out[field.Name] = Redacted
				continue
			}
			out[field.Name] = redactValue(rv.Field(i).Interface(), depth+1)
		}
		return out
	case reflect.Map:
		out := make(map[string]any, rv.Len())
		for _, key := range rv.MapKeys() {
			name := fmt.Sprint(key.Interface())
			if isSensitiveKey(name) {
				out[name] = Redacted
				continue
			}
			out[name] = redactValue(rv.MapIndex(key).Interface(), depth+1)
		}
		return out
	case reflect.Slice, reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			// Byte slices are payloads, not structures; mask them as text.
			return MaskString(string(rv.Bytes()))
		}
		out := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, redactValue(rv.Index(i).Interface(), depth+1))
		}
		return out
	default:
		return v
	}
}

func isSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

// MaskString redacts secrets embedded in free text.
func MaskString(s string) string {
	if s == "" {
		return s
	}
	out := telegramToken.ReplaceAllString(s, Redacted)
	out = inlineSecret.ReplaceAllString(out, "$1$2"+Redacted)
	return out
}
