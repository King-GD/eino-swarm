package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/LubyRuffy/eino-swarm/internal/audioparams"
)

// SetAudioParameters accepts only the private catalog, before source publication.
func (s *AccountSource) SetAudioParameters(raw []byte) { s.audio = audioparams.ParseCatalog(raw) }

// AudioParameters never grants access. RoleProvider must first authorize execution.
func (p *Pool) AudioParameters(id, model, role string) *audioparams.View {
	source := p.accountForExecution(id)
	if source == nil {
		return nil
	}
	view := &audioparams.View{Descriptor: audioparams.Descriptor{Status: "unsupported"}}
	sum := sha256.Sum256([]byte(source.owner + "\x00" + source.baseURL + "\x00" + source.id + "\x00" + source.catalogGroup + "\x00" + model + "\x00" + role))
	view.Scope = "audio:" + hex.EncodeToString(sum[:])
	if !source.allowsRole(role) || source.capabilities == nil || source.capabilities.Capabilities[role].AudioParametersVersion != 1 {
		return view
	}
	view.Descriptor = audioparams.Descriptor{Version: 1, Status: "unavailable"}
	if d, ok := source.audio[model]; ok {
		// Parse a fresh copy: callers cannot mutate the published account source.
		raw, _ := json.Marshal(d)
		view.Descriptor = audioparams.Parse(raw, role)
	}
	return view
}

// NotifyAudioParametersChanged schedules the existing owned refresh loop.
func (p *Pool) NotifyAudioParametersChanged() {
	p.mu.Lock()
	trigger := p.capabilitiesTrigger
	p.mu.Unlock()
	if trigger != nil {
		trigger()
	}
}
