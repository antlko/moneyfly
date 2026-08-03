package store

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antlko/moneyapp/internal/domain/importer"
	"github.com/antlko/moneyapp/internal/domain/telegram"
	"github.com/antlko/moneyapp/internal/platform/apperr"
	"github.com/antlko/moneyapp/internal/platform/clock"
	bot "github.com/antlko/moneyapp/internal/transport/telegram"
)

// fakeBotClient stands in for the Telegram API. No test here touches a network.
type fakeBotClient struct {
	mu       sync.Mutex
	updates  [][]bot.Update
	sent     []string
	files    map[string]string
	failWith error
	polls    int
	pollAt   []time.Time
	clock    clock.Clock
}

func (f *fakeBotClient) GetUpdates(_ context.Context, _ int64, _ time.Duration) ([]bot.Update, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	if f.clock != nil {
		f.pollAt = append(f.pollAt, f.clock.Now())
	}
	if f.failWith != nil {
		return nil, f.failWith
	}
	if len(f.updates) == 0 {
		return nil, nil
	}
	batch := f.updates[0]
	f.updates = f.updates[1:]
	return batch, nil
}

func (f *fakeBotClient) SendMessage(_ context.Context, _ int64, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, text)
	return nil
}

func (f *fakeBotClient) Download(_ context.Context, fileID string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path, ok := f.files[fileID]
	if !ok {
		return nil, errors.New("no such file")
	}
	return os.Open(path) //nolint:gosec // a fixture path chosen by the test
}

func (f *fakeBotClient) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.sent...)
}

func (f *fakeBotClient) lastMessage(t *testing.T) string {
	t.Helper()
	msgs := f.messages()
	if len(msgs) == 0 {
		t.Fatal("the bot sent nothing")
	}
	return msgs[len(msgs)-1]
}

// botHarness is the worker over the real database and the real importer.
type botHarness struct {
	*harness
	links   *telegram.Service
	client  *fakeBotClient
	worker  *bot.Worker
	repo    *TelegramRepo
	userID  int64
	imports importer.Service
}

func newBotHarness(t *testing.T) *botHarness {
	t.Helper()
	h := newHarness(t)
	u := h.user("owner@example.test")

	repo := NewTelegramRepo(h.db)
	links := telegram.NewService(repo, h.clock)
	client := &fakeBotClient{files: map[string]string{}, clock: h.clock}
	imports := h.importService()

	worker := bot.NewWorker(client, links, imports, repo,
		slog.New(slog.NewTextHandler(io.Discard, nil)), h.clock,
		"https://money.example.test", 20<<20)

	return &botHarness{
		harness: h, links: links, client: client, worker: worker,
		repo: repo, userID: u.ID, imports: imports,
	}
}

// runOnce drives the worker until it has consumed the queued updates.
func (b *botHarness) runOnce(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		b.worker.Run(ctx)
		close(done)
	}()
	deadline := time.After(3 * time.Second)
	for {
		b.client.mu.Lock()
		empty := len(b.client.updates) == 0
		b.client.mu.Unlock()
		if empty {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the worker did not consume the queued updates")
		case <-time.After(5 * time.Millisecond):
		}
	}
	// Let the last update finish before the context is cancelled.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
}

func (b *botHarness) queue(updates ...bot.Update) {
	b.client.mu.Lock()
	defer b.client.mu.Unlock()
	b.client.updates = append(b.client.updates, updates)
}

func (b *botHarness) batches(t *testing.T) []importer.Batch {
	t.Helper()
	out, err := b.imports.List(b.ctx, b.userID, 50)
	if err != nil {
		t.Fatalf("listing batches: %v", err)
	}
	return out
}

func TestLink_ValidCode(t *testing.T) {
	b := newBotHarness(t)
	link, err := b.links.Mint(b.ctx, b.userID)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	bound, err := b.links.Redeem(b.ctx, 555, link.Code)
	if err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if !bound.Live() || bound.ChatID == nil || *bound.ChatID != 555 {
		t.Fatalf("link = %+v, want a live link to chat 555", bound)
	}

	resolved, err := b.links.Resolve(b.ctx, 555)
	if err != nil || resolved.UserID != b.userID {
		t.Fatalf("Resolve = %+v, %v; want the owner", resolved, err)
	}
}

