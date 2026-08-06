package api

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"

	"moneyfly/internal/auth"
	"moneyfly/internal/db"
)

// adminMW requires the signed-in account to be an admin. It runs after authMW
// (routes register both), so userLocal is already valid here.
func (s *Server) adminMW(c fiber.Ctx) error {
	if !userLocal(c).IsAdmin {
		return fiber.NewError(fiber.StatusForbidden, "admin only")
	}
	return c.Next()
}

// handleListUsers is the roster the admin screen shows — every account on
// the instance, not just the caller's own.
func (s *Server) handleListUsers(c fiber.Ctx) error {
	users, err := s.conn().ListUsers()
	if err != nil {
		return err
	}
	out := make([]AdminUserDTO, 0, len(users))
	for i := range users {
		out = append(out, toAdminUserDTO(&users[i]))
	}
	return c.JSON(out)
}

type adminCreateUserRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
}

// handleAdminCreateUser provisions an account directly — the "root manages
// the others" path for an instance that keeps `registration: closed` and
// still wants more than one person on it. It shares every validation rule
// self-registration has (internal/api/handlers_auth.go); the only
// difference is who is allowed to call it and that `registration` is not
// consulted at all, because an admin adding someone is not the public
// signing themselves up.
//
// The new account is provisioned, not signed in — unlike handleRegister,
// this request is the admin's session, not the new person's, so there is no
// browser here to hand a cookie to.
func (s *Server) handleAdminCreateUser(c fiber.Ctx) error {
	var in adminCreateUserRequest
	if err := decode(c, &in); err != nil {
		return err
	}
	email, err := parseEmail(in.Email)
	if err != nil {
		return err
	}
	if err := auth.ValidatePassword(in.Password); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}

	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(in.DisplayName)
	if name == "" {
		name, _, _ = strings.Cut(email, "@")
	}

	user, err := s.conn().CreateUser(email, hash, name, s.config().App.DefaultCurrency)
	if errors.Is(err, db.ErrEmailTaken) {
		return fiber.NewError(fiber.StatusConflict, "that email is already registered")
	}
	if err != nil {
		return err
	}
	return c.Status(fiber.StatusCreated).JSON(toAdminUserDTO(user))
}

// handleAdminDeleteUser removes an account and everything it owns
// (db.DeleteUser). Self-deletion is refused here, before it ever reaches the
// last-admin check: leaving through an admin screen that also manages other
// people's accounts is a different, more consequential action than the
// ordinary account settings screen, and one confusing enough to just not
// offer — there is nothing here a person cannot already do to their own
// account elsewhere.
func (s *Server) handleAdminDeleteUser(c fiber.Ctx) error {
	id := c.Params("id")
	if id == userLocal(c).ID {
		return fiber.NewError(fiber.StatusBadRequest, "manage your own account from Settings, not here")
	}
	err := s.conn().DeleteUser(id)
	switch {
	case errors.Is(err, db.ErrLastAdmin):
		return fiber.NewError(fiber.StatusConflict, "cannot delete the last admin")
	case errors.Is(err, db.ErrNotFound):
		return fiber.NewError(fiber.StatusNotFound, "user not found")
	case err != nil:
		return err
	}
	return c.SendStatus(fiber.StatusNoContent)
}

type setAdminRequest struct {
	IsAdmin bool `json:"isAdmin"`
}

// handleSetAdmin promotes or demotes an account. Unlike delete, acting on
// your own row is allowed — stepping down as admin when someone else already
// holds it is a reasonable thing to want — and refused only by the same
// last-admin rule that applies to anyone.
func (s *Server) handleSetAdmin(c fiber.Ctx) error {
	var in setAdminRequest
	if err := decode(c, &in); err != nil {
		return err
	}
	id := c.Params("id")
	err := s.conn().SetAdmin(id, in.IsAdmin)
	switch {
	case errors.Is(err, db.ErrLastAdmin):
		return fiber.NewError(fiber.StatusConflict, "cannot remove the last admin")
	case errors.Is(err, db.ErrNotFound):
		return fiber.NewError(fiber.StatusNotFound, "user not found")
	case err != nil:
		return err
	}
	user, err := s.conn().UserByID(id)
	if err != nil {
		return err
	}
	return c.JSON(toAdminUserDTO(user))
}
