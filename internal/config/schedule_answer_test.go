package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScheduleAnswerCheckSettingsLoadAndRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, mode string
		enabled          bool
	}{
		{"legacy stays enabled on chat", "swarm: {}\n", ScheduleAnswerChat, true},
		{"automatic", "swarm:\n  schedule_answer_model: ' AUTO '\n", ScheduleAnswerAuto, true},
		{"explicit decision while disabled", "swarm:\n  schedule_answer_check: false\n  schedule_answer_model: decision\n", ScheduleAnswerDecision, false},
		{"unknown model uses default", "swarm:\n  schedule_answer_model: unrecognized\n", ScheduleAnswerChat, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, FileName), []byte(tc.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if err := cfg.Save(); err != nil {
				t.Fatal(err)
			}
			reloaded, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, current := range []*Config{cfg, reloaded} {
				if current.Swarm.ScheduleAnswerCheck != tc.enabled || current.Swarm.ScheduleAnswerModel != tc.mode {
					t.Fatalf("settings = %t/%q, want %t/%q", current.Swarm.ScheduleAnswerCheck, current.Swarm.ScheduleAnswerModel, tc.enabled, tc.mode)
				}
			}
		})
	}
}
