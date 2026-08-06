package db

import "time"

// Webhook is one URL notified when this user writes a transaction, and the
// secret that signs the payload sent there.
type Webhook struct {
	ID        string
	UserID    string
	Name      string
	URL       string
	Secret    string
	CreatedAt int64
}

// CreateWebhook stores one.
func (d *DB) CreateWebhook(id, userID, name, url, secret string) (Webhook, error) {
	now := time.Now().Unix()
	if _, err := d.Exec(`
		INSERT INTO webhook (id, user_id, name, url, secret, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		id, userID, name, url, secret, now); err != nil {
		return Webhook{}, err
	}
	return Webhook{ID: id, UserID: userID, Name: name, URL: url, Secret: secret, CreatedAt: now}, nil
}

// ListWebhooks returns a user's webhooks, including the secret — unlike
// ListAPITokens, the operator has to be able to see it again to configure the
// receiving end, and unlike a token it is never itself the credential that
// gets this account into anything.
func (d *DB) ListWebhooks(userID string) ([]Webhook, error) {
	rows, err := d.Query(`
		SELECT id, name, url, secret, created_at FROM webhook
		WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := []Webhook{}
	for rows.Next() {
		w := Webhook{UserID: userID}
		if err := rows.Scan(&w.ID, &w.Name, &w.URL, &w.Secret, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// DeleteWebhook removes one, scoped by user id so one account can never
// remove another's.
func (d *DB) DeleteWebhook(userID, id string) error {
	_, err := d.Exec(`DELETE FROM webhook WHERE id = ? AND user_id = ?`, id, userID)
	return err
}
