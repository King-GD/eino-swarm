package speech

import "github.com/LubyRuffy/eino-swarm/internal/audioparams"

func (s *Service) resolveAudio(selected *selection) error {
	role := "speech"
	if selected.isInput {
		role = "transcription"
	}
	selected.audio = s.pool.AudioParameters(selected.target.ID, selected.target.Model, role)
	a := selected.audio
	if a == nil {
		return nil
	}
	a.Overrides = selected.settings.GatewayParameters[a.Scope]
	// Old/unknown manifests and undeclared models retain legacy passthrough.
	if a.Version != 1 || a.Status == "undeclared" {
		if len(a.Overrides.Values()) > 0 {
			return failure("invalid_audio_parameter", 409)
		}
		return nil
	}
	request, effective, err := audioparams.Resolve(a.Descriptor, a.Overrides)
	if err != nil {
		return failure("invalid_audio_parameter", 409)
	}
	selected.parameters = request
	p := a.Parameters
	if selected.isInput {
		if format, ok := effective["response_format"].(string); ok && format != "json" && format != "verbose_json" {
			return failure("speech_input_format", 400)
		}
		selected.settings.Input.Language, _ = effective["language"].(string)
		selected.settings.Input.Prompt, _ = effective["prompt"].(string)
		if p.MaxFileBytes > 0 && p.MaxFileBytes < selected.settings.Limits.InputBytes {
			selected.settings.Limits.InputBytes = p.MaxFileBytes
		}
	} else {
		out := &selected.settings.Output
		out.Voice, _ = effective["voice"].(string)
		out.Instructions, _ = effective["instructions"].(string)
		out.Speed = 1
		if speed, ok := effective["speed"].(float64); ok {
			out.Speed = speed
		}
		out.Format = "mp3"
		if format, ok := effective["response_format"].(string); ok {
			out.Format = format
		}
		if out.Format != "mp3" && out.Format != "wav" && out.Format != "pcm" {
			return failure("speech_input_format", 400)
		}
		if stream, _ := effective["stream_format"].(string); stream != "" && stream != "audio" && (stream != "sse" || out.Format != "pcm") {
			return failure("speech_input_format", 400)
		}
		if p.MaxInputCharacters > 0 && p.MaxInputCharacters < selected.settings.Limits.ChunkChars {
			selected.settings.Limits.ChunkChars = p.MaxInputCharacters
		}
	}
	return nil
}

func speechBody(selected selection, text string, stream bool) map[string]any {
	output := selected.settings.Output
	body := map[string]any{"model": selected.target.Model, "input": text}
	if selected.parameters != nil {
		for key, value := range selected.parameters {
			body[key] = value
		}
		return body
	}
	body["response_format"], body["speed"] = output.Format, output.Speed
	if output.Format == "pcm" {
		body["stream_format"] = "audio"
		if stream && selected.audio != nil {
			body["stream_format"] = "sse"
		}
	}
	if output.Voice != "" {
		body["voice"] = output.Voice
	}
	if output.Instructions != "" {
		body["instructions"] = output.Instructions
	}
	return body
}
