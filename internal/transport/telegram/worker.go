// Package telegram is the optional, in-process bot worker: long-poll getUpdates,
// relay an uploaded export to the importer, reply once.
//
// It is off by default, does exactly one thing, and runs under a supervising
// goroutine so a bot crash cannot take down the web server
// (docs/adr/0013-telegram-in-process.md).
//
// **The bot contains no import logic.** It calls importer.Receive and formats the
// answer. Parsing, aliases, dedup and commit are the shared pipeline from stage
// 04; a second path here is exactly how the old system drifted.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"strings"
	"time"

	"github.com/antlko/moneyapp/internal/domain/importer"
	"github.com/antlko/moneyapp/internal/domain/telegram"
	"github.com/antlko/moneyapp/internal/platform/clock"
)

// Update is one Telegram update, reduced to what the bot uses.
type Update struct {
	ID       int64
	ChatID   int64
	Text     string
	Document *Document
}

// Document is an uploaded file.
type Document struct {
	FileID   string
	FileName string
	Size     int64
}

// Client is the Telegram API surface the worker needs. It is an interface so the
// tests can drive the whole worker without a network.
type Client interface {
	// GetUpdates long-polls from offset. It returns when updates arrive or the
	// timeout elapses, whichever is first.
	GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error)
	SendMessage(ctx context.Context, chatID int64, text string) error
	// Download streams a file. The caller closes the reader.
	Download(ctx context.Context, fileID string) (io.ReadCloser, error)
}

// Importer is the stage-04 seam. This is the whole of the bot's import surface.
type Importer interface {
	Receive(ctx context.Context, userID int64, origin importer.Origin, filename string, r io.Reader) (*importer.Batch, error)
}

// Links resolves a chat to an account.
type Links interface {
	Resolve(ctx context.Context, chatID int64) (telegram.Link, error)
	Redeem(ctx context.Context, chatID int64, code string) (telegram.Link, error)
	RevokeChat(ctx context.Context, chatID int64) (int, error)
}

// Backoff bounds. A flapping network must not fill the disk with log lines, so
// the worker logs once per state transition rather than once per attempt.
const (
	MinBackoff  = time.Second
	MaxBackoff  = 60 * time.Second
	PollTimeout = 30 * time.Second
)

// Worker is the long-poll loop.
type Worker struct {
	client   Client
	links    Links
	imports  Importer
	offsets  telegram.OffsetStore
	log      *slog.Logger
	clock    clock.Clock
	baseURL  string
	maxBytes int64

	// failing tracks whether the last poll failed, so the log records the
	// transition rather than every attempt.
	failing bool
}

// NewWorker builds the worker.
func NewWorker(
	client Client, links Links, imports Importer, offsets telegram.OffsetStore,
	log *slog.Logger, clk clock.Clock, baseURL string, maxBytes int64,
) *Worker {
	if maxBytes <= 0 {
		maxBytes = 20 << 20 // Telegram's own getFile ceiling
	}
	return &Worker{
		client: client, links: links, imports: imports, offsets: offsets,
		log: log, clock: clk, baseURL: strings.TrimRight(baseURL, "/"), maxBytes: maxBytes,
	}
}

// Run polls until the context is cancelled. It never returns an error: a bot
// that cannot reach Telegram is a degraded feature, not a reason to stop serving.
func (w *Worker) Run(ctx context.Context) {
	offset, err := w.offsets.Offset(ctx)
	if err != nil {
		w.log.Error("telegram: reading the poll offset", "error", err.Error())
	}
	w.log.Info("telegram: worker started", "offset", offset)

	backoff := MinBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		updates, err := w.client.GetUpdates(ctx, offset, PollTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if !w.failing {
				// Once per transition, not once per attempt.
				w.log.Warn("telegram: polling failed, backing off", "error", err.Error())
				w.failing = true
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(jitter(backoff)):
			}
			backoff *= 2
			if backoff > MaxBackoff {
				backoff = MaxBackoff
			}
			continue
		}
		if w.failing {
			w.log.Info("telegram: polling recovered")
			w.failing = false
		}
		backoff = MinBackoff

		for _, update := range updates {
			w.handleSafely(ctx, update)
			// The offset advances whether or not handling succeeded: a single
			// poisonous update must not be replayed forever.
			offset = update.ID + 1
			if err := w.offsets.SetOffset(ctx, offset, w.clock.Now()); err != nil {
				w.log.Error("telegram: writing the poll offset", "error", err.Error())
			}
		}
	}
}

// handleSafely runs one update and recovers from a panic, so a malformed message
// costs that message and nothing else.
func (w *Worker) handleSafely(ctx context.Context, update Update) {
	defer func() {
		if r := recover(); r != nil {
			w.log.Error("telegram: update panicked", "update_id", update.ID, "panic", r)
		}
	}()
	if err := w.handle(ctx, update); err != nil {
		w.log.Warn("telegram: update failed",
			"update_id", update.ID, "chat_id", update.ChatID, "error", err.Error())
	}
}