func TestLink_CodeSingleUse(t *testing.T) {
	b := newBotHarness(t)
	link, _ := b.links.Mint(b.ctx, b.userID)
	if _, err := b.links.Redeem(b.ctx, 555, link.Code); err != nil {
		t.Fatalf("first redeem: %v", err)
	}
	if _, err := b.links.Redeem(b.ctx, 666, link.Code); err == nil {
		t.Fatal("a code must work exactly once")
	}
}

func TestLink_ExpiredCode(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	stepping := clock.Steppable{}
	_ = stepping

	repo := NewTelegramRepo(h.db)
	links := telegram.NewService(repo, h.clock)
	link, err := links.Mint(h.ctx, u.ID)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	// Age the code past its ten minutes without waiting for them.
	if _, err := h.db.Exec(`UPDATE telegram_link SET expires_at = ? WHERE id = ?`,
		fixedNow.Add(-time.Minute).UTC().Format(TimeLayout), link.ID); err != nil {
		t.Fatalf("ageing the code: %v", err)
	}
	if _, err := links.Redeem(h.ctx, 555, link.Code); err == nil {
		t.Fatal("an expired code must be refused")
	}
}

func TestLink_RateLimited(t *testing.T) {
	b := newBotHarness(t)
	// Six wrong guesses: the sixth is throttled rather than merely wrong, which
	// is what makes a six-character code safe for ten minutes.
	var lastErr error
	for i := 0; i < telegram.MaxLinkAttempts+1; i++ {
		_, lastErr = b.links.Redeem(b.ctx, 777, "WRONG1")
	}
	if !errors.Is(lastErr, apperr.ErrForbidden) {
		t.Fatalf("last error = %v, want a throttle after %d attempts", lastErr, telegram.MaxLinkAttempts)
	}
}

