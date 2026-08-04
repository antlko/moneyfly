package api

import (
	"moneyfly/internal/db"
)

// Wire shapes are camelCase; database columns are snake_case. This file is the
// only place the two meet — the frontend's types mirror what is here.

// UserDTO is the signed-in account, as the SPA sees it.
type UserDTO struct {
	ID           string        `json:"id"`
	Email        string        `json:"email"`
	DisplayName  string        `json:"displayName"`
	BaseCurrency string        `json:"baseCurrency"`
	IsAdmin      bool          `json:"isAdmin"`
	HasPassword  bool          `json:"hasPassword"`
	Identities   []IdentityDTO `json:"identities"`
}

// IdentityDTO is one linked identity provider. The provider's subject id is
// deliberately absent: the UI has no use for it and it is an account identifier
// at a third party.
type IdentityDTO struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"`
	Email     string `json:"email"`
	CreatedAt int64  `json:"createdAt"`
}

// DeviceDTO is one browser that syncs.
type DeviceDTO struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	CreatedAt  int64  `json:"createdAt"`
	LastSeenAt int64  `json:"lastSeenAt"`
	Current    bool   `json:"current"`
}

func toUserDTO(u *db.User, ids []db.Identity) UserDTO {
	out := UserDTO{
		ID:           u.ID,
		Email:        u.Email,
		DisplayName:  u.DisplayName,
		BaseCurrency: u.BaseCurrency,
		IsAdmin:      u.IsAdmin,
		HasPassword:  u.PasswordHash != "",
		Identities:   make([]IdentityDTO, 0, len(ids)),
	}
	for _, i := range ids {
		out.Identities = append(out.Identities, IdentityDTO{
			ID:        i.ID,
			Provider:  i.Provider,
			Email:     i.Email,
			CreatedAt: i.CreatedAt,
		})
	}
	return out
}

func toDeviceDTOs(devices []db.Device, currentID string) []DeviceDTO {
	out := make([]DeviceDTO, 0, len(devices))
	for _, d := range devices {
		out = append(out, DeviceDTO{
			ID:         d.ID,
			Name:       d.Name,
			Platform:   d.Platform,
			CreatedAt:  d.CreatedAt,
			LastSeenAt: d.LastSeenAt,
			Current:    d.ID == currentID,
		})
	}
	return out
}
