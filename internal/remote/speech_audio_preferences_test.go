package remote

import (
	"github.com/LubyRuffy/eino-swarm/internal/audioparams"
	"github.com/LubyRuffy/eino-swarm/internal/config"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGatewayPreferencesDoNotInvalidateUnchangedLocalPhoneSpeech(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(audioWAV())
	}))
	defer up.Close()
	e := localVoiceEngine(t, up.URL+"/v1")
	voice, speed := "saved", 8.0
	if err := e.Config().Update(func(c *config.Config) error {
		c.Speech.GatewayParameters = map[string]audioparams.Overrides{"audio:gateway": {Voice: &voice, Speed: &speed}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p, replies := catalogPump(t, e)
	before := p.audioRevision()
	if p.audioRevision() != before {
		t.Fatal("unchanged explicit preferences changed the private configuration revision")
	}
	sendVoice(p, Request{ID: "speak", Op: OpAudioSpeech, AudioID: "local-with-gateway-preference", Text: "hello"})
	if response := voiceReply(t, replies, "speak"); !response.OK || response.Audio == nil || response.Audio.Size != len(audioWAV()) {
		t.Fatalf("local voice cancelled by a detached snapshot: %+v", response)
	}
	if err := e.Config().Update(func(c *config.Config) error { c.Speech.Output.APIKey = "replacement-key"; return nil }); err != nil {
		t.Fatal(err)
	}
	if p.audioRevision() == before {
		t.Fatal("private credential change did not revoke old audio")
	}
}
