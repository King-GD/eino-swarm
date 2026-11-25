package main

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/app"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"gorm.io/gorm"
)

type indexedStartupEvidence struct {
	ReadyMS      int64                  `json:"ready_ms"`
	Progress     engine.RestoreProgress `json:"progress"`
	Backfill     int                    `json:"backfill_threads"`
	HistoryReads int                    `json:"history_reads"`
	Unexpected   []string               `json:"unexpected_history_queries,omitempty"`
}

// This probe observes real statements executed in the test child from
// listener publication through the committed ready lease.
type indexedStartupProbe struct {
	mu       sync.Mutex
	app      *app.App
	path     string
	allowed  map[string]bool
	evidence indexedStartupEvidence
	frozen   bool
}

func observeIndexedStartup(a *app.App) (*indexedStartupProbe, error) {
	path := os.Getenv("ZWAI_INDEXED_RESTORE_PROBE")
	if path == "" {
		return nil, nil
	}
	p := &indexedStartupProbe{app: a, path: path, allowed: map[string]bool{}}
	for _, scope := range strings.Fields(os.Getenv("ZWAI_INDEXED_RESTORE_WORK")) {
		p.allowed[scope] = true
	}
	cb := a.Store.DB().Callback()
	if err := cb.Query().After("gorm:query").Register("indexed_startup_observation", p.read); err != nil {
		return nil, err
	}
	if err := cb.Row().After("gorm:row").Register("indexed_startup_observation", p.read); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *indexedStartupProbe) read(db *gorm.DB) {
	sql := strings.ToLower(strings.Join(strings.Fields(db.Statement.SQL.String()), " "))
	table := db.Statement.Table
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.frozen || strings.HasPrefix(sql, "with evidence as") {
		return
	}
	if strings.Contains(sql, "from events indexed by idx_evt_thread_kind_seq") && strings.Contains(sql, "json_each") {
		p.evidence.Backfill++
		return
	}
	if table != "events" && table != "turns" && table != "messages" &&
		!strings.Contains(sql, "from events ") && !strings.Contains(sql, "from turns ") && !strings.Contains(sql, "from messages ") {
		return
	}
	p.evidence.HistoryReads++
	for _, arg := range db.Statement.Vars {
		if id, ok := arg.(string); ok && p.allowed[id] {
			return
		}
	}
	p.evidence.Unexpected = append(p.evidence.Unexpected, sql)
}

func (p *indexedStartupProbe) finish(elapsed time.Duration) error {
	p.mu.Lock()
	p.frozen = true
	p.evidence.ReadyMS = elapsed.Milliseconds()
	_, p.evidence.Progress = p.app.Engine.StartupStatus()
	body, err := json.Marshal(p.evidence)
	p.mu.Unlock()
	if err != nil {
		return err
	}
	return os.WriteFile(p.path, body, 0o600)
}
