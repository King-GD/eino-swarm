package server

import (
	"github.com/LubyRuffy/eino-swarm/internal/config"
)

type speechSettingsView struct {
	Input struct {
		config.SpeechInputConfig
		HasAPIKey bool `json:"has_api_key"`
	} `json:"input"`
	Output struct {
		config.SpeechOutputConfig
		HasAPIKey bool `json:"has_api_key"`
	} `json:"output"`
	Limits config.SpeechLimits `json:"limits"`
}

func toSpeechSettingsView(cfg config.SpeechConfig) speechSettingsView {
	var value speechSettingsView
	value.Input.SpeechInputConfig, value.Input.HasAPIKey = cfg.Input, cfg.Input.APIKey != ""
	value.Output.SpeechOutputConfig, value.Output.HasAPIKey = cfg.Output, cfg.Output.APIKey != ""
	value.Limits = cfg.Limits
	return value
}

type putSpeechSettings struct {
	Input *struct {
		config.SpeechInputConfig
		APIKey *string `json:"api_key"`
	} `json:"input"`
	Output *struct {
		config.SpeechOutputConfig
		APIKey *string `json:"api_key"`
	} `json:"output"`
	Limits *config.SpeechLimits `json:"limits"`
}

func mergeSpeechSettings(previous config.SpeechConfig, patch *putSpeechSettings) config.SpeechConfig {
	if patch.Input != nil {
		key := previous.Input.APIKey
		if patch.Input.APIKey != nil {
			key = *patch.Input.APIKey
		}
		previous.Input = patch.Input.SpeechInputConfig
		previous.Input.APIKey = key
	}
	if patch.Output != nil {
		key := previous.Output.APIKey
		if patch.Output.APIKey != nil {
			key = *patch.Output.APIKey
		}
		previous.Output = patch.Output.SpeechOutputConfig
		previous.Output.APIKey = key
	}
	if patch.Limits != nil {
		previous.Limits = *patch.Limits
	}
	return previous
}
