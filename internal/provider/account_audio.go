package provider

import (
	"errors"

	"github.com/LubyRuffy/eino-swarm/internal/config"
)

// ErrAccountAudioUnavailable means the server did not enable this voice role.
var ErrAccountAudioUnavailable = errors.New("account_audio_unavailable")

// AccountAudio resolves a fixed server voice role without the conversation
// profile's alias rewriting. Its private credential remains inside Go.
func (p *Pool) AccountAudio(role string) (config.Provider, string, error) {
	if role != "transcription" && role != "speech" {
		return config.Provider{}, "", ErrAccountAudioUnavailable
	}
	src := p.activeAccount()
	if src == nil {
		return config.Provider{}, "", ErrAccountLoginRequired
	}
	if on, configured := src.Role(role); !configured || !on {
		return config.Provider{}, "", ErrAccountAudioUnavailable
	}
	label := role
	for _, value := range src.roles {
		if value.ID == role && value.Label != "" {
			label = value.Label
		}
	}
	return config.Provider{ID: src.id, Label: label, BaseURL: src.baseURL, APIKey: src.token, Model: role}, label, nil
}
