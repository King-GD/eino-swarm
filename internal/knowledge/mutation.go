package knowledge

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

func (s *Store) WritePage(change PageWrite, stamp Stamp) (*Page, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, snap, err := s.openEnabled()
	if err != nil {
		return nil, err
	}
	defer root.Close()

	page := clonePage(change.Page)
	page.Title = strings.TrimSpace(page.Title)
	page.UpdatedAt = stamp.UpdatedAt.UTC()
	page.UpdatedTurn = stamp.UpdatedTurn
	page.UpdatedThread = stamp.UpdatedThread
	if page.UpdatedAt.IsZero() || strings.TrimSpace(page.UpdatedTurn) == "" {
		return nil, fmt.Errorf("%w: a page write requires a host time and turn", ErrInvalid)
	}
	for i := range page.Sources {
		source := &page.Sources[i]
		source.Available = nil
		source.TurnID = ""
		if source.URI != "" && source.Classification == "" {
			source.Classification = "external"
		}
	}
	if err := snap.validatePageWrite(page); err != nil {
		return nil, err
	}
	data, err := marshalPage(page)
	if err != nil {
		return nil, err
	}
	folder, err := openTypeDir(root, snap.index.types[page.Type].Dir, true)
	if err != nil {
		return nil, err
	}
	defer folder.Close()
	// Publishing checks the actual bytes even if an external editor
	// preserved the cached stat signature.
	s.cached = nil
	if err := publishFile(folder, page.ID+".md", page.Revision, data); err != nil {
		return nil, err
	}
	page.Revision = revision(data)
	return &page, nil
}

func (s *Snapshot) validatePageWrite(page Page) error {
	if err := validatePageIdentity(page, s.index); err != nil {
		return err
	}
	if s.ambiguous[page.ID] {
		return fmt.Errorf("%w: page ID %q is ambiguous", ErrConflict, page.ID)
	}
	existing, found := s.pages[page.ID]
	exclude := ""
	if found && page.Revision != "" {
		exclude = page.ID
	}
	if ref, found := s.matchedPage(page, exclude); found {
		return &DuplicatePageError{Matched: ref}
	}
	if (found && existing.page.Revision != page.Revision) || (!found && page.Revision != "") {
		return fmt.Errorf("%w: page %q", ErrConflict, page.ID)
	}
	if found && existing.page.Type != page.Type {
		return fmt.Errorf("%w: changing a page type requires moving the Markdown file", ErrInvalid)
	}
	if err := contentError(page, s.index); err != nil {
		return err
	}
	for _, link := range page.Links {
		if err := s.validateTargetType(page, link); err != nil {
			return err
		}
	}
	// A newly resolved link must also honor the type declared by its author.
	// Existing pages keep their types, so their incoming links need no check.
	if !found {
		for _, id := range s.ordered {
			source := s.pages[id].page
			for _, link := range source.Links {
				relation := s.index.relations[link.Rel]
				if link.To == page.ID && slices.Contains(relation.From, source.Type) && !slices.Contains(relation.To, page.Type) {
					return fmt.Errorf("%w: page %q requires target %q of type in %v for relation %q",
						ErrInvalid, source.ID, page.ID, relation.To, relation.ID)
				}
			}
		}
	}
	return nil
}

func (s *Snapshot) validateTargetType(page Page, link Link) error {
	var targetType string
	if link.To == page.ID {
		targetType = page.Type
	} else if target, exists := s.pages[link.To]; exists {
		targetType = target.page.Type
	}
	relation := s.index.relations[link.Rel]
	if targetType != "" && !slices.Contains(relation.To, targetType) {
		return fmt.Errorf("%w: relation %q does not accept target type %q for %q", ErrInvalid, relation.ID, targetType, link.To)
	}
	return nil
}

func marshalPage(page Page) ([]byte, error) {
	front, err := yaml.Marshal(page)
	if err != nil {
		return nil, err
	}
	return []byte("---\n" + string(front) + "---\n" + page.Body), nil
}

