// Package audioparams holds the public, versioned gateway audio contract.
package audioparams

import (
	"encoding/json"
	"errors"
	"math"
	"slices"
	"unicode/utf8"
)

type Enum struct {
	Values       []string `json:"values"`
	Default      *string  `json:"default,omitempty"`
	Configurable bool     `json:"configurable"`
}
type Number struct {
	Min          float64 `json:"min"`
	Max          float64 `json:"max"`
	Default      float64 `json:"default"`
	Configurable bool    `json:"configurable"`
}
type Text struct {
	MaxCharacters int     `json:"max_characters"`
	Default       *string `json:"default,omitempty"`
	Configurable  bool    `json:"configurable"`
}
type Parameters struct {
	Voice              *Enum   `json:"voice,omitempty"`
	ResponseFormat     *Enum   `json:"response_format,omitempty"`
	StreamFormat       *Enum   `json:"stream_format,omitempty"`
	Language           *Enum   `json:"language,omitempty"`
	Speed              *Number `json:"speed,omitempty"`
	Instructions       *Text   `json:"instructions,omitempty"`
	Prompt             *Text   `json:"prompt,omitempty"`
	MaxInputCharacters int     `json:"max_input_characters,omitempty"`
	MaxFileBytes       int64   `json:"max_file_bytes,omitempty"`
}
type Descriptor struct {
	Version    int         `json:"version"`
	Revision   string      `json:"revision,omitempty"`
	Status     string      `json:"status"`
	Parameters *Parameters `json:"parameters,omitempty"`
}

// Overrides retains omission separately from an explicit value equal to a default.
type Overrides struct {
	Voice          *string  `json:"voice,omitempty" yaml:"voice,omitempty"`
	ResponseFormat *string  `json:"response_format,omitempty" yaml:"response_format,omitempty"`
	StreamFormat   *string  `json:"stream_format,omitempty" yaml:"stream_format,omitempty"`
	Language       *string  `json:"language,omitempty" yaml:"language,omitempty"`
	Speed          *float64 `json:"speed,omitempty" yaml:"speed,omitempty"`
	Instructions   *string  `json:"instructions,omitempty" yaml:"instructions,omitempty"`
	Prompt         *string  `json:"prompt,omitempty" yaml:"prompt,omitempty"`
}

func Parse(raw json.RawMessage, role string) Descriptor {
	unknown := Descriptor{Status: "unsupported"}
	var d Descriptor
	if json.Unmarshal(raw, &d) != nil || d.Version != 1 || d.Revision == "" || len(d.Revision) > 256 {
		return unknown
	}
	if d.Status == "undeclared" || d.Status == "incompatible" {
		d.Parameters = nil
		return d
	}
	if d.Status != "configured" || d.Parameters == nil {
		return unknown
	}
	p := d.Parameters
	for _, e := range []*Enum{p.Voice, p.ResponseFormat, p.StreamFormat, p.Language} {
		if e == nil {
			continue
		}
		if len(e.Values) == 0 || len(e.Values) > 128 {
			return unknown
		}
		seen := map[string]bool{}
		for _, v := range e.Values {
			if v == "" || len(v) > 256 || seen[v] || !utf8.ValidString(v) {
				return unknown
			}
			seen[v] = true
		}
		if e.Default != nil && !slices.Contains(e.Values, *e.Default) || !e.Configurable && e.Default == nil {
			return unknown
		}
	}
	if n := p.Speed; n != nil && (math.IsNaN(n.Min) || math.IsNaN(n.Max) || math.IsNaN(n.Default) || math.IsInf(n.Min, 0) || math.IsInf(n.Max, 0) || math.IsInf(n.Default, 0) || n.Min <= 0 || n.Max < n.Min || n.Default < n.Min || n.Default > n.Max) {
		return unknown
	}
	for _, t := range []*Text{p.Instructions, p.Prompt} {
		if t != nil && (t.MaxCharacters < 1 || t.MaxCharacters > 100000 || t.Default != nil && utf8.RuneCountInString(*t.Default) > t.MaxCharacters) {
			return unknown
		}
	}
	if p.MaxFileBytes < 0 || p.MaxInputCharacters < 0 {
		return unknown
	}
	if role == "speech" {
		p.Language = nil
		p.Prompt = nil
		p.MaxFileBytes = 0
	} else if role == "transcription" {
		p.Voice = nil
		p.Speed = nil
		p.StreamFormat = nil
		p.Instructions = nil
		p.MaxInputCharacters = 0
	} else {
		return unknown
	}
	return d
}

