package store

import (
	"fmt"

	"gorm.io/gorm"
)

const startupGenerationKinds = "'generation_started','generation_completed','generation_interrupted','generation_recovered'"
const startupWorkerKinds = "'engine_restart_checkpoint','spawned','finished','cleanup','workers_retained','user_stop_requested','done','error','tool_call','tool_result'"

// A positive hint requests the normal latest-wave replay for a future wake.
// The replay decides whether this historical finish still represents a stop.
const startupWorkerStopHint = `COALESCE(%[1]s.kind='finished' AND (
	INSTR(LOWER(%[1]s.err),'context canceled')>0 OR INSTR(LOWER(%[1]s.err),'context deadline')>0
	OR INSTR(LOWER(%[1]s.err),'the previous process stopped')>0),0)`

// A generation can have an open output and an earlier pending interruption
// with the same message ID. A completion closes the former, independently
// of the latter. The row contains only pointers to original receipts.
type startupRecoveryGeneration struct {
	ThreadID       string
	MessageID      string
	OpenEventID    uint
	PendingEventID uint
}

// Match the interruption schema without decoding tool output or trusting a
// malformed JSON receipt. Use the last matching field, as encoding/json does.
func startupPendingGeneration(body string) string {
	object := "CASE WHEN json_valid(" + body + ") THEN " + body + " ELSE '{}' END"
	return "COALESCE((SELECT value FROM json_each(" + object + ` )
		WHERE lower(key)='recovery' AND type='text' ORDER BY id DESC LIMIT 1)='pending',0)
		AND NOT EXISTS (SELECT 1 FROM json_each(` + object + ` )
		WHERE lower(key) IN ('reason','detail','recovery') AND type NOT IN ('text','null'))`
}

func ensureStartupRecovery(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for _, stmt := range []string{
			`CREATE TABLE IF NOT EXISTS startup_recovery_threads (
				thread_id TEXT PRIMARY KEY, generation_dirty INTEGER NOT NULL DEFAULT 0,
				worker_stop_hint INTEGER NOT NULL DEFAULT 0,
				worker_revision INTEGER NOT NULL DEFAULT 0, no_parked_at INTEGER NOT NULL DEFAULT -1,
				no_wake_at INTEGER NOT NULL DEFAULT -1)`,
			`CREATE TABLE IF NOT EXISTS startup_recovery_generations (
				thread_id TEXT NOT NULL, message_id TEXT NOT NULL, open_event_id INTEGER NOT NULL DEFAULT 0,
				pending_event_id INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(thread_id,message_id))`,
			`CREATE INDEX IF NOT EXISTS idx_startup_generation_dirty ON startup_recovery_threads(thread_id) WHERE generation_dirty=1`,
			`CREATE INDEX IF NOT EXISTS idx_startup_running_turn ON turns(thread_id) WHERE status='running'`,
			`INSERT OR IGNORE INTO startup_recovery_threads(thread_id,generation_dirty) SELECT id,1 FROM threads`,
		} {
			if err := tx.Exec(stmt).Error; err != nil {
				return err
			}
		}
		replaced, err := installStartupRecoveryTriggers(tx)
		if err != nil {
			return err
		}
		if replaced {
			// A schema rebuild in another version may have taken the triggers
			// with it. Existing derived rows then need a fresh proof.
			return tx.Exec(`UPDATE startup_recovery_threads SET generation_dirty=1, no_parked_at=-1, no_wake_at=-1`).Error
		}
		return nil
	})
}