func (s *Store) DeletePage(id, revision string) error {
	if !ValidSlug(id) {
		return fmt.Errorf("%w: invalid page slug %q", ErrInvalid, id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	root, snap, err := s.openEnabled()
	if err != nil {
		return err
	}
	defer root.Close()
	if snap.ambiguous[id] {
		return fmt.Errorf("%w: page ID %q is ambiguous", ErrConflict, id)
	}
	existing, exists := snap.pages[id]
	if !exists {
		return fmt.Errorf("%w: page %q", ErrNotFound, id)
	}
	if revision == "" || revision != existing.page.Revision {
		return fmt.Errorf("%w: page %q", ErrConflict, id)
	}
	folder, err := openTypeDir(root, snap.index.types[existing.page.Type].Dir, false)
	if err != nil {
		return err
	}
	defer folder.Close()
	name := id + ".md"
	s.cached = nil
	if err := matchRevision(folder, name, revision); err != nil {
		return err
	}
	if err := folder.Remove(name); err != nil {
		return err
	}
	return nil
}

func openTypeDir(root *os.Root, name string, create bool) (*os.Root, error) {
	pages, err := openDir(root, pagesDir, create)
	if err != nil {
		return nil, err
	}
	defer pages.Close()
	return openDir(pages, name, create)
}

func (s *Store) openEnabled() (*os.Root, *Snapshot, error) {
	root, err := s.open(false)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, ErrDisabled
	}
	if err != nil {
		return nil, nil, err
	}
	snap, err := s.snapshot(root)
	if err == nil && !snap.Enabled() {
		err = ErrDisabled
	}
	if err != nil {
		root.Close()
		return nil, nil, err
	}
	return root, snap, nil
}

func (s *Store) AppendSchema(add SchemaAppend, want string) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, snap, err := s.openEnabled()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if want == "" || snap.revision != want {
		return nil, fmt.Errorf("%w: schema", ErrConflict)
	}
	if len(add.Types)+len(add.Relations)+len(add.Views) == 0 {
		return nil, fmt.Errorf("%w: schema additions are empty", ErrInvalid)
	}
	s.cached = nil
	data, err := readRegular(root, schemaFile, s.files.schema)
	if err != nil {
		return nil, err
	}
	if revision(data) != want {
		return nil, fmt.Errorf("%w: schema", ErrConflict)
	}
	next, err := appendYAML(data, add)
	if err != nil {
		return nil, err
	}
	added, err := buildSnapshot(root, s.files, next)
	if err != nil {
		return nil, err
	}
	if err := publishFile(root, schemaFile, want, next); err != nil {
		return nil, err
	}
	return added, nil
}

// Append the collections in the existing document. Re-encoding the YAML may
// change whitespace but keeps existing entries, key order and comments.
func appendYAML(data []byte, add SchemaAppend) ([]byte, error) {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	mapping := document.Content[0]
	for _, entry := range []struct {
		name   string
		length int
		values any
	}{
		{name: "types", length: len(add.Types), values: add.Types},
		{name: "relations", length: len(add.Relations), values: add.Relations},
		{name: "views", length: len(add.Views), values: add.Views},
	} {
		if entry.length > 0 {
			if err := appendYAMLSequence(mapping, entry.name, entry.values); err != nil {
				return nil, err
			}
		}
	}
	return yaml.Marshal(&document)
}

func appendYAMLSequence(mapping *yaml.Node, key string, values any) error {
	var newItems yaml.Node
	if err := newItems.Encode(values); err != nil {
		return err
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value != key {
			continue
		}
		sequence := mapping.Content[i+1]
		if sequence.Tag == "!!null" {
			sequence.Tag, sequence.Kind, sequence.Value = "!!seq", yaml.SequenceNode, ""
			// A block sequence has no scalar line to carry the old comment.
			sequence.HeadComment = strings.TrimSpace(sequence.HeadComment + "\n" + sequence.LineComment)
			sequence.LineComment = ""
		}
		if sequence.Kind != yaml.SequenceNode {
			return fmt.Errorf("%w: schema %s must be an explicit list to append items", ErrInvalid, key)
		}
		sequence.Content = append(sequence.Content, newItems.Content...)
		return nil
	}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, &newItems)
	return nil
}
