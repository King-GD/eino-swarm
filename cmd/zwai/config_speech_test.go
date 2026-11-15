package main

import (
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func TestConfigShowRedactsBothSpeechCredentials(t *testing.T) {
	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = cfg.Update(func(next *config.Config) error {
		next.Speech.Input.APIKey = "private-asr-key"
		next.Speech.Output.APIKey = "private-tts-key"
		next.Speech.Input.Model = "recognizer"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := runConfig([]string{"show", "--data-dir", cfg.DataDir()}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "private-asr-key") || strings.Contains(out, "private-tts-key") || strings.Count(out, "api_key: <set, hidden>") < 2 || !strings.Contains(out, "recognizer") {
		t.Fatal("config show did not redact voice credentials while keeping useful configuration")
	}
}
