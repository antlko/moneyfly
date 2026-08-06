package api

import (
	"strings"

	"github.com/gofiber/fiber/v3"

	"moneyfly/internal/auth"
	"moneyfly/internal/db"
)

type apiTokenDTO struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	CreatedAt  int64  `json:"createdAt"`
	LastUsedAt int64  `json:"lastUsedAt"`
}

func toAPITokenDTO(t db.APIToken) apiTokenDTO {
	return apiTokenDTO{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt}
}

// handleListAPITokens never returns a hash or the plaintext — a token is
// shown once, at creation, and never again.
func (s *Server) handleListAPITokens(c fiber.Ctx) error {
	tokens, err := s.conn().ListAPITokens(userLocal(c).ID)
	if err != nil {
		return err
	}
	out := make([]apiTokenDTO, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, toAPITokenDTO(t))
	}
	return c.JSON(out)
}

type createTokenRequest struct {
	Name string `json:"name"`
}

type createTokenResponse struct {
	apiTokenDTO
	// Token is the plaintext value. It is not recoverable after this
	// response — only its hash is stored, the same as a session cookie.
	Token string `json:"token"`
}

func (s *Server) handleCreateAPIToken(c fiber.Ctx) error {
	var in createTokenRequest
	if err := decode(c, &in); err != nil {
		return err
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return fiber.NewError(fiber.StatusBadRequest, "name must not be empty")
	}

	token, err := auth.NewToken()
	if err != nil {
		return err
	}
	stored, err := s.conn().CreateAPIToken(db.NewID(), userLocal(c).ID, name, auth.HashToken(token))
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(createTokenResponse{
		apiTokenDTO: toAPITokenDTO(stored),
		Token:       token,
	})
}

func (s *Server) handleDeleteAPIToken(c fiber.Ctx) error {
	if err := s.conn().DeleteAPIToken(userLocal(c).ID, c.Params("id")); err != nil {
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}
