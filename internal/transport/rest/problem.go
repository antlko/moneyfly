package rest

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gofiber/fiber/v2"

	"github.com/antlko/moneyapp/internal/platform/apperr"
)

// ProblemBase namespaces the problem types (RFC 7807).
const ProblemBase = "https://moneyapp.local/problems/"

// ContentTypeProblem is the media type for an error response.
const ContentTypeProblem = "application/problem+json"

// Problem is an RFC 7807 problem document.
type Problem struct {
	Type      string              `json:"type"`
	Title     string              `json:"title"`
	Status    int                 `json:"status"`
	Detail    string              `json:"detail,omitempty"`
	Errors    []apperr.FieldError `json:"errors,omitempty"`
	RequestID string              `json:"request_id,omitempty"`
}

// writeProblem maps a domain error onto an HTTP response. Sentinels are matched
// with errors.Is; nothing is matched by string (conventions §5).
//
// A 500 logs the full error with the request ID and returns only that ID — no
// internal detail crosses the wire.
func writeProblem(c *fiber.Ctx, log *slog.Logger, err error) error {
	reqID := requestID(c)

	var ve *apperr.ValidationError
	switch {
	case errors.As(err, &ve):
		return problem(c, http.StatusUnprocessableEntity, "validation", "Validation failed",
			pluralFields(len(ve.Fields)), ve.Fields, reqID)
	case errors.Is(err, apperr.ErrValidation):
		return problem(c, http.StatusUnprocessableEntity, "validation", "Validation failed", err.Error(), nil, reqID)
	case errors.Is(err, apperr.ErrNotFound):
		return problem(c, http.StatusNotFound, "not-found", "Not found", "", nil, reqID)
	case errors.Is(err, apperr.ErrConflict):
		return problem(c, http.StatusConflict, "conflict", "Conflict", err.Error(), nil, reqID)
	case errors.Is(err, apperr.ErrUnauthorized):
		return problem(c, http.StatusUnauthorized, "unauthorized", "Unauthorized", "", nil, reqID)
	case errors.Is(err, apperr.ErrForbidden):
		return problem(c, http.StatusForbidden, "forbidden", "Forbidden", err.Error(), nil, reqID)
	case errors.Is(err, apperr.ErrTooLarge):
		return problem(c, http.StatusRequestEntityTooLarge, "too-large", "Payload too large", err.Error(), nil, reqID)
	}

	var fe *fiber.Error
	if errors.As(err, &fe) {
		return problem(c, fe.Code, slugForStatus(fe.Code), http.StatusText(fe.Code), fe.Message, nil, reqID)
	}

	log.Error("unhandled error",
		"request_id", reqID, "method", c.Method(), "path", c.Path(), "error", err.Error())
	return problem(c, http.StatusInternalServerError, "internal", "Internal server error",
		"The request could not be completed. Quote the request id when reporting this.", nil, reqID)
}

func problem(c *fiber.Ctx, status int, slug, title, detail string, fields []apperr.FieldError, reqID string) error {
	return sendProblem(c, status, Problem{
		Type:      ProblemBase + slug,
		Title:     title,
		Status:    status,
		Detail:    detail,
		Errors:    fields,
		RequestID: reqID,
	})
}

// sendProblem writes an error body as application/problem+json.
//
// The content type is set *after* the body, because fiber's JSON() sets it to
// application/json and would otherwise win.
func sendProblem(c *fiber.Ctx, status int, payload any) error {
	body, err := c.App().Config().JSONEncoder(payload)
	if err != nil {
		return err
	}
	c.Status(status)
	c.Response().Header.SetContentType(ContentTypeProblem)
	return c.Send(body)
}

func slugForStatus(status int) string {
	switch status {
	case http.StatusNotFound:
		return "not-found"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusConflict:
		return "conflict"
	case http.StatusUnprocessableEntity:
		return "validation"
	case http.StatusTooManyRequests:
		return "rate-limited"
	default:
		if status >= 500 {
			return "internal"
		}
		return "request"
	}
}

func pluralFields(n int) string {
	if n == 1 {
		return "1 field is invalid"
	}
	return strconv.Itoa(n) + " fields are invalid"
}
