package knowledge

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

const maxFileBytes = 4 << 20

// Store serializes access to one knowledge directory. The engine shares one
// instance per project. Snapshots are derived from files and safe to keep
// after later writes replace their cached index.
type Store struct {
	dir    string
	mu     sync.Mutex
	cached *Snapshot
	files  inventory
}

func New(dir string) *Store  { return &Store{dir: filepath.Clean(dir)} }
func (s *Store) Dir() string { return s.dir }

type inventory struct {
	schema fs.FileInfo
	pages  map[string]fs.FileInfo
}

func (s *Store) Snapshot() (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.open(false)
	if errors.Is(err, fs.ErrNotExist) {
		return s.disabled(), nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return s.snapshot(root)
}

func (s *Store) disabled() *Snapshot {
	s.cached = nil
	s.files = inventory{}
	return &Snapshot{}
}

func (s *Store) snapshot(root *os.Root) (*Snapshot, error) {
	info, err := root.Lstat(schemaFile)
	if errors.Is(err, fs.ErrNotExist) {
		return s.disabled(), nil
	}
	if err != nil {
		return nil, err
	}
	current, err := scanPages(root)
	if err != nil {
		return nil, err
	}
	current.schema = info
	if s.cached != nil && current.same(s.files) {
		return s.cached, nil
	}
	data, err := readRegular(root, schemaFile, info)
	if err != nil {
		return nil, err
	}
	snap, err := buildSnapshot(root, current, data)
	if err != nil {
		return nil, err
	}
	s.cached, s.files = snap, current
	return snap, nil
}

func (s *Store) Enable() (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	root, err := s.open(true)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	if _, err := root.Lstat(schemaFile); !errors.Is(err, fs.ErrNotExist) {
		if err != nil {
			return nil, err
		}
		return s.snapshot(root)
	}
	data, err := yaml.Marshal(starterSchema())
	if err != nil {
		return nil, err
	}
	files, err := scanPages(root)
	if err != nil {
		return nil, err
	}
	snap, err := buildSnapshot(root, files, data)
	if err != nil {
		return nil, err
	}
	if err := publishFile(root, schemaFile, "", data); err != nil {
		return nil, err
	}
	s.cached = nil
	return snap, nil
}

func (s *Store) open(create bool) (*os.Root, error) {
	// Parent is the configured project directory. It has already been
	// provisioned by the engine and must not be recreated for a stale owner.
	parent, err := os.OpenRoot(filepath.Dir(s.dir))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return openDir(parent, filepath.Base(s.dir), create)
}

func openDir(parent *os.Root, name string, create bool) (*os.Root, error) {
	info, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) && create {
		if err := parent.Mkdir(name, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		info, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a regular directory", ErrInvalid, name)
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, fmt.Errorf("%w: %s changed while opening", ErrConflict, name)
	}
	return root, nil
}

func scanPages(root *os.Root) (inventory, error) {
	result := inventory{pages: map[string]fs.FileInfo{}}
	pages, err := openDir(root, pagesDir, false)
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	defer pages.Close()

	dirs, err := fs.ReadDir(pages.FS(), ".")
	if err != nil {
		return result, err
	}
	for _, dir := range dirs {
		name := dir.Name()
		if dir.Type()&os.ModeSymlink != 0 {
			info, err := pages.Lstat(name)
			if err != nil {
				return result, err
			}
			result.pages[filepath.Join(pagesDir, name)] = info
			continue
		}
		if !dir.IsDir() {
			continue
		}
		if err := scanTypeDir(pages, name, result.pages); err != nil {
			return result, err
		}
	}
	return result, nil
}

func scanTypeDir(pages *os.Root, name string, out map[string]fs.FileInfo) error {
	folder, err := openDir(pages, name, false)
	if err != nil {
		return err
	}
	defer folder.Close()
	files, err := fs.ReadDir(folder.FS(), ".")
	if err != nil {
		return err
	}
	for _, file := range files {
		if filepath.Ext(file.Name()) != ".md" {
			continue
		}
		info, err := folder.Lstat(file.Name())
		if err != nil {
			return err
		}
		out[filepath.Join(pagesDir, name, file.Name())] = info
	}
	return nil
}

func (current inventory) same(previous inventory) bool {
	if !sameStat(current.schema, previous.schema) || len(current.pages) != len(previous.pages) {
		return false
	}
	for path, info := range current.pages {
		if prior := previous.pages[path]; prior == nil || !sameStat(info, prior) {
			return false
		}
	}
	return true
}

func sameStat(a, b fs.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}

func buildSnapshot(root *os.Root, files inventory, schemaBytes []byte) (*Snapshot, error) {
	schema, index, err := parseSchema(schemaBytes)
	if err != nil {
		return nil, err
	}
	snap := &Snapshot{schema: schema, index: index, revision: revision(schemaBytes), pages: map[string]indexedPage{}, ambiguous: map[string]bool{}}
	paths := make([]string, 0, len(files.pages))
	for path := range files.pages {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	for _, path := range paths {
		info := files.pages[path]
		page, err := loadPage(root, path, info, index)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				return nil, err
			}
			snap.issues = append(snap.issues, Issue{Kind: "invalid_page", Path: filepath.ToSlash(path), Detail: err.Error()})
			continue
		}
		snap.addPage(page, path, info)
	}
	snap.derive()
	return snap, nil
}

