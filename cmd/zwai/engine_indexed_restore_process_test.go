//go:build !windows && !race

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/lease"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// The first upgrade processes old generation metadata. A later actual
// process must serve the same long library without loading closed histories.
func TestIndexedEngineStartupScalesWithPendingConversations(t *testing.T) {
	if testing.Short() {
		t.Skip("large on-disk migration and real engine processes")
	}
	dir := t.TempDir()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Product.Mode = config.ProductModeLocal
	cfg.Swarm.AutoTitle = false
	cfg.Memory.Enabled = false
	cfg.Memory.AutoReview = false
	cfg.Remote.Enabled = false
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	seedIndexedStartupLibrary(t, cfg.DBPath())

	first, _, stop := bootIndexedStartup(t, dir, "", 45*time.Second)
	stop()
	if first.Backfill != 317 || first.Progress.Total != 0 || first.HistoryReads != 0 || len(first.Unexpected) != 0 {
		t.Fatalf("migration did not isolate historical metadata: %+v", first)
	}
	t.Logf("317 legacy conversations, 1,207,770 events: first upgrade after listener %d ms", first.ReadyMS)
	for _, n := range []int{0, 10, 12} {
		t.Run(fmt.Sprintf("pending-%d", n), func(t *testing.T) {
			var work []store.Turn
			var ids []string
			if n > 0 {
				work, ids = seedIndexedStartupWork(t, cfg.DBPath(), n)
			}
			receipt, rec, stop := bootIndexedStartup(t, dir, strings.Join(ids, " "), 20*time.Second)
			defer stop()
			if receipt.Backfill != 0 || len(receipt.Unexpected) != 0 || receipt.Progress.Total != n ||
				receipt.Progress.Completed != n || receipt.Progress.Failed != 0 {
				t.Fatalf("startup read closed history or lost work: %+v", receipt)
			}
			if n == 0 && (receipt.ReadyMS > 1000 || receipt.HistoryReads != 0) {
				t.Fatalf("empty startup missed the 1-second/zero-history target: %+v", receipt)
			}
			if n == 10 && receipt.ReadyMS > 10000 {
				t.Fatalf("10 pending conversations missed the 10-second target: %+v", receipt)
			}
			for _, turn := range work {
				waitRestartCondition(t, 15*time.Second, "original turn finished", func() bool {
					observed := restartTrace(t, rec.URL, turn.ID)
					return observed.Turn.ID == turn.ID && observed.Turn.Status == store.TurnDone
				})
			}
			t.Logf("pending=%d listener to ready=%d ms history reads=%d closed-history reads=%d", n, receipt.ReadyMS, receipt.HistoryReads, len(receipt.Unexpected))
		})
	}
}

func seedIndexedStartupLibrary(t *testing.T, path string) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.DB().Exec("DROP TRIGGER startup_recovery_event_added").Error; err != nil {
		t.Fatal(err)
	}
	for i := range 317 {
		thread := &store.Thread{ID: fmt.Sprintf("th_indexed_%03d", i), Title: fmt.Sprintf("Retained conversation %d", i)}
		if err := st.CreateThread(thread); err != nil {
			t.Fatal(err)
		}
		turn := &store.Turn{ThreadID: thread.ID, UserText: "historical request"}
		if err := st.CreateTurn(turn); err != nil {
			t.Fatal(err)
		}
		if err := st.FinishTurn(turn.ID, store.TurnDone, "historical result", ""); err != nil {
			t.Fatal(err)
		}
		// Generation pairs represent ordinary completed model calls. Large
		// tool bodies make an accidental snapshot read expensive and visible.
		err := st.DB().Exec(`WITH RECURSIVE seq(n) AS (
			VALUES(1) UNION ALL SELECT n+1 FROM seq WHERE n<3810
		) INSERT INTO events(thread_id,turn_id,seq,kind,message_id,text)
		SELECT ?,?,n,CASE n%5 WHEN 1 THEN 'generation_started' WHEN 2 THEN 'generation_completed'
			WHEN 3 THEN 'agent_message' WHEN 4 THEN 'tool_result' ELSE 'user_message' END,
			'old-' || ((n-1)/5),CASE WHEN n%5 IN (3,4) THEN hex(zeroblob(512)) ELSE '' END FROM seq`, thread.ID, turn.ID).Error
		if err != nil {
			t.Fatal(err)
		}
	}
}

func seedIndexedStartupWork(t *testing.T, path string, n int) ([]store.Turn, []string) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var work []store.Turn
	var ids []string
	for i := range n {
		turn := store.Turn{ThreadID: fmt.Sprintf("th_indexed_%03d", i), UserText: "Finish this task."}
		if err := st.CreateTurn(&turn); err != nil {
			t.Fatal(err)
		}
		if err := st.AppendEvent(&store.Event{ThreadID: turn.ThreadID, TurnID: turn.ID, Kind: "user_message", Text: turn.UserText}); err != nil {
			t.Fatal(err)
		}
		work = append(work, turn)
		ids = append(ids, turn.ThreadID)
		turns, err := st.ListTurns(turn.ThreadID)
		if err != nil {
			t.Fatal(err)
		}
		for _, original := range turns {
			ids = append(ids, original.ID)
		}
	}
	return work, ids
}

func bootIndexedStartup(t *testing.T, dir, work string, deadline time.Duration) (indexedStartupEvidence, lease.Record, func()) {
	t.Helper()
	attempt := t.TempDir()
	probe := filepath.Join(attempt, "ready.json")
	log, err := os.OpenFile(filepath.Join(attempt, "child.log"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "engine", "--data-dir", dir, "--mock")
	cmd.Env = append(os.Environ(), "ZWAI_ENGINE_RESTORE_HELPER=1", "ZWAI_INDEXED_RESTORE_PROBE="+probe,
		"ZWAI_INDEXED_RESTORE_WORK="+work, "ZWAI_RESTORE_DELAY=")
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	done := make(chan struct{})
	var exitErr error
	go func() { exitErr = cmd.Wait(); close(done) }()
	stopped := false
	stop := func() {
		t.Helper()
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("indexed engine did not shut down")
		}
		_ = log.Close()
		if exitErr != nil {
			raw, _ := os.ReadFile(log.Name())
			t.Errorf("indexed engine exit %v: %s", exitErr, raw)
		}
	}
	t.Cleanup(stop)
	var receipt indexedStartupEvidence
	var record lease.Record
	waitRestartCondition(t, deadline, "indexed engine ready receipt", func() bool {
		select {
		case <-done:
			raw, _ := os.ReadFile(log.Name())
			t.Fatalf("engine exited before readiness (%v): %s", exitErr, raw)
		default:
		}
		raw, err := os.ReadFile(probe)
		if err != nil || json.Unmarshal(raw, &receipt) != nil {
			return false
		}
		record, err = lease.Discover(dir, true)
		return err == nil && record.PID == cmd.Process.Pid && record.Startup == "ready"
	})
	return receipt, record, stop
}
