package knowledge

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net/url"
	"slices"
	"strings"
	"time"
)

type Page struct {
	ID            string         `yaml:"id" json:"id"`
	Type          string         `yaml:"type" json:"type"`
	Title         string         `yaml:"title" json:"title"`
	Aliases       []string       `yaml:"aliases,omitempty" json:"aliases,omitempty"`
	Fields        map[string]any `yaml:"fields,omitempty" json:"fields,omitempty"`
	Links         []Link         `yaml:"links,omitempty" json:"links,omitempty"`
	Sources       []Source       `yaml:"sources,omitempty" json:"sources,omitempty"`
	AsOf          string         `yaml:"as_of,omitempty" json:"as_of,omitempty"`
	UpdatedAt     time.Time      `yaml:"updated_at,omitempty" json:"updated_at,omitzero"`
	UpdatedTurn   string         `yaml:"updated_turn,omitempty" json:"updated_turn,omitempty"`
	UpdatedThread string         `yaml:"updated_thread,omitempty" json:"updated_thread,omitempty"`
	Body          string         `yaml:"-" json:"body"`
	Revision      string         `yaml:"-" json:"revision"`
}

type Link struct {
	Rel string `yaml:"rel" json:"rel"`
	To  string `yaml:"to" json:"to"`
}

type Source struct {
	URI            string    `yaml:"uri,omitempty" json:"uri,omitempty"`
	Kind           string    `yaml:"kind,omitempty" json:"kind,omitempty"`
	ThreadID       string    `yaml:"thread_id,omitempty" json:"thread_id,omitempty"`
	Seq            int64     `yaml:"seq,omitempty" json:"seq,omitempty"`
	Timestamp      time.Time `yaml:"timestamp,omitempty" json:"timestamp,omitzero"`
	Classification string    `yaml:"classification,omitempty" json:"classification,omitempty"`
	RecordID       string    `yaml:"record_id,omitempty" json:"-"`
	Available      *bool     `yaml:"-" json:"available,omitempty"`
	TurnID         string    `yaml:"-" json:"turn_id,omitempty"`
}

type PageWrite struct{ Page }

type Stamp struct {
	UpdatedAt     time.Time
	UpdatedTurn   string
	UpdatedThread string
}

type PageRef struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
}

func (p Page) ref() PageRef {
	return PageRef{ID: p.ID, Type: p.Type, Title: p.Title}
}

