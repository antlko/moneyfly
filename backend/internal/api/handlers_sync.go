package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"

	"moneyfly/internal/db"
	syncproto "moneyfly/internal/sync"
)

// heartbeatInterval keeps the event stream alive through proxies that close idle
// connections. It is a comment line, which EventSource ignores.
const heartbeatInterval = 25 * time.Second

type pushRequest struct {
	DeviceID string         `json:"deviceId"`
	Ops      []syncproto.Op `json:"ops"`
}

type pushResponse struct {
	Accepted  int                   `json:"accepted"`
	Rejected  []syncproto.Rejection `json:"rejected"`
	ServerSeq int64                 `json:"serverSeq"`
	Lamport   int64                 `json:"lamport"`
}

// handlePush applies a batch of operations.
//
// The user comes from the session, never from the payload — that single line is
// what keeps one account's ops out of another's data, and the (user_id, id)
// primary key makes it structural rather than a check that could be forgotten.
func (s *Server) handlePush(c fiber.Ctx) error {
	var in pushRequest
	if err := decode(c, &in); err != nil {
		return err
	}
	if len(in.Ops) > syncproto.MaxOpsPerPush {
		return fiber.NewError(fiber.StatusRequestEntityTooLarge,
			fmt.Sprintf("at most %d operations per push", syncproto.MaxOpsPerPush))
	}

	user := userLocal(c)
	database := s.conn()

	res, err := database.ApplyOps(user.ID, in.Ops)
	if err != nil {
		return err
	}
	if err := database.TouchDeviceSync(user.ID, in.DeviceID, res.ServerSeq); err != nil {
		return err
	}

	// Only wake the other devices when something actually landed. A push that
	// lost every comparison changes nothing and must not cause a pull storm.
	if res.Accepted > 0 {
		s.events.publish(user.ID, syncEvent{Seq: res.ServerSeq, DeviceID: in.DeviceID})
	}

	return c.JSON(pushResponse{
		Accepted:  res.Accepted,
		Rejected:  res.Rejected,
		ServerSeq: res.ServerSeq,
		Lamport:   res.Lamport,
	})
}

type pullResponse struct {
	Changes   []syncproto.Change `json:"changes"`
	ServerSeq int64              `json:"serverSeq"`
	HasMore   bool               `json:"hasMore"`
}

// handlePull returns the deltas after a cursor.
func (s *Server) handlePull(c fiber.Ctx) error {
	since, err := strconv.ParseInt(c.Query("since", "0"), 10, 64)
	if err != nil || since < 0 {
		return fiber.NewError(fiber.StatusBadRequest, "since must be a non-negative integer")
	}
	limit, _ := strconv.Atoi(c.Query("limit", ""))

	user := userLocal(c)
	changes, serverSeq, hasMore, err := s.conn().PullChanges(user.ID, since, limit)
	if errors.Is(err, db.ErrResyncRequired) {
		// 409, not 400: the request was well-formed, the client's assumption
		// about the world was not. The SPA reacts by re-bootstrapping.
		return fiber.NewError(fiber.StatusConflict, "resync required")
	}
	if err != nil {
		return err
	}
	if err := s.conn().TouchDeviceSync(user.ID, c.Query("deviceId"), serverSeq); err != nil {
		return err
	}
	return c.JSON(pullResponse{Changes: changes, ServerSeq: serverSeq, HasMore: hasMore})
}

type snapshotResponse struct {
	Rows      []syncproto.Change `json:"rows"`
	ServerSeq int64              `json:"serverSeq"`
	Lamport   int64              `json:"lamport"`
}

// handleSnapshot bootstraps a device that has no usable cursor.
func (s *Server) handleSnapshot(c fiber.Ctx) error {
	user := userLocal(c)
	database := s.conn()

	rows, serverSeq, err := database.SnapshotRows(user.ID)
	if err != nil {
		return err
	}
	lamport, err := database.SyncLamport(user.ID)
	if err != nil {
		return err
	}
	return c.JSON(snapshotResponse{Rows: rows, ServerSeq: serverSeq, Lamport: lamport})
}

// handleEvents streams "something changed" notifications over SSE.
//
// SSE rather than a WebSocket because the need is one-directional, EventSource
// reconnects by itself, and it survives any reverse proxy. Writes go out through
// the normal push endpoint.
func (s *Server) handleEvents(c fiber.Ctx) error {
	user := userLocal(c)
	ch, unsubscribe := s.events.subscribe(user.ID)

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-cache, no-transform")
	c.Set("Connection", "keep-alive")
	// nginx buffers proxied responses by default, which would hold events until
	// the connection closed — i.e. defeat the entire endpoint.
	c.Set("X-Accel-Buffering", "no")

	return c.SendStreamWriter(func(w *bufio.Writer) {
		defer unsubscribe()

		// Tell the client how long to wait before reconnecting, and prove the
		// stream is open so it can stop showing "connecting".
		if _, err := fmt.Fprintf(w, "retry: 5000\n\nevent: ready\ndata: {}\n\n"); err != nil {
			return
		}
		if err := w.Flush(); err != nil {
			return
		}

		heartbeat := time.NewTicker(heartbeatInterval)
		defer heartbeat.Stop()

		for {
			select {
			case <-s.stop:
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				payload, err := json.Marshal(ev)
				if err != nil {
					return
				}
				if _, err := fmt.Fprintf(w, "event: changed\ndata: %s\n\n", payload); err != nil {
					return
				}
				if err := w.Flush(); err != nil {
					return // the client hung up
				}
			case <-heartbeat.C:
				if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
					return
				}
				if err := w.Flush(); err != nil {
					return
				}
			}
		}
	})
}
