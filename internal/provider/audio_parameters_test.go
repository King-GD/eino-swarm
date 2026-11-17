package provider

import (
	"encoding/json"
	"github.com/LubyRuffy/eino-swarm/internal/config"
	"strings"
	"testing"
)

func TestAudioParameterProtocolSurvivesCapabilityRoundTrip(t *testing.T) {
	raw := `{"data":{"version":1,"model_roles_version":3,"sources":{"preset":{"authorized":true,"available":true,"configurable":false,"capabilities":{"speech":{"supported":true,"available":true,"configurable":false,"endpoints":["audio_speech"],"audio_parameters_version":1}}},"gateway":{"authorized":false,"available":false,"configurable":false,"capabilities":{}}}}}`
	caps, err := ParseCapabilities([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}
	// A locked model must still be able to advertise independently editable audio parameters.
	if !strings.Contains(string(saved), `"audio_parameters_version":1`) {
		t.Fatalf("lost negotiated audio protocol: %s", saved)
	}
}

func TestAudioParametersBindAuthorityModelAndRevisionWithoutSharingMutableData(t *testing.T) {
	cfg := config.Default()
	pool := New(cfg)
	if pool.AudioParameters("personal", "voice", "speech") != nil {
		t.Fatal("personal provider borrowed gateway declaration")
	}
	source := accountSourceFor(t, "https://gateway.invalid/v1", "private")
	source.SetAccountOwner("one")
	caps := roleManifest("speech", "audio_speech", false)
	role := caps.Capabilities["speech"]
	role.AudioParametersVersion = 1
	caps.Capabilities["speech"] = role
	source.SetCapabilities(caps)
	source.SetAudioParameters([]byte(`{"data":[{"id":"voice","model_type":"speech","audio":{"version":1,"revision":"r1","status":"configured","parameters":{"voice":{"values":["a"],"default":"a","configurable":true}}}}]}`))
	pool.SetAccountSource(source)
	a := pool.AudioParameters(source.ID(), "voice", "speech")
	if a == nil || a.Status != "configured" || a.Scope == "" {
		t.Fatalf("%+v", a)
	}
	a.Parameters.Voice.Values[0] = "corrupted"
	if pool.AudioParameters(source.ID(), "voice", "speech").Parameters.Voice.Values[0] != "a" {
		t.Fatal("mutated published catalog")
	}
	if pool.AudioParameters(source.ID(), "missing", "speech").Status != "unavailable" {
		t.Fatal("invented missing model options")
	}
	copy := *source
	copy.SetAccountOwner("two")
	pool.SetAccountSource(&copy)
	if pool.AudioParameters(source.ID(), "voice", "speech").Scope == a.Scope {
		t.Fatal("account preference leaked")
	}
	copy.SetCapabilities(nil)
	pool.SetAccountSource(&copy)
	if pool.AudioParameters(source.ID(), "voice", "speech").Status != "unsupported" {
		t.Fatal("missing manifest enabled controls")
	}
	var calls int
	pool.SetCapabilitiesTrigger(func() { calls++ })
	pool.NotifyAudioParametersChanged()
	if calls != 1 {
		t.Fatal("refresh not triggered")
	}
	pool.SetCapabilitiesTrigger(nil)
	pool.NotifyAudioParametersChanged()
	if source.equal(&copy) {
		t.Fatal("changed metadata not published")
	}
	copy = *source
	copy.SetAudioParameters(nil)
	if source.equal(&copy) {
		t.Fatal("audio-only changes ignored")
	}
}