func (w *Worker) handle(ctx context.Context, update Update) error {
	switch {
	case update.Document != nil:
		return w.handleDocument(ctx, update)
	case strings.HasPrefix(update.Text, "/link"):
		return w.handleLink(ctx, update)
	case strings.HasPrefix(update.Text, "/unlink"):
		return w.handleUnlink(ctx, update)
	case strings.HasPrefix(update.Text, "/start"), strings.HasPrefix(update.Text, "/help"):
		return w.client.SendMessage(ctx, update.ChatID, helpText)
	case update.Text == "":
		return nil
	default:
		// Deliberately not a command surface. Everything else the app does, it
		// does better; two renderers of the same numbers drift apart.
		return w.client.SendMessage(ctx, update.ChatID, helpText)
	}
}

const helpText = `MoneyApp

/link <code> — connect this chat to your account
/unlink — disconnect it
/help — this message

Then share a Monefy CSV export here and it will be imported.
Everything else lives in the app.`

func (w *Worker) handleLink(ctx context.Context, update Update) error {
	code := strings.TrimSpace(strings.TrimPrefix(update.Text, "/link"))
	if code == "" {
		return w.client.SendMessage(ctx, update.ChatID,
			"Send /link followed by the code from Settings → Telegram.")
	}
	if _, err := w.links.Redeem(ctx, update.ChatID, code); err != nil {
		// One message for every failure: a wrong code must not be
		// distinguishable from an expired or already-used one.
		return w.client.SendMessage(ctx, update.ChatID,
			"That code is not valid. Generate a new one in Settings → Telegram.")
	}
	return w.client.SendMessage(ctx, update.ChatID, "Linked. Share a Monefy export here to import it.")
}

func (w *Worker) handleUnlink(ctx context.Context, update Update) error {
	revoked, err := w.links.RevokeChat(ctx, update.ChatID)
	if err != nil {
		return err
	}
	if revoked == 0 {
		return w.client.SendMessage(ctx, update.ChatID, "This chat was not linked.")
	}
	return w.client.SendMessage(ctx, update.ChatID, "Unlinked. Nothing sent here will be imported.")
}

func (w *Worker) handleDocument(ctx context.Context, update Update) error {
	link, err := w.links.Resolve(ctx, update.ChatID)
	if err != nil {
		// Nothing is stored and nothing is revealed about whether a code exists.
		return w.client.SendMessage(ctx, update.ChatID,
			"This chat is not linked. Use /link with a code from Settings → Telegram.")
	}
	if update.Document.Size > w.maxBytes {
		return w.client.SendMessage(ctx, update.ChatID,
			fmt.Sprintf("That file is larger than the %d MB limit.", w.maxBytes>>20))
	}

	body, err := w.client.Download(ctx, update.Document.FileID)
	if err != nil {
		return fmt.Errorf("telegram: downloading %s: %w", update.Document.FileName, err)
	}
	defer func() { _ = body.Close() }()

	// The whole of the bot's import surface. Everything below this line is
	// formatting.
	batch, err := w.imports.Receive(ctx, link.UserID, importer.OriginTelegram,
		update.Document.FileName, io.LimitReader(body, w.maxBytes+1))
	if err != nil {
		return w.client.SendMessage(ctx, update.ChatID, "That file could not be read: "+plain(err))
	}
	return w.client.SendMessage(ctx, update.ChatID, w.reply(*batch))
}

// reply is the one message per upload: what happened, and where to go next.
func (w *Worker) reply(batch importer.Batch) string {
	link := fmt.Sprintf("%s/import", w.baseURL)
	switch {
	case batch.AlreadyImported:
		return fmt.Sprintf("That exact file was already imported (%d rows). %s",
			batch.RowsTotal, link)
	case batch.Status == importer.StatusNeedsMapping:
		return fmt.Sprintf(
			"%d row(s) use names I do not recognise, so nothing was imported. Resolve them: %s",
			batch.RowsUnmapped, link)
	case batch.Status == importer.StatusFailed:
		return "That file could not be processed. " + link
	default:
		return fmt.Sprintf("Ready: %d new, %d duplicate, %d rejected. Review and commit: %s",
			batch.RowsNew, batch.RowsDuplicate, batch.RowsRejected, link)
	}
}

// plain keeps an error one line and free of internals.
func plain(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, "\n"); i > 0 {
		msg = msg[:i]
	}
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	//nolint:gosec // spreading retries, not a security decision
	return d + time.Duration(rand.Int63n(int64(d/2)+1))
}

// ErrDisabled is returned when the worker is asked to start while disabled.
var ErrDisabled = errors.New("telegram: disabled")