func revision(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func parsePage(data []byte) (Page, error) {
	var page Page
	first, rest, found := bytes.Cut(data, []byte("\n"))
	if !found || string(bytes.TrimSuffix(first, []byte("\r"))) != "---" {
		return page, fmt.Errorf("%w: missing Markdown front matter", ErrInvalid)
	}
	for offset := 0; offset < len(rest); {
		line, after, newline := bytes.Cut(rest[offset:], []byte("\n"))
		if string(bytes.TrimSuffix(line, []byte("\r"))) == "---" {
			if err := unmarshalYAML(rest[:offset], &page); err != nil {
				return page, fmt.Errorf("%w: page front matter: %v", ErrInvalid, err)
			}
			page.Body = string(after)
			page.Revision = revision(data)
			return page, nil
		}
		if !newline {
			break
		}
		offset += len(line) + 1
	}
	return page, fmt.Errorf("%w: unterminated Markdown front matter", ErrInvalid)
}

func validatePageIdentity(page Page, index schemaIndex) error {
	if !ValidSlug(page.ID) || strings.TrimSpace(page.Title) == "" || index.types[page.Type].ID == "" {
		return fmt.Errorf("%w: page needs a slug, title and known type", ErrInvalid)
	}
	return nil
}

func contentError(page Page, index schemaIndex) error {
	kind := index.types[page.Type]
	for name := range page.Fields {
		if !slices.ContainsFunc(kind.Fields, func(field Field) bool { return field.ID == name }) {
			return fmt.Errorf("%w: undefined field %q", ErrInvalid, name)
		}
	}
	for _, field := range kind.Fields {
		value := page.Fields[field.ID]
		if value == nil && !field.Required {
			continue
		}
		if !fieldValueMatches(field, value) {
			return fmt.Errorf("%w: field %q requires a %s value", ErrInvalid, field.ID, field.Kind)
		}
	}
	if page.AsOf != "" {
		if _, err := time.Parse(time.DateOnly, page.AsOf); err != nil {
			return fmt.Errorf("%w: as_of must be a date", ErrInvalid)
		}
	}
	for _, alias := range page.Aliases {
		if normalizedName(alias) == "" {
			return fmt.Errorf("%w: aliases must be nonempty", ErrInvalid)
		}
	}
	seen := map[Link]bool{}
	for _, link := range page.Links {
		rel, ok := index.relations[link.Rel]
		if !ok || !ValidSlug(link.To) || !slices.Contains(rel.From, page.Type) || seen[link] {
			return fmt.Errorf("%w: invalid or duplicate relation %q to %q from type %q", ErrInvalid, link.Rel, link.To, page.Type)
		}
		seen[link] = true
	}
	for _, source := range page.Sources {
		if !validSource(source) {
			return fmt.Errorf("%w: source requires an absolute URI or a history reference", ErrInvalid)
		}
	}
	return nil
}

func fieldValueMatches(field Field, value any) bool {
	switch field.Kind {
	case "string", "text", "date", "url":
		text, ok := value.(string)
		if !ok || (field.Required && strings.TrimSpace(text) == "") {
			return false
		}
		switch field.Kind {
		case "date":
			_, err := time.Parse(time.DateOnly, text)
			return err == nil
		case "url":
			return absoluteURI(text)
		default:
			return true
		}
	case "number", "integer":
		switch n := value.(type) {
		case int, int64, uint64:
			return true
		case float64:
			return !math.IsInf(n, 0) && !math.IsNaN(n) && (field.Kind != "integer" || math.Trunc(n) == n)
		}
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "list":
		switch list := value.(type) {
		case []string:
			return len(list) > 0 || !field.Required
		case []any:
			for _, item := range list {
				if _, ok := item.(string); !ok {
					return false
				}
			}
			return len(list) > 0 || !field.Required
		}
	}
	return false
}

func absoluteURI(value string) bool {
	if strings.TrimSpace(value) != value {
		return false
	}
	uri, err := url.Parse(value)
	return err == nil && uri.IsAbs() && (uri.Host != "" || uri.Opaque != "" || uri.Path != "") &&
		((uri.Scheme != "http" && uri.Scheme != "https") || uri.Host != "")
}

func validSource(source Source) bool {
	if source.URI != "" {
		return absoluteURI(source.URI) && source.Kind == "" && source.ThreadID == "" && source.Seq == 0 &&
			source.RecordID == "" && source.Timestamp.IsZero() && (source.Classification == "external" || source.Classification == "")
	}
	return (source.Kind == "message" || source.Kind == "event") && strings.TrimSpace(source.ThreadID) != "" &&
		source.Seq > 0 && (source.Classification == "primary" || source.Classification == "analysis" || source.Classification == "")
}

func clonePage(page Page) Page {
	page.Aliases = slices.Clone(page.Aliases)
	page.Links = slices.Clone(page.Links)
	page.Sources = slices.Clone(page.Sources)
	if page.Fields != nil {
		cloned := make(map[string]any, len(page.Fields))
		for k, v := range page.Fields {
			cloned[k] = cloneField(v)
		}
		page.Fields = cloned
	}
	return page
}

func cloneField(value any) any {
	switch value := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for k, v := range value {
			out[k] = cloneField(v)
		}
		return out
	case []any:
		out := make([]any, len(value))
		for i, v := range value {
			out[i] = cloneField(v)
		}
		return out
	case []string:
		return slices.Clone(value)
	default:
		return value
	}
}
