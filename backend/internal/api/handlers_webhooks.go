package api

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"moneyfly/internal/auth"
	"moneyfly/internal/db"
)

// webhookDTO includes the secret, unlike apiTokenDTO's hash: the operator has
// to see it again to configure whatever receives the delivery, and unlike a
// token it is never by itself a way into this account — it only ever signs a
// payload this server sends out, never lets anyone in.
type webhookDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Secret    string `json:"secret"`
	CreatedAt int64  `json:"createdAt"`
}

func toWebhookDTO(w db.Webhook) webhookDTO {
	return webhookDTO{ID: w.ID, Name: w.Name, URL: w.URL, Secret: w.Secret, CreatedAt: w.CreatedAt}
}

func (s *Server) handleListWebhooks(c fiber.Ctx) error {
	hooks, err := s.conn().ListWebhooks(userLocal(c).ID)
	if err != nil {
		return err
	}
	out := make([]webhookDTO, 0, len(hooks))
	for _, h := range hooks {
		out = append(out, toWebhookDTO(h))
	}
	return c.JSON(out)
}

type createWebhookRequest struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (s *Server) handleCreateWebhook(c fiber.Ctx) error {
	var in createWebhookRequest
	if err := decode(c, &in); err != nil {
		return err
	}
	name := strings.TrimSpace(in.Name)
	url := strings.TrimSpace(in.URL)
	if name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name must not be empty")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fiber.NewError(fiber.StatusBadRequest, "url must start with http:// or https://")
	}

	secret, err := auth.NewURLToken()
	if err != nil {
		return err
	}
	stored, err := s.conn().CreateWebhook(db.NewID(), userLocal(c).ID, name, url, secret)
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(toWebhookDTO(stored))
}

func (s *Server) handleDeleteWebhook(c fiber.Ctx) error {
	if err := s.conn().DeleteWebhook(userLocal(c).ID, c.Params("id")); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
