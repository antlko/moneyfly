package rest

import (
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/antlko/moneyapp/internal/domain/telegram"
)

func (s *Server) registerTelegramRoutes(api fiber.Router) {
	guard := []fiber.Handler{s.requireSession(), s.requirePasswordChanged()}

	g := api.Group("/telegram", guard...)
	g.Post("/link-code", s.requireWritable(), s.handleMintLinkCode)
	g.Get("/links", s.handleListTelegramLinks)
	g.Delete("/links/:id", s.requireWritable(), s.handleRevokeTelegramLink)
}

// TelegramLinkDTO is one chat link on the wire.
type TelegramLinkDTO struct {
	ID     int64  `json:"id"`
	ChatID *int64 `json:"chat_id"`
	// Code is only returned when the link is minted. It is never listed
	// afterwards: it is a credential for the ten minutes it lives.
	Code      string  `json:"code,omitempty"`
	ExpiresAt string  `json:"expires_at"`
	LinkedAt  *string `json:"linked_at"`
	RevokedAt *string `json:"revoked_at"`
	Live      bool    `json:"live"`
	CreatedAt string  `json:"created_at"`
}

func toTelegramLinkDTO(l telegram.Link, includeCode bool) TelegramLinkDTO {
	out := TelegramLinkDTO{
		ID: l.ID, ChatID: l.ChatID,
		ExpiresAt: l.ExpiresAt.UTC().Format(time.RFC3339),
		Live:      l.Live(),
		CreatedAt: l.CreatedAt.UTC().Format(time.RFC3339),
	}
	if includeCode {
		out.Code = l.Code
	}
	if l.LinkedAt != nil {
		v := l.LinkedAt.UTC().Format(time.RFC3339)
		out.LinkedAt = &v
	}
	if l.RevokedAt != nil {
		v := l.RevokedAt.UTC().Format(time.RFC3339)
		out.RevokedAt = &v
	}
	return out
}

func (s *Server) handleMintLinkCode(c *fiber.Ctx) error {
	u := currentUser(c)
	link, err := s.deps.Telegram.Mint(c.UserContext(), u.ID)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"code":       link.Code,
		"expires_at": link.ExpiresAt.UTC().Format(time.RFC3339),
		"instructions": "Send /link " + link.Code +
			" to the bot from the chat you want to import from. The code works once, for ten minutes.",
	})
}

func (s *Server) handleListTelegramLinks(c *fiber.Ctx) error {
	u := currentUser(c)
	links, err := s.deps.Telegram.List(c.UserContext(), u.ID)
	if err != nil {
		return err
	}
	out := make([]TelegramLinkDTO, 0, len(links))
	for _, l := range links {
		out = append(out, toTelegramLinkDTO(l, false))
	}
	return c.JSON(out)
}

func (s *Server) handleRevokeTelegramLink(c *fiber.Ctx) error {
	u := currentUser(c)
	id, err := paramInt64(c, "id")
	if err != nil {
		return err
	}
	if err := s.deps.Telegram.Revoke(c.UserContext(), u.ID, id); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
