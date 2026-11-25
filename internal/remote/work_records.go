package remote

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/LubyRuffy/eino-swarm/internal/engine"
)

func handleThreadWork(eng *engine.Engine, req Request, path, sessionID string) Response {
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		return fail(req.ID, path, sessionID, "bad_request", "thread_id required")
	}
	if !eng.Config().SwarmSnapshot().WorkLedger {
		return fail(req.ID, path, sessionID, "work_disabled", "Work records are disabled.")
	}
	if err := eng.RejectIfBackup(); err != nil {
		return mapErr(req.ID, path, sessionID, err)
	}
	generation, err := eng.DataGeneration()
	if err != nil {
		return mapErr(req.ID, path, sessionID, err)
	}
	snapshot, err := eng.Store().WorkSnapshot(context.Background(), threadID)
	if err != nil {
		return mapErr(req.ID, path, sessionID, err)
	}
	if err := eng.RejectIfBackup(); err != nil {
		return mapErr(req.ID, path, sessionID, err)
	}
	current, err := eng.DataGeneration()
	if err != nil {
		return mapErr(req.ID, path, sessionID, err)
	}
	if current != generation {
		return fail(req.ID, path, sessionID, "work_conflict", "PC data changed while reading work records.")
	}
	if !eng.Config().SwarmSnapshot().WorkLedger {
		return fail(req.ID, path, sessionID, "work_disabled", "Work records are disabled.")
	}
	reply := okBase(req.ID, path, sessionID)
	reply.DataGeneration = &generation
	reply.WorkSnapshot = &snapshot
	raw, err := json.Marshal(reply)
	if err != nil {
		return mapErr(req.ID, path, sessionID, err)
	}
	if len(raw) > plaintextBudget() {
		return fail(req.ID, path, sessionID, "work_too_large", "This work graph is too large for the paired connection.")
	}
	return reply
}
