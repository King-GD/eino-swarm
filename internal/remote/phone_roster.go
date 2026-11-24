package remote

import (
	"encoding/json"
	"sort"
	"strings"

	swarm "github.com/LubyRuffy/eino-swarm"
	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// attachRunningRoster adds spawned identities for workers the phone would
// still show as running after folding this page. Their rows stay out of
// Events: the history cursor is the contiguous page, and a spawned seq from
// an earlier turn would make Earlier skip everything after it.
func attachRunningRoster(st *store.Store, resp Response, cfg config.RemoteConfig) Response {
	if st == nil || !resp.OK || strings.TrimSpace(resp.ThreadID) == "" {
		return resp
	}
	rows, err := st.ListRosterIdentities(resp.ThreadID)
	if err != nil {
		return resp
	}
	have := make(map[int64]struct{}, len(resp.Events))
	for _, ev := range resp.Events {
		if ev.Seq > 0 {
			have[ev.Seq] = struct{}{}
		}
	}
	roster := make([]EventView, 0)
	for _, ev := range runningWorkerSpawns(rows) {
		if _, ok := have[ev.Seq]; ok {
			continue
		}
		roster = append(roster, eventView(ev, cfg))
	}
	resp.Roster = &roster
	return fitRoster(resp)
}

// runningWorkerSpawns is the phone's running set: spawned, then finished or
// cleanup. A later error does not retire a worker there, so the sidecar must
// not either; Earlier would otherwise change the count.
func runningWorkerSpawns(rows []store.Event) []store.Event {
	latest := make(map[string]store.Event)
	running := make(map[string]bool)
	var order []string
	for _, ev := range rows {
		id := strings.TrimSpace(ev.AgentID)
		switch ev.Kind {
		case "spawned":
			if id == "" || id == swarm.DefaultManagerID {
				continue
			}
			if _, seen := latest[id]; !seen {
				order = append(order, id)
			}
			latest[id] = ev
			running[id] = true
		case "finished":
			if id == "" || id == swarm.DefaultManagerID {
				continue
			}
			running[id] = false
		case "cleanup":
			for id := range running {
				running[id] = false
			}
		}
	}
	out := make([]store.Event, 0, len(order))
	for _, id := range order {
		if running[id] {
			out = append(out, latest[id])
		}
	}
	// Oldest spawn first, so a frame that cannot carry every identity keeps
	// the workers that started most recently.
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

func fitRoster(resp Response) Response {
	if resp.Roster == nil {
		return resp
	}
	for {
		// A frame that cannot be measured is left unchanged. The sender has
		// to marshal the same value, so dropping rows here would not make
		// the open succeed.
		raw, err := json.Marshal(resp)
		if err != nil || len(raw) <= plaintextBudget() || len(*resp.Roster) == 0 {
			return resp
		}
		next := (*resp.Roster)[1:]
		resp.Roster = &next
	}
}