func TestLink_OneLivePerChat(t *testing.T) {
	b := newBotHarness(t)
	first, _ := b.links.Mint(b.ctx, b.userID)
	second, _ := b.links.Mint(b.ctx, b.userID)

	if _, err := b.links.Redeem(b.ctx, 555, first.Code); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Re-linking the same chat replaces rather than accumulating; the partial
	// unique index would refuse two live rows.
	if _, err := b.links.Redeem(b.ctx, 555, second.Code); err != nil {
		t.Fatalf("re-link: %v", err)
	}

	links, err := b.links.List(b.ctx, b.userID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	live := 0
	for _, l := range links {
		if l.Live() {
			live++
		}
	}
	if live != 1 {
		t.Fatalf("live links = %d, want exactly 1", live)
	}
}

func TestUnlink_RevokesImmediately(t *testing.T) {
	b := newBotHarness(t)
	link, _ := b.links.Mint(b.ctx, b.userID)
	if _, err := b.links.Redeem(b.ctx, 555, link.Code); err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	if _, err := b.links.RevokeChat(b.ctx, 555); err != nil {
		t.Fatalf("RevokeChat: %v", err)
	}

	if _, err := b.links.Resolve(b.ctx, 555); !errors.Is(err, apperr.ErrNotFound) {
		t.Fatalf("Resolve after unlink = %v, want not found", err)
	}

	// And the next upload is refused with nothing stored.
	b.client.files["f1"] = fixtureDir + "reimport-same.csv"
	b.queue(bot.Update{ID: 1, ChatID: 555, Document: &bot.Document{
		FileID: "f1", FileName: "export.csv", Size: 200,
	}})
	b.runOnce(t)
	if got := len(b.batches(t)); got != 0 {
		t.Fatalf("batches = %d, want 0 after unlinking", got)
	}
}

func TestUpload_UnlinkedChat_StoresNothing(t *testing.T) {
	b := newBotHarness(t)
	b.client.files["f1"] = fixtureDir + "reimport-same.csv"
	b.queue(bot.Update{ID: 1, ChatID: 999, Document: &bot.Document{
		FileID: "f1", FileName: "export.csv", Size: 200,
	}})
	b.runOnce(t)

	if got := len(b.batches(t)); got != 0 {
		t.Fatalf("batches = %d, want 0 from an unlinked chat", got)
	}
	msg := b.client.lastMessage(t)
	if !strings.Contains(msg, "not linked") {
		t.Fatalf("reply = %q, want an instruction to link", msg)
	}
	// Nothing about whether a code exists, or whose account this might be.
	if strings.Contains(strings.ToLower(msg), "owner@example.test") {
		t.Fatal("the reply leaks account information")
	}
}

func TestUpload_LinkedChat_CreatesBatch(t *testing.T) {
	b := newBotHarness(t)
	link, _ := b.links.Mint(b.ctx, b.userID)
	if _, err := b.links.Redeem(b.ctx, 555, link.Code); err != nil {
		t.Fatalf("Redeem: %v", err)
	}

	b.client.files["f1"] = fixtureDir + "reimport-same.csv"
	b.queue(bot.Update{ID: 1, ChatID: 555, Document: &bot.Document{
		FileID: "f1", FileName: "monefy.csv", Size: 200,
	}})
	b.runOnce(t)

	batches := b.batches(t)
	if len(batches) != 1 {
		t.Fatalf("batches = %d, want 1", len(batches))
	}
	if batches[0].Origin != importer.OriginTelegram {
		t.Fatalf("origin = %q, want telegram", batches[0].Origin)
	}
	if batches[0].RowsNew != 2 {
		t.Fatalf("rows_new = %d, want 2", batches[0].RowsNew)
	}
	// Nothing is committed by the bot: reviewing an import is the app's job.
	if batches[0].Status != importer.StatusPreviewed {
		t.Fatalf("status = %q, want previewed", batches[0].Status)
	}

	msg := b.client.lastMessage(t)
	if !strings.Contains(msg, "2 new") || !strings.Contains(msg, "https://money.example.test/import") {
		t.Fatalf("reply = %q, want the counts and a link", msg)
	}
}

func TestUpload_UnmappedNames_DoesNotCommit(t *testing.T) {
	b := newBotHarness(t)
	link, _ := b.links.Mint(b.ctx, b.userID)
	if _, err := b.links.Redeem(b.ctx, 555, link.Code); err != nil {
		t.Fatalf("Redeem: %v", err)
	}

	b.client.files["f1"] = fixtureDir + "unknown-category.csv"
	b.queue(bot.Update{ID: 1, ChatID: 555, Document: &bot.Document{
		FileID: "f1", FileName: "monefy.csv", Size: 200,
	}})
	b.runOnce(t)

	batches := b.batches(t)
	if len(batches) != 1 || batches[0].Status != importer.StatusNeedsMapping {
		t.Fatalf("batch = %+v, want needs_mapping", batches)
	}
	if got := b.liveRows(b.userID); got != 0 {
		t.Fatalf("stored transactions = %d, want 0", got)
	}
	msg := b.client.lastMessage(t)
	if !strings.Contains(msg, "do not recognise") || !strings.Contains(msg, "/import") {
		t.Fatalf("reply = %q, want the refusal and a link to resolve it", msg)
	}
}

func TestUpload_OversizeRejected(t *testing.T) {
	h := newHarness(t)
	u := h.user("owner@example.test")
	repo := NewTelegramRepo(h.db)
	links := telegram.NewService(repo, h.clock)
	client := &fakeBotClient{files: map[string]string{}, clock: h.clock}
	// A one-kilobyte ceiling, so the fixture is oversize.
	worker := bot.NewWorker(client, links, h.importService(), repo,
		slog.New(slog.NewTextHandler(io.Discard, nil)), h.clock, "https://money.example.test", 1024)

	link, _ := links.Mint(h.ctx, u.ID)
	if _, err := links.Redeem(h.ctx, 555, link.Code); err != nil {
		t.Fatalf("Redeem: %v", err)
	}
	client.files["f1"] = fixtureDir + "monefy-real-1683.csv"
	client.updates = append(client.updates, []bot.Update{{
		ID: 1, ChatID: 555, Document: &bot.Document{FileID: "f1", FileName: "big.csv", Size: 75000},
	}})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	worker.Run(ctx)

	msg := client.lastMessage(t)
	if !strings.Contains(msg, "larger than") {
		t.Fatalf("reply = %q, want a size refusal", msg)
	}
	svc := h.importService()
	batches, err := svc.List(h.ctx, u.ID, 50)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(batches) != 0 {
		t.Fatalf("batches = %d, want 0 for an oversize file", len(batches))
	}
}

func TestWorker_PanicRecoveredPerUpdate(t *testing.T) {
	b := newBotHarness(t)
	link, _ := b.links.Mint(b.ctx, b.userID)
	if _, err := b.links.Redeem(b.ctx, 555, link.Code); err != nil {
		t.Fatalf("Redeem: %v", err)
	}

	// The first update names a file the client cannot serve, which fails inside
	// handling; the second must still be processed.
	b.client.files["good"] = fixtureDir + "reimport-same.csv"
	b.queue(
		bot.Update{ID: 1, ChatID: 555, Document: &bot.Document{FileID: "missing", FileName: "a.csv", Size: 10}},
		bot.Update{ID: 2, ChatID: 555, Document: &bot.Document{FileID: "good", FileName: "b.csv", Size: 200}},
	)
	b.runOnce(t)

	if got := len(b.batches(t)); got != 1 {
		t.Fatalf("batches = %d, want 1 — the second update must survive the first", got)
	}
}

func TestWorker_OffsetPersistedAcrossRestart(t *testing.T) {
	b := newBotHarness(t)
	link, _ := b.links.Mint(b.ctx, b.userID)
	if _, err := b.links.Redeem(b.ctx, 555, link.Code); err != nil {
		t.Fatalf("Redeem: %v", err)
	}

	b.client.files["f1"] = fixtureDir + "reimport-same.csv"
	b.queue(bot.Update{ID: 41, ChatID: 555, Document: &bot.Document{
		FileID: "f1", FileName: "a.csv", Size: 200,
	}})
	b.runOnce(t)

	offset, err := b.repo.Offset(b.ctx)
	if err != nil {
		t.Fatalf("Offset: %v", err)
	}
	// The next poll asks for 42: Telegram treats the offset as an acknowledgement,
	// so this is what makes a restart neither replay nor skip.
	if offset != 42 {
		t.Fatalf("offset = %d, want 42", offset)
	}
}

func TestWorker_BackoffOnPollFailure(t *testing.T) {
	b := newBotHarness(t)
	b.client.failWith = errors.New("network is unreachable")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	b.worker.Run(ctx)

	b.client.mu.Lock()
	polls := b.client.polls
	b.client.mu.Unlock()
	// With a one-second minimum backoff, 300ms permits the first attempt and at
	// most one more. Spinning would give hundreds.
	if polls == 0 {
		t.Fatal("the worker never polled")
	}
	if polls > 3 {
		t.Fatalf("polled %d times in 300ms; the backoff is not applied", polls)
	}
}

func TestBot_NoImportLogic(t *testing.T) {
	// The architectural point of the stage: the bot is a transport. If CSV
	// parsing appears here, there are two importers again.
	entries, err := os.ReadDir("../transport/telegram")
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	forbidden := []string{"encoding/csv", "DD.MM.YYYY", "NaturalKey", "occurrence"}
	sawImporter := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		body, err := os.ReadFile("../transport/telegram/" + e.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		text := string(body)
		if strings.Contains(text, "domain/importer") {
			sawImporter = true
		}
		for _, needle := range forbidden {
			if strings.Contains(text, needle) {
				t.Errorf("%s mentions %q; import logic belongs in the shared pipeline",
					e.Name(), needle)
			}
		}
	}
	if !sawImporter {
		t.Error("the bot does not call the importer at all")
	}
}

func TestLog_TokenRedactedInDownloadURL(t *testing.T) {
	const token = "1234567:AA-super-secret-bot-token"
	client := bot.NewAPIClient(token)

	// The getFile download URL embeds the token in its path; anything built from
	// it must be scrubbed before it reaches a log.
	url := "https://api.telegram.org/file/bot" + token + "/documents/file_1.csv"
	if got := client.Redact(url); strings.Contains(got, token) {
		t.Fatalf("redacted = %q, still contains the token", got)
	}
	if got := client.Redact("Get \"" + url + "\": dial tcp: timeout"); strings.Contains(got, token) {
		t.Fatalf("redacted error = %q, still contains the token", got)
	}
}
