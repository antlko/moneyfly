package api

import (
	"log/slog"

	"github.com/gofiber/fiber/v3"

	"moneyfly/internal/db"
	syncproto "moneyfly/internal/sync"
)

// Taking data back out: undoing one CSV import, or erasing records wholesale.
//
// Both are tombstones written through the ordinary op path — never a DELETE —
// so every other device learns about them the same way it learns about any
// delete (docs/SYNC.md §2 "Deletes").
//
// Two things differ from a delete made on a device, both deliberately:
//
//   - The tombstone carries an empty body. A device's own delete keeps the
//     row's body, natural key included, and the importer and recurring worker
//     both treat a deleted natural key as "this was removed on purpose — do
//     not bring it back". That is right for one record deleted by hand and
//     exactly wrong here: the reason to undo an import is to run it again, and
//     with the keys kept every row of the second run would be skipped as
//     "already imported". Dropping the body is what makes erase mean *forget*.
//
//   - The lamport is the account's high-water mark plus one, not the row's
//     own plus one the recurring worker uses. That worker writes on someone's
//     behalf and has to lose to them; this is the person's own explicit
//     action, so it takes the place a device's delete would — after every
//     change the server has seen.

// eraseScopes is what each erase scope removes, in the order it is written:
// transactions before the accounts and categories they point at, so a device
// that syncs half way through never shows records under a missing category.
// user_setting is never included — base currency and the declared currency
// list are preferences, not data, and the app cannot render without them.
var eraseScopes = map[string][]string{
	"records":    {"txn"},
	"everything": {"txn", "recurring_rule", "budget", "account", "category"},
}

type eraseRequest struct {
	Scope string `json:"scope"`
}

// eraseResponse reports what was removed. Like the import commit, a failure
// half way through is a 200 carrying the damage, because the earlier chunks
// are committed and an {"error": …} body would throw that count away.
// Re-running is safe: rows already tombstoned are no longer live.
type eraseResponse struct {
	Deleted       int    `json:"deleted"`
	Failed        int    `json:"failed"`
	FailureReason string `json:"failureReason,omitempty"`
}

// tombstoneOps builds one delete per row, each at a lamport that beats both
// the row itself and everything else the account has seen.
func tombstoneOps(entity string, rows []db.RowVersion, highWater int64) []syncproto.Op {
	ops := make([]syncproto.Op, 0, len(rows))
	for _, r := range rows {
		lamport := max(highWater, r.Lamport) + 1
		ops = append(ops, syncproto.Op{
			Entity:   entity,
			ID:       r.ID,
			Lamport:  lamport,
			DeviceID: serverDeviceID,
			Deleted:  true,
			Data:     []byte(emptyBody),
		})
	}
	return ops
}

const emptyBody = "{}"

// applyTombstones writes ops and tells the user's devices, returning what
// landed alongside any error — see applyInChunks.
func (s *Server) applyTombstones(c fiber.Ctx, userID string, ops []syncproto.Op) eraseResponse {
	accepted, lastSeq, err := applyInChunks(
		func(chunk []syncproto.Op) (db.ApplyResult, error) {
			return s.conn().ApplyOps(userID, chunk)
		}, ops)
	if accepted > 0 {
		s.events.publish(userID, syncEvent{Seq: lastSeq, DeviceID: serverDeviceID})
	}
	out := eraseResponse{Deleted: accepted}
	if err != nil {
		out.Failed = len(ops) - accepted
		out.FailureReason = err.Error()
		slog.ErrorContext(c.Context(), "erase: applying tombstones",
			"user", userID, "accepted", accepted, "failed", out.Failed, "error", err)
	}
	return out
}

// handleErase removes every live row in the requested scope.
func (s *Server) handleErase(c fiber.Ctx) error {
	var in eraseRequest
	if err := decode(c, &in); err != nil {
		return err
	}
	entities, ok := eraseScopes[in.Scope]
	if !ok {
		return fiber.NewError(fiber.StatusBadRequest, `scope must be "records" or "everything"`)
	}

	user := userLocal(c)
	highWater, err := s.conn().SyncLamport(user.ID)
	if err != nil {
		return err
	}
	var ops []syncproto.Op
	for _, entity := range entities {
		rows, err := s.conn().LiveRows(user.ID, entity)
		if err != nil {
			return err
		}
		ops = append(ops, tombstoneOps(entity, rows, highWater)...)
	}

	out := s.applyTombstones(c, user.ID, ops)
	slog.InfoContext(c.Context(), "erase", "user", user.ID, "scope", in.Scope, "deleted", out.Deleted)
	return c.JSON(out)
}

type importBatchDTO struct {
	ID        string `json:"id"`
	FileName  string `json:"fileName"`
	CreatedAt int64  `json:"createdAt"`
	Rows      int    `json:"rows"`
	// Legacy marks the transactions imported before imports were tracked
	// one by one; they can only be removed together.
	Legacy bool `json:"legacy,omitempty"`
}

// handleListImports lists the imports that can still be undone, newest first.
func (s *Server) handleListImports(c fiber.Ctx) error {
	batches, err := s.conn().ImportBatches(userLocal(c).ID)
	if err != nil {
		return err
	}
	out := make([]importBatchDTO, 0, len(batches))
	for _, b := range batches {
		out = append(out, importBatchDTO{
			ID: b.ID, FileName: b.FileName, CreatedAt: b.CreatedAt, Rows: b.Rows,
			Legacy: b.ID == db.LegacyImportID,
		})
	}
	return c.JSON(out)
}

// handleUndoImport removes every transaction one import wrote that is still
// live. Accounts and categories created for it are left alone: they were
// created on a device, by the person, and may already hold other records.
func (s *Server) handleUndoImport(c fiber.Ctx) error {
	user := userLocal(c)
	id := c.Params("id")
	if id != db.LegacyImportID {
		exists, err := s.conn().ImportBatchExists(user.ID, id)
		if err != nil {
			return err
		}
		if !exists {
			return fiber.NewError(fiber.StatusNotFound, "import not found")
		}
	}

	highWater, err := s.conn().SyncLamport(user.ID)
	if err != nil {
		return err
	}
	rows, err := s.conn().ImportBatchRows(user.ID, id)
	if err != nil {
		return err
	}

	out := s.applyTombstones(c, user.ID, tombstoneOps("txn", rows, highWater))
	slog.InfoContext(c.Context(), "import undone", "user", user.ID, "import", id, "deleted", out.Deleted)
	return c.JSON(out)
}