func installStartupRecoveryTriggers(tx *gorm.DB) (bool, error) {
	triggers := map[string]string{
		"thread_added": `AFTER INSERT ON threads BEGIN
			INSERT OR IGNORE INTO startup_recovery_threads(thread_id) VALUES(NEW.id);
		END`,
		"thread_removed": `AFTER DELETE ON threads BEGIN
			DELETE FROM startup_recovery_generations WHERE thread_id=OLD.id;
			DELETE FROM startup_recovery_threads WHERE thread_id=OLD.id;
		END`,
		"turn_added": `AFTER INSERT ON turns BEGIN
			UPDATE startup_recovery_threads SET worker_revision=worker_revision+1 WHERE thread_id=NEW.thread_id;
		END`,
		"turn_removed": `AFTER DELETE ON turns BEGIN
			UPDATE startup_recovery_threads SET worker_revision=worker_revision+1 WHERE thread_id=OLD.thread_id;
		END`,
		"turn_changed": `AFTER UPDATE OF thread_id,seq,status,schedule_continue ON turns
		WHEN OLD.thread_id IS NOT NEW.thread_id OR OLD.seq IS NOT NEW.seq OR OLD.status IS NOT NEW.status
			OR OLD.schedule_continue IS NOT NEW.schedule_continue BEGIN
			UPDATE startup_recovery_threads SET worker_revision=worker_revision+1 WHERE thread_id IN (OLD.thread_id,NEW.thread_id);
		END`,
		"event_added": `AFTER INSERT ON events WHEN NEW.kind IN (` + startupGenerationKinds + "," + startupWorkerKinds + `) BEGIN
			UPDATE startup_recovery_threads SET worker_revision=worker_revision+1,
				worker_stop_hint=(worker_stop_hint OR ` + fmt.Sprintf(startupWorkerStopHint, "NEW") + `)
				WHERE thread_id=NEW.thread_id AND NEW.kind IN (` + startupWorkerKinds + `);
			UPDATE startup_recovery_threads SET generation_dirty=1 WHERE thread_id=NEW.thread_id
				AND NEW.kind IN (` + startupGenerationKinds + `) AND generation_dirty=0
				AND EXISTS(SELECT 1 FROM events WHERE thread_id=NEW.thread_id AND seq>=NEW.seq AND id<>NEW.id);
			INSERT OR IGNORE INTO startup_recovery_generations(thread_id,message_id)
				SELECT NEW.thread_id,COALESCE(NEW.message_id,'')
				WHERE (NEW.kind='generation_started' OR
					(NEW.kind='generation_interrupted' AND (` + startupPendingGeneration("NEW.text") + `)))
					AND EXISTS(SELECT 1 FROM startup_recovery_threads WHERE thread_id=NEW.thread_id AND generation_dirty=0);
			UPDATE startup_recovery_generations SET
				open_event_id=CASE NEW.kind WHEN 'generation_started' THEN NEW.id
					WHEN 'generation_recovered' THEN open_event_id ELSE 0 END,
				pending_event_id=CASE NEW.kind WHEN 'generation_recovered' THEN 0 WHEN 'generation_interrupted'
					THEN CASE WHEN (` + startupPendingGeneration("NEW.text") + `) THEN NEW.id ELSE 0 END
					ELSE pending_event_id END
				WHERE thread_id=NEW.thread_id AND message_id=COALESCE(NEW.message_id,'')
					AND NEW.kind IN (` + startupGenerationKinds + `)
					AND EXISTS(SELECT 1 FROM startup_recovery_threads WHERE thread_id=NEW.thread_id AND generation_dirty=0);
			DELETE FROM startup_recovery_generations WHERE thread_id=NEW.thread_id AND message_id=COALESCE(NEW.message_id,'')
				AND open_event_id=0 AND pending_event_id=0;
		END`,
		"event_removed": `AFTER DELETE ON events WHEN OLD.kind IN (` + startupGenerationKinds + "," + startupWorkerKinds + `) BEGIN
			UPDATE startup_recovery_threads SET worker_revision=worker_revision+(OLD.kind IN (` + startupWorkerKinds + `)),
				generation_dirty=(generation_dirty OR OLD.kind IN (` + startupGenerationKinds + `))
				WHERE thread_id=OLD.thread_id;
		END`,
		"event_changed": `AFTER UPDATE OF thread_id,turn_id,seq,kind,agent_id,message_id,worker_generation,tool_call_id,text,err,role ON events
		WHEN OLD.kind IN (` + startupGenerationKinds + "," + startupWorkerKinds + `) OR NEW.kind IN (` + startupGenerationKinds + "," + startupWorkerKinds + `) BEGIN
			UPDATE startup_recovery_threads SET worker_revision=worker_revision+1,
				worker_stop_hint=(worker_stop_hint OR (thread_id IS NEW.thread_id AND ` + fmt.Sprintf(startupWorkerStopHint, "NEW") + `)),
				generation_dirty=CASE WHEN OLD.kind IN (` + startupGenerationKinds + `) OR NEW.kind IN (` + startupGenerationKinds + `)
					THEN 1 ELSE generation_dirty END
				WHERE thread_id IN (OLD.thread_id,NEW.thread_id);
		END`,
	}
	var installed []string
	if err := tx.Raw("SELECT name FROM sqlite_master WHERE type='trigger' AND name GLOB 'startup_recovery_*'").Scan(&installed).Error; err != nil {
		return false, err
	}
	present := make(map[string]bool, len(installed))
	for _, name := range installed {
		present[name] = true
	}
	missing := false
	for key, body := range triggers {
		name := "startup_recovery_" + key
		if present[name] {
			continue
		}
		missing = true
		if err := tx.Exec("CREATE TRIGGER " + name + " " + body).Error; err != nil {
			return false, fmt.Errorf("store: create %s: %w", name, err)
		}
	}
	return missing, nil
}
