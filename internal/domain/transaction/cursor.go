package transaction

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/antlko/moneyapp/internal/platform/apperr"
)

// EncodeCursor renders a pagination cursor for the wire.
//
// Pagination is cursor-based on (occurred_on, id) rather than by offset: an import
// inserts thousands of rows at once, and an offset would drift mid-scroll
// (docs/09-api.md §9.1).
func EncodeCursor(c Cursor) string {
	payload := cursorPayload{D: c.OccurredOn.UTC().Format(DateLayout), I: c.ID}
	buf, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(buf)
}

// DecodeCursor parses a pagination cursor. An empty string is "start at the top".
func DecodeCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, nil
	}
	buf, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, apperr.Validation("cursor", "is not a valid cursor")
	}
	var payload cursorPayload
	if err := json.Unmarshal(buf, &payload); err != nil {
		return Cursor{}, apperr.Validation("cursor", "is not a valid cursor")
	}
	on, err := time.Parse(DateLayout, payload.D)
	if err != nil {
		return Cursor{}, apperr.Validation("cursor", "is not a valid cursor")
	}
	return Cursor{OccurredOn: on.UTC(), ID: payload.I, Set: true}, nil
}

type cursorPayload struct {
	D string `json:"d"`
	I int64  `json:"i"`
}
