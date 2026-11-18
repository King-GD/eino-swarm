package remote

import (
	"encoding/base64"
	"errors"
	"io"

	"github.com/LubyRuffy/eino-swarm/internal/engine"
)

func handleGeneratedImageChunk(eng *engine.Engine, req Request, path, sessionID string) Response {
	if req.Before < 0 {
		return fail(req.ID, path, sessionID, "bad_request", "image offset is negative")
	}
	file, mime, err := eng.OpenGeneratedImage(req.ThreadID, req.ImageID)
	if err != nil {
		return mapErr(req.ID, path, sessionID, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return mapErr(req.ID, path, sessionID, err)
	}
	if req.Before > info.Size() {
		return fail(req.ID, path, sessionID, "bad_request", "image offset is beyond its end")
	}
	data := make([]byte, FileChunkRaw)
	n, err := file.ReadAt(data, req.Before)
	if err != nil && !errors.Is(err, io.EOF) {
		return mapErr(req.ID, path, sessionID, err)
	}
	next := req.Before + int64(n)
	resp := okBase(req.ID, path, sessionID)
	resp.FileChunk = &FileChunkView{
		Path: req.ImageID, Size: info.Size(), MIME: mime,
		Offset: req.Before, NextOffset: next, Data: base64.StdEncoding.EncodeToString(data[:n]),
		More: next < info.Size(),
	}
	return resp
}