func ParseCatalog(raw []byte) map[string]Descriptor {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	type row struct {
		ID    string          `json:"id"`
		Role  string          `json:"model_type"`
		Audio json.RawMessage `json:"audio"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return nil
	}
	var rows []row
	if json.Unmarshal(envelope.Data, &rows) != nil {
		var one row
		if json.Unmarshal(envelope.Data, &one) != nil || one.ID == "" {
			return nil
		}
		rows = []row{one}
	}
	result := map[string]Descriptor{}
	for _, row := range rows {
		if _, exists := result[row.ID]; !exists && row.ID != "" && len(row.ID) <= 256 && (row.Role == "speech" || row.Role == "transcription") {
			result[row.ID] = Parse(row.Audio, row.Role)
		}
	}
	return result
}

func (o Overrides) Values() map[string]any {
	result := map[string]any{}
	for name, value := range map[string]*string{"voice": o.Voice, "response_format": o.ResponseFormat, "stream_format": o.StreamFormat, "language": o.Language, "instructions": o.Instructions, "prompt": o.Prompt} {
		if value != nil {
			result[name] = *value
		}
	}
	if o.Speed != nil {
		result["speed"] = *o.Speed
	}
	return result
}

// Resolve returns explicit request fields and separate effective playback values.
// Defaults are not copied into the request: the gateway remains their owner.
func Resolve(d Descriptor, o Overrides) (map[string]any, map[string]any, error) {
	if d.Version != 1 || d.Status != "configured" || d.Parameters == nil {
		return nil, nil, errors.New("audio configuration unavailable")
	}
	p := d.Parameters
	request := o.Values()
	effective := map[string]any{}
	for name, option := range map[string]*Enum{"voice": p.Voice, "response_format": p.ResponseFormat, "stream_format": p.StreamFormat, "language": p.Language} {
		value, explicit := request[name]
		if explicit {
			text, ok := value.(string)
			if option == nil || !ok || !slices.Contains(option.Values, text) || !option.Configurable {
				return nil, nil, errors.New("invalid audio parameter")
			}
			effective[name] = text
		} else if option != nil && option.Default != nil {
			effective[name] = *option.Default
		} else if name == "voice" && option != nil {
			return nil, nil, errors.New("voice selection required")
		}
	}
	if value, explicit := request["speed"]; explicit {
		n := value.(float64)
		o := p.Speed
		if o == nil || math.IsNaN(n) || math.IsInf(n, 0) || n < o.Min || n > o.Max || !o.Configurable {
			return nil, nil, errors.New("invalid audio parameter")
		}
		effective["speed"] = n
	} else if p.Speed != nil {
		effective["speed"] = p.Speed.Default
	}
	for name, option := range map[string]*Text{"instructions": p.Instructions, "prompt": p.Prompt} {
		if value, explicit := request[name]; explicit {
			text := value.(string)
			if option == nil || !utf8.ValidString(text) || utf8.RuneCountInString(text) > option.MaxCharacters || !option.Configurable {
				return nil, nil, errors.New("invalid audio parameter")
			}
			effective[name] = text
		} else if option != nil && option.Default != nil {
			effective[name] = *option.Default
		}
	}
	return request, effective, nil
}

// View binds public declarations and explicit preferences to one authority.
type View struct {
	Descriptor
	Scope     string    `json:"scope"`
	Overrides Overrides `json:"overrides"`
}

// CloneOverrides detaches mutable preference pointers at publication boundaries.
func CloneOverrides(values map[string]Overrides) map[string]Overrides {
	if values == nil {
		return nil
	}
	result := make(map[string]Overrides, len(values))
	for key, o := range values {
		result[key] = Overrides{Voice: copyPointer(o.Voice), ResponseFormat: copyPointer(o.ResponseFormat), StreamFormat: copyPointer(o.StreamFormat), Language: copyPointer(o.Language), Speed: copyPointer(o.Speed), Instructions: copyPointer(o.Instructions), Prompt: copyPointer(o.Prompt)}
	}
	return result
}
func copyPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
