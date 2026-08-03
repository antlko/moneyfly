package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// APIClient talks to the Bot API over HTTPS, long-polling only.
//
// There is no webhook mode: it would need a routable HTTPS endpoint to save a few
// seconds on a monthly upload, and an inbound endpoint is precisely what this
// design avoids (docs/adr/0013-telegram-in-process.md).
type APIClient struct {
	token string
	base  string
	http  *http.Client
}

// NewAPIClient builds the client. The token is never logged; see Redact.
func NewAPIClient(token string) *APIClient {
	return &APIClient{
		token: token,
		base:  "https://api.telegram.org",
		// Longer than the poll timeout, so a long-poll is not cut short by the
		// transport.
		http: &http.Client{Timeout: PollTimeout + 30*time.Second},
	}
}

// WithBaseURL points the client at a test server.
func (c *APIClient) WithBaseURL(base string) *APIClient {
	c.base = strings.TrimRight(base, "/")
	return c
}

// Redact removes the bot token from a string.
//
// The getFile download URL embeds the token in its path, so any log line
// carrying a URL would otherwise leak the credential that controls the bot.
func (c *APIClient) Redact(s string) string {
	if c.token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, "[redacted]")
}

func (c *APIClient) method(name string) string {
	return c.base + "/bot" + c.token + "/" + name
}

type apiResponse[T any] struct {
	OK          bool   `json:"ok"`
	Result      T      `json:"result"`
	Description string `json:"description"`
	ErrorCode   int    `json:"error_code"`
}

// GetUpdates long-polls for updates.
func (c *APIClient) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	query := url.Values{}
	query.Set("offset", strconv.FormatInt(offset, 10))
	query.Set("timeout", strconv.Itoa(int(timeout.Seconds())))
	// Only what the bot acts on. Asking for less is less to go wrong.
	query.Set("allowed_updates", `["message"]`)

	body, err := c.get(ctx, c.method("getUpdates")+"?"+query.Encode())
	if err != nil {
		return nil, err
	}
	var decoded apiResponse[[]struct {
		UpdateID int64 `json:"update_id"`
		Message  *struct {
			Chat struct {
				ID int64 `json:"id"`
			} `json:"chat"`
			Text     string `json:"text"`
			Caption  string `json:"caption"`
			Document *struct {
				FileID   string `json:"file_id"`
				FileName string `json:"file_name"`
				FileSize int64  `json:"file_size"`
			} `json:"document"`
		} `json:"message"`
	}]
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("telegram: decoding getUpdates: %w", err)
	}
	if !decoded.OK {
		return nil, fmt.Errorf("telegram: getUpdates: %s", decoded.Description)
	}

	out := make([]Update, 0, len(decoded.Result))
	for _, raw := range decoded.Result {
		update := Update{ID: raw.UpdateID}
		if raw.Message == nil {
			out = append(out, update)
			continue
		}
		update.ChatID = raw.Message.Chat.ID
		update.Text = strings.TrimSpace(raw.Message.Text)
		if raw.Message.Document != nil {
			update.Document = &Document{
				FileID:   raw.Message.Document.FileID,
				FileName: raw.Message.Document.FileName,
				Size:     raw.Message.Document.FileSize,
			}
		}
		out = append(out, update)
	}
	return out, nil
}

// SendMessage posts one reply.
func (c *APIClient) SendMessage(ctx context.Context, chatID int64, text string) error {
	query := url.Values{}
	query.Set("chat_id", strconv.FormatInt(chatID, 10))
	query.Set("text", text)
	query.Set("disable_web_page_preview", "true")

	body, err := c.get(ctx, c.method("sendMessage")+"?"+query.Encode())
	if err != nil {
		return err
	}
	var decoded apiResponse[json.RawMessage]
	if err := json.Unmarshal(body, &decoded); err != nil {
		return fmt.Errorf("telegram: decoding sendMessage: %w", err)
	}
	if !decoded.OK {
		return fmt.Errorf("telegram: sendMessage: %s", decoded.Description)
	}
	return nil
}

// Download streams a file. The body is returned unread, so a large upload never
// sits in memory.
func (c *APIClient) Download(ctx context.Context, fileID string) (io.ReadCloser, error) {
	body, err := c.get(ctx, c.method("getFile")+"?file_id="+url.QueryEscape(fileID))
	if err != nil {
		return nil, err
	}
	var decoded apiResponse[struct {
		FilePath string `json:"file_path"`
	}]
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("telegram: decoding getFile: %w", err)
	}
	if !decoded.OK || decoded.Result.FilePath == "" {
		return nil, fmt.Errorf("telegram: getFile: %s", decoded.Description)
	}

	// This URL embeds the bot token. It is never logged, and any error built from
	// it is redacted before it leaves this method.
	downloadURL := c.base + "/file/bot" + c.token + "/" + decoded.Result.FilePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram: building the download request: %w", c.redactErr(err))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram: downloading the file: %w", c.redactErr(err))
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("telegram: downloading the file: status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func (c *APIClient) get(ctx context.Context, endpoint string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram: building a request: %w", c.redactErr(err))
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram: calling the api: %w", c.redactErr(err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("telegram: reading a response: %w", c.redactErr(err))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram: api returned %d: %s",
			resp.StatusCode, c.Redact(string(body)))
	}
	return body, nil
}

// redactErr strips the token from an error whose message may contain the URL.
func (c *APIClient) redactErr(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", c.Redact(err.Error()))
}
