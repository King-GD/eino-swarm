package remote

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/provider"
)

func TestBootstrapModelDefaultsRejectUnknownOrMalformedBindings(t *testing.T) {
	for _, raw := range []string{`[]`, `1`, `{"unknown":"model"}`, `{"title":false}`, `{"title":""}`, `{"title":"\n"}`, `{"title":"` + strings.Repeat("a", 257) + `"}`} {
		if _, err := parseModelDefaults(json.RawMessage(raw)); err == nil {
			t.Fatalf("accepted malformed defaults %s", raw)
		}
	}
	for _, raw := range []string{"", "null", `{"conversation":"chat","speech":"voice"}`} {
		if _, err := parseModelDefaults(json.RawMessage(raw)); err != nil {
			t.Fatal(err)
		}
	}
	yes, no := true, false
	for _, tc := range []struct {
		boot accountBootstrap
		want bool
	}{
		{accountBootstrap{}, true}, {accountBootstrap{profile: &provider.AccountProfile{}}, false},
		{accountBootstrap{roles: []provider.AccountRole{{ID: "conversation"}}}, false},
		{accountBootstrap{editable: &yes}, true}, {accountBootstrap{editable: &no}, false},
	} {
		if tc.boot.configurable() != tc.want {
			t.Fatal("policy was inferred from wrong source")
		}
	}
}

func TestManagedPresetIsBuiltWithoutTheBroaderCatalog(t *testing.T) {
	boot := accountBootstrap{sourceID: accountModelsSourceID, label: "Preset", apiBaseURL: "https://gateway.invalid/v1"}
	if got := fetchPreset(boot, "private", AccountModelsState{}); got.source != nil || got.state.Reason != AccountReasonUnsafe {
		t.Fatal("missing conversation accepted")
	}
	boot.defaults = map[string]string{"conversation": "chat", "title": "chat", "speech": "voice", "embedding": "embed"}
	got := fetchPreset(boot, "private", AccountModelsState{})
	if got.source == nil || got.state.State != AccountReady || got.state.Models != 1 {
		t.Fatalf("preset=%+v", got.state)
	}
	if k := got.source.Kinds(); k.Speech != 1 || k.Embedding != 1 || k.Chat != 1 {
		t.Fatal(k)
	}
	boot.sourceID = "not-a-gateway-source"
	if got := fetchPreset(boot, "private", AccountModelsState{}); got.source != nil {
		t.Fatal("invalid source accepted")
	}
}
