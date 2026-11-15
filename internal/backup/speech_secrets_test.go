package backup

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

// Voice endpoints carry the same private credentials as chat providers. A
// credentials-free export must not accidentally include the newly added keys.
func TestCredentialFreeBackupOmitsVoiceKeysAndKeepsOnlyMatchingDestinationKeys(t *testing.T) {
	src := seed(t)
	for _, endpoint := range []*config.SpeechEndpoint{&src.cfg.Speech.Input.SpeechEndpoint, &src.cfg.Speech.Output.SpeechEndpoint} {
		endpoint.Source, endpoint.BaseURL, endpoint.APIKey = config.SpeechSourceCustom, "https://voice.invalid/v1", "source-voice-secret"
	}
	if err := src.cfg.Save(); err != nil {
		t.Fatal(err)
	}
	db, err := src.st.DB().DB()
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "without-keys.zwai-backup")
	if _, err := Create(context.Background(), Options{DataDir: src.dir, DB: db, Kind: KindFull, Output: archive, OmitSecrets: true}); err != nil {
		t.Fatal(err)
	}
	if archiveHas(t, archive, "source-voice-secret") {
		t.Fatal("voice API keys leaked into a credentials-free backup")
	}
	for _, sameEndpoint := range []bool{true, false} {
		t.Run(map[bool]string{true: "same-endpoint", false: "different-endpoint"}[sameEndpoint], func(t *testing.T) {
			destination, err := config.Load(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for _, endpoint := range []*config.SpeechEndpoint{&destination.Speech.Input.SpeechEndpoint, &destination.Speech.Output.SpeechEndpoint} {
				endpoint.Source, endpoint.BaseURL, endpoint.APIKey = config.SpeechSourceCustom, "https://voice.invalid/v1", "destination-voice-secret"
				if !sameEndpoint {
					endpoint.BaseURL = "https://other.invalid/v1"
				}
			}
			if err := destination.Save(); err != nil {
				t.Fatal(err)
			}
			if _, err := Restore(context.Background(), RestoreOptions{DataDir: destination.DataDir(), Path: archive, Confirm: true, Replace: true}); err != nil {
				t.Fatal(err)
			}
			got, err := config.Load(destination.DataDir())
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if sameEndpoint {
				want = "destination-voice-secret"
			}
			if got.Speech.Input.APIKey != want || got.Speech.Output.APIKey != want {
				t.Fatal("voice key was lost or copied to a different service")
			}
		})
	}
}
