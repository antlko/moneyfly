package db

import (
	"encoding/json"
	"errors"
	"testing"

	syncproto "moneyfly/internal/sync"
)

func TestListUsersOldestFirst(t *testing.T) {
	d := openTest(t)
	a := mustUser(t, d, "a@example.com")
	b := mustUser(t, d, "b@example.com")

	users, err := d.ListUsers()
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 2 || users[0].ID != a.ID || users[1].ID != b.ID {
		t.Fatalf("ListUsers = %+v, want [a, b] in creation order", users)
	}
}

// The whole point: an account's data must actually be gone, not just
// unreachable through the API — every synced table carries the same foreign
// key back to users (docs/ARCHITECTURE.md §1), so one DELETE is enough.
func TestDeleteUserCascadesToDomainRows(t *testing.T) {
	d := openTest(t)
	first := mustUser(t, d, "admin@example.com") // stays admin throughout
	victim := mustUser(t, d, "b@example.com")

	data, _ := json.Marshal(map[string]any{"name": "Cash", "currency": "EUR"})
	if _, err := d.ApplyOps(victim.ID, []syncproto.Op{
		{Entity: "account", ID: "a1", Lamport: 1, DeviceID: "dev-a", Data: data},
	}); err != nil {
		t.Fatalf("seed account: %v", err)
	}

	if err := d.DeleteUser(victim.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if _, err := d.UserByID(victim.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("UserByID after delete: err = %v, want ErrNotFound", err)
	}
	rows, _, err := d.SnapshotRows(victim.ID)
	if err != nil {
		t.Fatalf("SnapshotRows: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("deleted user's data still has %d live rows: %+v", len(rows), rows)
	}

	// first must be unaffected.
	if _, err := d.UserByID(first.ID); err != nil {
		t.Errorf("the other account was affected by an unrelated delete: %v", err)
	}
}

func TestDeleteUserRefusesTheLastAdmin(t *testing.T) {
	d := openTest(t)
	admin := mustUser(t, d, "admin@example.com")
	mustUser(t, d, "b@example.com") // not admin — second account, admin stays first

	if err := d.DeleteUser(admin.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("DeleteUser(only admin): err = %v, want ErrLastAdmin", err)
	}
}

func TestDeleteUserAllowsAnAdminWhenAnotherRemains(t *testing.T) {
	d := openTest(t)
	admin := mustUser(t, d, "admin@example.com")
	second := mustUser(t, d, "b@example.com")
	if err := d.SetAdmin(second.ID, true); err != nil {
		t.Fatalf("SetAdmin: %v", err)
	}

	if err := d.DeleteUser(admin.ID); err != nil {
		t.Errorf("DeleteUser(one of two admins): %v", err)
	}
}

func TestSetAdminRefusesToDemoteTheLastAdmin(t *testing.T) {
	d := openTest(t)
	admin := mustUser(t, d, "admin@example.com")

	if err := d.SetAdmin(admin.ID, false); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("SetAdmin(only admin, false): err = %v, want ErrLastAdmin", err)
	}
}

func TestSetAdminPromotesAndDemotes(t *testing.T) {
	d := openTest(t)
	mustUser(t, d, "admin@example.com")
	second := mustUser(t, d, "b@example.com")

	if err := d.SetAdmin(second.ID, true); err != nil {
		t.Fatalf("promote: %v", err)
	}
	got, err := d.UserByID(second.ID)
	if err != nil || !got.IsAdmin {
		t.Fatalf("second = %+v, %v; want IsAdmin", got, err)
	}

	if err := d.SetAdmin(second.ID, false); err != nil {
		t.Fatalf("demote: %v", err)
	}
	got, err = d.UserByID(second.ID)
	if err != nil || got.IsAdmin {
		t.Fatalf("second = %+v, %v; want not IsAdmin", got, err)
	}
}
