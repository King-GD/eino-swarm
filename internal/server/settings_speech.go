package server

import (
	"github.com/LubyRuffy/eino-swarm/internal/audioparams"
	"github.com/LubyRuffy/eino-swarm/internal/config"
)

type speechSettingsView struct {
	GatewayParameters map[string]audioparams.Overrides `json:"gateway_parameters,omitempty"`
	Input             struct {
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
	value.GatewayParameters = audioparams.CloneOverrides(cfg.GatewayParameters)
	return value
}

type putSpeechSettings struct {
	GatewayParameters *map[string]audioparams.Overrides `json:"gateway_parameters"`
	Input             *struct {
		config.SpeechInputConfig
		APIKey          *string `json:"api_key"`
		GatewayDisabled *bool   `json:"gateway_disabled"`
	} `json:"input"`
	Output *struct {
		config.SpeechOutputConfig
		APIKey          *string `json:"api_key"`
		GatewayDisabled *bool   `json:"gateway_disabled"`
	} `json:"output"`
	Limits *config.SpeechLimits `json:"limits"`
}

func mergeSpeechSettings(previous config.SpeechConfig, patch *putSpeechSettings) config.SpeechConfig {
	if patch.GatewayParameters != nil {
		previous.GatewayParameters = audioparams.CloneOverrides(*patch.GatewayParameters)
	}
	if patch.Input != nil {
		key := previous.Input.APIKey
		if patch.Input.APIKey != nil {
			key = *patch.Input.APIKey
		}
		gatewayDisabled := previous.Input.GatewayDisabled
		if patch.Input.GatewayDisabled != nil {
			gatewayDisabled = *patch.Input.GatewayDisabled
		}
		previous.Input = patch.Input.SpeechInputConfig
		previous.Input.APIKey = key
		previous.Input.GatewayDisabled = gatewayDisabled
	}
	if patch.Output != nil {
		key := previous.Output.APIKey
		if patch.Output.APIKey != nil {
			key = *patch.Output.APIKey
		}
		gatewayDisabled := previous.Output.GatewayDisabled
		if patch.Output.GatewayDisabled != nil {
			gatewayDisabled = *patch.Output.GatewayDisabled
		}
		previous.Output = patch.Output.SpeechOutputConfig
		previous.Output.APIKey = key
		previous.Output.GatewayDisabled = gatewayDisabled
	}
	if patch.Limits != nil {
		previous.Limits = *patch.Limits
	}
	return previous
}
