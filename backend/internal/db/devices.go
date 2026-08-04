package db

import "time"

// Device is a browser profile that syncs. The id is minted client-side, so this
// row is created on first contact rather than handed out by the server.
type Device struct {
	ID         string
	UserID     string
	Name       string
	Platform   string
	CreatedAt  int64
	LastSeenAt int64
	LastSeq    int64
}

// UpsertDevice registers a device for an account, or refreshes its metadata.
//
// The user_id is part of the conflict target's WHERE clause rather than being
// blindly overwritten: a device id belongs to whoever first claimed it, so a
// second account cannot take over another's sync cursor by guessing an id.
func (d *DB) UpsertDevice(userID, deviceID, name, platform string) error {
	if deviceID == "" {
		return nil
	}
	now := time.Now().Unix()
	_, err := d.Exec(`
		INSERT INTO devices (id, user_id, name, platform, created_at, last_seen_at, last_seq)
		VALUES (?, ?, ?, ?, ?, ?, 0)
		ON CONFLICT (id) DO UPDATE SET
			name         = excluded.name,
			platform     = excluded.platform,
			last_seen_at = excluded.last_seen_at
		WHERE devices.user_id = excluded.user_id`,
		deviceID, userID, name, platform, now, now)
	return err
}

// DevicesForUser lists an account's devices, most recently seen first.
func (d *DB) DevicesForUser(userID string) ([]Device, error) {
	rows, err := d.Query(`
		SELECT id, user_id, name, platform, created_at, last_seen_at, last_seq
		FROM devices WHERE user_id = ? ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Device
	for rows.Next() {
		var dev Device
		if err := rows.Scan(&dev.ID, &dev.UserID, &dev.Name, &dev.Platform,
			&dev.CreatedAt, &dev.LastSeenAt, &dev.LastSeq); err != nil {
			return nil, err
		}
		out = append(out, dev)
	}
	return out, rows.Err()
}

// DeleteDevice forgets a device. Its sync cursor goes with it, so if that
// browser comes back it re-bootstraps from a snapshot.
func (d *DB) DeleteDevice(userID, deviceID string) error {
	res, err := d.Exec(`DELETE FROM devices WHERE id = ? AND user_id = ?`, deviceID, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
