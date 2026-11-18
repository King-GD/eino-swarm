package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

func TestNativeFixtureAdvertisesOnlyGrantedModelEndpoints(t *testing.T) {
	for _, tc := range []struct {
		name        string
		api, speech bool
	}{
		{name: "ordinary", api: true},
		{name: "streaming speech", api: true, speech: true},
		{name: "API disabled", speech: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, server, token := webFixture(t)
			f.api, f.speechStreaming = tc.api, tc.speech
			var boot struct {
				Data struct {
					CapabilitiesURL string `json:"capabilities_url"`
					Models          struct {
						SourceID string            `json:"source_id"`
						Defaults map[string]string `json:"defaults"`
					} `json:"models"`
				} `json:"data"`
			}
			raw := webRequest(t, server, clientPrefix+"/bootstrap", token, "", http.StatusOK)
			if err := json.Unmarshal([]byte(raw), &boot); err != nil {
				t.Fatal(err)
			}
			if boot.Data.CapabilitiesURL != server.URL+modelCapabilitiesPath || boot.Data.Models.SourceID != fixtureModelSourceID {
				t.Fatalf("bootstrap did not identify the paired model source and manifest: %+v", boot.Data)
			}
			if (boot.Data.Models.Defaults["speech"] == "speech") != tc.speech {
				t.Fatalf("wrong speech default: %+v", boot.Data.Models.Defaults)
			}
			raw = webRequest(t, server, modelCapabilitiesPath, token, "", http.StatusOK)
			caps, err := provider.ParseCapabilities([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			preset, gateway := caps.Sources["preset"], caps.Sources["gateway"]
			if preset.Allows("conversation") != tc.api || preset.Allows("speech") != (tc.api && tc.speech) ||
				preset.Allows("image") || preset.Allows("transcription") || gateway.Authorized || len(gateway.Capabilities) != 0 {
				t.Fatalf("unexpected fixture role admission: %+v, %+v", preset, gateway)
			}
			if !slices.Equal(preset.Capabilities["conversation"].Endpoints, []string{"chat_completions"}) {
				t.Fatalf("wrong conversation endpoint: %+v", preset.Capabilities)
			}
			if tc.speech && !slices.Equal(preset.Capabilities["speech"].Endpoints, []string{"audio_speech"}) {
				t.Fatalf("wrong speech endpoint: %+v", preset.Capabilities)
			}
			webRequest(t, server, modelCapabilitiesPath, "", "", http.StatusUnauthorized)
			webRequest(t, server, modelCapabilitiesPath, token, "{}", http.StatusMethodNotAllowed)
		})
	}
}
