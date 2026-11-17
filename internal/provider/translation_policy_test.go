package provider

import (
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

// Old fixed-role servers used an off translation switch to keep conversation
// translation. A negotiated denial is a permission boundary and cannot do that.
func TestLegacyTranslationFallbackDoesNotBypassNegotiatedDenial(t *testing.T) {
	for _, negotiated := range []bool{false, true} {
		p := New(config.Default())
		src := accountSourceFor(t, "https://gateway.invalid/v1", "private")
		src.SetRoles([]AccountRole{{ID: "conversation", Enabled: true}, {ID: "translation", Enabled: false}})
		if negotiated {
			src.SetCapabilities(roleManifest("conversation", "chat_completions", false))
		}
		p.SetAccountSource(src)
		ref, configured := p.RoleSelection("translation", src.id, "conversation")
		if !configured || negotiated && ref != (config.ModelRef{}) || !negotiated && (ref.ProviderID != src.id || ref.Model != "conversation") {
			t.Fatalf("negotiated=%v: selection=%+v configured=%v", negotiated, ref, configured)
		}
		if p.FeatureAvailable(src.id, "translation") == negotiated {
			t.Fatalf("negotiated=%v: translation grant did not remain independent", negotiated)
		}
	}
}
