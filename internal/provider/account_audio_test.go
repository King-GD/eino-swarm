package provider

import (
	"errors"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

func TestAudioUsesOnlyItsEnabledAccountRole(t *testing.T) {
	p := New(config.Default())
	if _, _, err := p.AccountAudio("speech"); !errors.Is(err, ErrAccountLoginRequired) {
		t.Fatalf("signed out: %v", err)
	}
	src := accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "private-token")
	p.SetAccountSource(src)
	if _, _, err := p.AccountAudio("speech"); !errors.Is(err, ErrAccountAudioUnavailable) {
		t.Fatalf("legacy server: %v", err)
	}
	src = accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "private-token")
	src.SetRoles([]AccountRole{{ID: "speech", Enabled: true, Label: "Voice"}, {ID: "transcription", Enabled: false}})
	p.SetAccountSource(src)
	prov, label, err := p.AccountAudio("speech")
	if err != nil || prov.Model != "speech" || prov.APIKey != "private-token" || label != "Voice" {
		t.Fatalf("target=%+v label=%q err=%v", prov, label, err)
	}
	for _, role := range []string{"conversation", "transcription"} {
		if _, _, err := p.AccountAudio(role); !errors.Is(err, ErrAccountAudioUnavailable) {
			t.Fatalf("%s: %v", role, err)
		}
	}
	src = accountSourceFor(t, "https://gateway.invalid/client/desktop/v1", "private-token")
	src.SetRoles([]AccountRole{{ID: "speech", Enabled: true}})
	p.SetAccountSource(src)
	_, label, err = p.AccountAudio("speech")
	if err != nil || label != "speech" {
		t.Fatalf("unlabelled role: %q %v", label, err)
	}
}

func TestVoiceCatalogKindsDoNotEnterConversationChoices(t *testing.T) {
	src, err := NewAccountSource(accountTestID, "Server", "https://gateway.invalid/v1", "token", Catalog{Names: []string{"asr", "tts", "chat"}, Types: map[string]string{"asr": "transcription", "tts": "speech", "chat": "chat"}})
	if err != nil {
		t.Fatal(err)
	}
	kinds := src.Kinds()
	if kinds.Transcription != 1 || kinds.Speech != 1 || len(kinds.TranscriptionModels) != 1 || kinds.TranscriptionModels[0] != "asr" || len(kinds.SpeechModels) != 1 || kinds.SpeechModels[0] != "tts" {
		t.Fatalf("kinds=%+v", kinds)
	}
	names := src.conversationalNames()
	if len(names) != 1 || names[0] != "chat" {
		t.Fatalf("voice became a chat choice: %v", names)
	}
}