func loadPage(root *os.Root, path string, info fs.FileInfo, index schemaIndex) (Page, error) {
	data, err := readRegular(root, path, info)
	if err != nil {
		return Page{}, err
	}
	page, err := parsePage(data)
	if err != nil {
		return Page{}, err
	}
	if err := validatePageIdentity(page, index); err != nil {
		return Page{}, err
	}
	if path != filepath.Join(pagesDir, index.types[page.Type].Dir, page.ID+".md") {
		return Page{}, fmt.Errorf("%w: page ID and type must match its file and directory", ErrInvalid)
	}
	// YAML decodes an unquoted scalar date as time.Time inside a generic
	// field mapping. Normalize declared date fields for round-trip edits.
	for _, field := range index.types[page.Type].Fields {
		if date, ok := page.Fields[field.ID].(time.Time); ok && field.Kind == "date" {
			if date.Equal(date.Truncate(24 * time.Hour)) {
				page.Fields[field.ID] = date.Format(time.DateOnly)
			}
		}
	}
	if _, err := json.Marshal(page.Fields); err != nil {
		return Page{}, fmt.Errorf("%w: page fields must be JSON-compatible: %v", ErrInvalid, err)
	}
	return page, nil
}

func (s *Snapshot) addPage(page Page, path string, info fs.FileInfo) {
	if other, exists := s.pages[page.ID]; exists || s.ambiguous[page.ID] {
		if exists {
			s.issues = append(s.issues, Issue{Kind: "invalid_page", Path: filepath.ToSlash(other.path), Detail: "ambiguous page ID"})
		}
		delete(s.pages, page.ID)
		s.ambiguous[page.ID] = true
		s.issues = append(s.issues, Issue{Kind: "invalid_page", Path: filepath.ToSlash(path), Detail: "ambiguous page ID"})
		return
	}
	if err := contentError(page, s.index); err != nil {
		s.issues = append(s.issues, Issue{Kind: "invalid_page", PageID: page.ID, Path: filepath.ToSlash(path), Detail: err.Error()})
	}
	s.pages[page.ID] = indexedPage{page: page, path: path, modTime: info.ModTime()}
}

func readRegular(root *os.Root, name string, expected fs.FileInfo) ([]byte, error) {
	if !expected.Mode().IsRegular() || expected.Size() > maxFileBytes {
		return nil, fmt.Errorf("%w: %s must be a regular file of at most %d bytes", ErrInvalid, filepath.ToSlash(name), maxFileBytes)
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := compareOpenFile(f, expected); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFileBytes {
		return nil, fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalid, filepath.ToSlash(name), maxFileBytes)
	}
	if err := compareOpenFile(f, expected); err != nil {
		return nil, err
	}
	return data, nil
}

func compareOpenFile(file *os.File, expected fs.FileInfo) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !sameStat(info, expected) {
		return fmt.Errorf("%w: %s changed while reading", ErrConflict, filepath.Base(file.Name()))
	}
	return nil
}

func matchRevision(root *os.Root, name, want string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) && want == "" {
		return nil
	}
	if errors.Is(err, fs.ErrNotExist) || err == nil && want == "" {
		return fmt.Errorf("%w: %s", ErrConflict, name)
	}
	if err != nil {
		return err
	}
	data, err := readRegular(root, name, info)
	if err != nil {
		return err
	}
	if revision(data) != want {
		return fmt.Errorf("%w: %s", ErrConflict, name)
	}
	return nil
}

func publishFile(root *os.Root, name, want string, data []byte) error {
	if len(data) > maxFileBytes {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrInvalid, name, maxFileBytes)
	}
	tmp := ".knowledge-" + strings.ToLower(rand.Text()) + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if err = errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err := matchRevision(root, name, want); err != nil {
		return err
	}
	if want == "" {
		if err := root.Link(tmp, name); errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%w: %s", ErrConflict, name)
		} else if err != nil {
			return err
		}
		return nil
	}
	return root.Rename(tmp, name)
}
