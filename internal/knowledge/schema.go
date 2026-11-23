// Package knowledge owns the file-backed schema and pages of one project wiki.
package knowledge

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

var (
	ErrDisabled  = errors.New("knowledge is disabled")
	ErrInvalid   = errors.New("invalid knowledge data")
	ErrNotFound  = errors.New("knowledge item not found")
	ErrConflict  = errors.New("knowledge revision conflict")
	ErrDuplicate = errors.New("knowledge page already exists")
)

const (
	schemaFile   = "schema.yaml"
	pagesDir     = "pages"
	typeView     = "type"
	pendingView  = "pending"
	maxViewDepth = 16
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidSlug accepts the same portable filename component in schema, pages and
// API paths. Windows treats several otherwise valid components as devices.
func ValidSlug(value string) bool {
	if len(value) > 100 || !slugPattern.MatchString(value) {
		return false
	}
	switch value {
	case "aux", "con", "nul", "prn", "com1", "com2", "com3", "com4", "com5", "com6", "com7", "com8", "com9",
		"lpt1", "lpt2", "lpt3", "lpt4", "lpt5", "lpt6", "lpt7", "lpt8", "lpt9":
		return false
	default:
		return true
	}
}

type Schema struct {
	Version   int        `yaml:"version" json:"version"`
	Types     []PageType `yaml:"types" json:"types"`
	Relations []Relation `yaml:"relations,omitempty" json:"relations"`
	Views     []View     `yaml:"views,omitempty" json:"views"`
}

type SchemaAppend struct {
	Types     []PageType `json:"types,omitempty"`
	Relations []Relation `json:"relations,omitempty"`
	Views     []View     `json:"views,omitempty"`
}

type PageType struct {
	ID            string   `yaml:"id" json:"id"`
	Dir           string   `yaml:"dir" json:"dir"`
	Description   string   `yaml:"description,omitempty" json:"description,omitempty"`
	Fields        []Field  `yaml:"fields,omitempty" json:"fields,omitempty"`
	Sections      []string `yaml:"sections,omitempty" json:"sections,omitempty"`
	FreshnessDays int      `yaml:"freshness_days,omitempty" json:"freshness_days,omitempty"`
}

type Field struct {
	ID       string `yaml:"id" json:"id"`
	Kind     string `yaml:"kind" json:"kind"`
	Required bool   `yaml:"required,omitempty" json:"required,omitempty"`
}

type Relation struct {
	ID          string   `yaml:"id" json:"id"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	From        []string `yaml:"from" json:"from"`
	To          []string `yaml:"to" json:"to"`
	Inverse     string   `yaml:"inverse,omitempty" json:"inverse,omitempty"`
	Required    bool     `yaml:"required,omitempty" json:"required,omitempty"`
}

type View struct {
	ID       string   `yaml:"id" json:"id"`
	RootType string   `yaml:"root_type" json:"root_type"`
	Follow   []string `yaml:"follow,omitempty" json:"follow,omitempty"`
	Depth    int      `yaml:"depth" json:"depth"`
}

func starterSchema() Schema {
	return Schema{
		Version:   1,
		Types:     []PageType{{ID: "entry", Dir: "entries", Description: "A durable page about one subject."}},
		Relations: []Relation{{ID: "related", Description: "An explicit connection between pages.", From: []string{"entry"}, To: []string{"entry"}, Inverse: "related"}},
	}
}

type schemaIndex struct {
	types     map[string]PageType
	relations map[string]Relation
	views     map[string]View
}

func parseSchema(data []byte) (Schema, schemaIndex, error) {
	var schema Schema
	if err := unmarshalYAML(data, &schema); err != nil {
		return schema, schemaIndex{}, fmt.Errorf("%w: schema: %v", ErrInvalid, err)
	}
	index, err := validateSchema(schema)
	return schema, index, err
}

func unmarshalYAML(data []byte, dst any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	err := decoder.Decode(&extra)
	if err != io.EOF {
		return errors.New("expected a single YAML document")
	}
	return nil
}

func validateSchema(schema Schema) (schemaIndex, error) {
	index := schemaIndex{types: map[string]PageType{}, relations: map[string]Relation{}, views: map[string]View{}}
	if schema.Version != 1 || len(schema.Types) == 0 {
		return index, fmt.Errorf("%w: schema version must be 1 and types must be nonempty", ErrInvalid)
	}
	dirs := map[string]bool{}
	for _, kind := range schema.Types {
		if !ValidSlug(kind.ID) || !ValidSlug(kind.Dir) || dirs[kind.Dir] || index.types[kind.ID].ID != "" {
			return index, fmt.Errorf("%w: invalid or duplicate type %q or directory %q", ErrInvalid, kind.ID, kind.Dir)
		}
		if kind.FreshnessDays < 0 || kind.FreshnessDays > 36500 {
			return index, fmt.Errorf("%w: invalid freshness_days for type %q", ErrInvalid, kind.ID)
		}
		fields := map[string]bool{}
		for _, field := range kind.Fields {
			if !ValidSlug(field.ID) || fields[field.ID] || !validFieldKind(field.Kind) {
				return index, fmt.Errorf("%w: invalid or duplicate field %q on type %q", ErrInvalid, field.ID, kind.ID)
			}
			fields[field.ID] = true
		}
		dirs[kind.Dir] = true
		index.types[kind.ID] = kind
	}
	for _, relation := range schema.Relations {
		if !ValidSlug(relation.ID) || index.relations[relation.ID].ID != "" ||
			!validEndpoints(relation.From, index.types) || !validEndpoints(relation.To, index.types) {
			return index, fmt.Errorf("%w: invalid or duplicate relation %q", ErrInvalid, relation.ID)
		}
		index.relations[relation.ID] = relation
	}
	for _, view := range schema.Views {
		if !ValidSlug(view.ID) || view.ID == typeView || view.ID == pendingView ||
			index.views[view.ID].ID != "" || index.types[view.RootType].ID == "" ||
			view.Depth < 0 || view.Depth > maxViewDepth {
			return index, fmt.Errorf("%w: invalid or duplicate view %q", ErrInvalid, view.ID)
		}
		followed := map[string]bool{}
		for _, rel := range view.Follow {
			if index.relations[rel].ID == "" || followed[rel] {
				return index, fmt.Errorf("%w: invalid or duplicate relation %q in view %q", ErrInvalid, rel, view.ID)
			}
			followed[rel] = true
		}
		index.views[view.ID] = view
	}
	return index, nil
}

func validEndpoints(ids []string, types map[string]PageType) bool {
	seen := map[string]bool{}
	for _, id := range ids {
		if types[id].ID == "" || seen[id] {
			return false
		}
		seen[id] = true
	}
	return len(ids) > 0
}

func validFieldKind(kind string) bool {
	switch kind {
	case "string", "text", "number", "integer", "boolean", "date", "url", "list":
		return true
	default:
		return false
	}
}

var nameFold = cases.Fold()

// Names compare with canonical Unicode normalization, case folding and
// collapsed whitespace.
func normalizedName(s string) string {
	return strings.Join(strings.Fields(norm.NFC.String(nameFold.String(s))), " ")
}

func cloneSchema(schema Schema) Schema {
	schema.Types = slices.Clone(schema.Types)
	schema.Relations = append([]Relation{}, schema.Relations...)
	schema.Views = append([]View{}, schema.Views...)
	for i := range schema.Types {
		kind := &schema.Types[i]
		kind.Fields = slices.Clone(kind.Fields)
		kind.Sections = slices.Clone(kind.Sections)
	}
	for i := range schema.Relations {
		relation := &schema.Relations[i]
		relation.From = slices.Clone(relation.From)
		relation.To = slices.Clone(relation.To)
	}
	for i := range schema.Views {
		schema.Views[i].Follow = slices.Clone(schema.Views[i].Follow)
	}
	return schema
}
