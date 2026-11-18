package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"github.com/LubyRuffy/eino-swarm/internal/store"
)

// Store generated media alongside the thread's input tree so backup, restore
// and thread deletion retain their existing scope.
func (e *Engine) generatedImagesDir(threadID string) string {
	return filepath.Join(e.cfg.ThreadInputsDir(threadID), "generated")
}

func (e *Engine) commitGeneratedImage(ctx context.Context, stage store.Event, image provider.GeneratedImage) (store.ImageRef, error) {
	ext := map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp"}[image.MIME]
	if ext == "" {
		return store.ImageRef{}, errors.New("unsupported generated image type")
	}
	ref := store.ImageRef{ID: store.NewID("img_"), Name: "generated-image" + ext, MIME: image.MIME, Width: image.Width, Height: image.Height}
	// Rewind and DeleteThread must not purge this tree between file creation
	// and the matching event or let a late writer recreate deleted media.
	e.recordMu.Lock()
	defer e.recordMu.Unlock()
	if _, dropped := e.droppedTurns[stage.TurnID]; dropped {
		return store.ImageRef{}, store.ErrNotFound
	}
	if _, err := e.store.GetThreadContext(ctx, stage.ThreadID); err != nil {
		return store.ImageRef{}, err
	}
	dir := e.generatedImagesDir(stage.ThreadID)
	path, err := writeGeneratedImage(dir, ref.ID, image.Data)
	if err != nil {
		return store.ImageRef{}, fmt.Errorf("write generated image: %w", err)
	}
	stage.Text = "completed"
	stage.MessageID = ref.ID
	stage.Images = []store.ImageRef{ref}
	if err := e.appendImageEventLocked(ctx, stage); err != nil {
		if removeErr := os.Remove(path); removeErr != nil {
			e.log.Warn("could not remove unpublished image", "thread", stage.ThreadID, "image", ref.ID, "err", removeErr)
		}
		return store.ImageRef{}, err
	}
	return ref, nil
}

func writeGeneratedImage(dir, id string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, ".generated-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return "", err
	}
	path := filepath.Join(dir, id)
	if err := os.Rename(file.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

// OpenGeneratedImage opens a committed image from this conversation. The
// caller owns the returned descriptor and may read ranges before closing it.
func (e *Engine) OpenGeneratedImage(threadID, imageID string) (*os.File, string, error) {
	if !safeImageID(imageID) {
		return nil, "", store.ErrNotFound
	}
	ref, err := e.store.GeneratedImageRef(threadID, imageID)
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(e.generatedImagesDir(threadID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", store.ErrNotFound
		}
		return nil, "", fmt.Errorf("open conversation media: %w", err)
	}
	defer func() { _ = root.Close() }()
	file, err := root.Open(imageID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", store.ErrNotFound
		}
		return nil, "", fmt.Errorf("open generated image: %w", err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > int64(maxImageBytes) {
		_ = file.Close()
		return nil, "", store.ErrNotFound
	}
	var prefix [512]byte
	n, err := file.ReadAt(prefix[:], 0)
	if err != nil && !errors.Is(err, io.EOF) || sniffImageMIME(prefix[:n]) != ref.MIME {
		_ = file.Close()
		return nil, "", store.ErrNotFound
	}
	return file, ref.MIME, nil
}
