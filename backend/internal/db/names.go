package db

import "fmt"

// NameKind is an existing category or account's id, name and — for a
// category only — kind. The importer's resolver matches CSV names against
// these; nothing else in the server currently needs to read a synced row's
// semantic content rather than just store and forward it.
type NameKind struct {
	ID   string
	Name string
	Kind string
}

// CategoryNames lists every live category for a user.
func (d *DB) CategoryNames(userID string) ([]NameKind, error) {
	rows, err := d.Query(
		`SELECT id, name, kind FROM category WHERE user_id = ? AND deleted = 0`, userID)
	if err != nil {
		return nil, fmt.Errorf("db: reading category names: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []NameKind{}
	for rows.Next() {
		var n NameKind
		if err := rows.Scan(&n.ID, &n.Name, &n.Kind); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// AccountNames lists every live account for a user.
//
// Unlike category, account has no `name` generated column — nothing before
// the importer needed the server to read one back out of an account's JSON
// body — so this reads it with json_extract directly rather than adding a
// column purely for a query that runs once per import, on a handful of rows.
func (d *DB) AccountNames(userID string) ([]NameKind, error) {
	rows, err := d.Query(
		`SELECT id, json_extract(data, '$.name') FROM account WHERE user_id = ? AND deleted = 0`, userID)
	if err != nil {
		return nil, fmt.Errorf("db: reading account names: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []NameKind{}
	for rows.Next() {
		var n NameKind
		if err := rows.Scan(&n.ID, &n.Name); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
