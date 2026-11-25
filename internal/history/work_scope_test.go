package history

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkToolPayloadsStayInTheirOwningHistory(t *testing.T) {
	path, db := projectScopeFixture(t)
	for _, sql := range []string{
		`ALTER TABLE events ADD work_record_payload INTEGER DEFAULT 0`,
		`ALTER TABLE messages ADD work_record_payload INTEGER DEFAULT 0`,
		`ALTER TABLE messages ADD role TEXT`,
		`ALTER TABLE messages ADD tool_calls TEXT`,
		`INSERT INTO events VALUES ('private-call','archived-thread','tool_call','private-work-call',1),
			('private-result','archived-thread','tool_result','private-work-result',1),
			('foreign-private','foreign-thread','tool_result','foreign-work-result',1)`,
		`INSERT INTO messages VALUES ('mixed','archived-thread','ordinary assistant words',1,'assistant','private-work-arguments'),
			('private-result','archived-thread','private-work-result',1,'tool',NULL),
			('ordinary-tool','archived-thread','ordinary tool result',0,'tool',NULL)`,
	} {
		if _, err := db.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name       string
		reader     *Reader
		canSeeWork bool
	}{
		{"owner", NewProject(path, "current").ForThread("archived-thread"), true},
		{"project peer", NewProject(path, "current").ForThread("current-thread"), false},
		{"standalone", New(path).ForThread("standalone-thread"), false},
		{"unbound reader", New(path), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			const statement = `SELECT text, NULL AS calls FROM events
				UNION ALL SELECT content, tool_calls FROM messages`
			result, err := tc.reader.Query(t.Context(), statement, nil, 16000)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(result.Rows)
			if err != nil {
				t.Fatal(err)
			}
			payload := string(raw)
			for _, label := range []string{"private-work-call", "private-work-arguments", "private-work-result"} {
				if strings.Contains(payload, label) != tc.canSeeWork {
					t.Fatalf("private marker %q visibility = %t", label, tc.canSeeWork)
				}
			}
			for _, label := range []string{"ordinary assistant words", "ordinary tool result"} {
				if !strings.Contains(payload, label) {
					t.Fatalf("ordinary history %q was removed", label)
				}
			}
			if strings.Contains(payload, "private checkpoint") || strings.Contains(payload, "foreign-work-result") {
				t.Fatal("work projection widened existing private history")
			}
		})
	}
}
