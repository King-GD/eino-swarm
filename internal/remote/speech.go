package remote

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LubyRuffy/eino-swarm/internal/engine"
	"github.com/LubyRuffy/eino-swarm/internal/speech"
)

// AudioView is an ephemeral, connection-owned voice result, never a file or
// transcript event. Offset and Next count decoded bytes, not base64 characters.
type AudioView struct {
	AudioID string `json:"audio_id"`
	Text    string `json:"text,omitempty"`
	MIME    string `json:"mime,omitempty"`
	Size    int    `json:"size,omitempty"`
	Offset  int64  `json:"offset"`
	Next    int64  `json:"next"`
	Data    string `json:"data,omitempty"`
	More    bool   `json:"more"`
}

type audioJob struct {
	id        string
	revision  [32]byte
	stage     *Staging
	view      speech.SnapshotView
	timer     *time.Timer
	expiry    uint64
	running   bool
	cancelled bool
	cancel    context.CancelFunc
	done      chan struct{}
	data      []byte
	mime      string
}

// Bounded terminal receipts stop replayed starts from making another billable
// model call. Reconnection never resumes or automatically repeats voice work.
const maxAudioReceipts = 64

func speechSnapshot(eng *engine.Engine) *speech.SnapshotView {
	if eng == nil || eng.Config() == nil || eng.Providers() == nil {
		return nil
	}
	view := speech.New(eng.Config(), eng.Providers()).Snapshot()
	return &view
}

// JSON escapes can expand valid prompt parameters beyond one sealed frame.
// Voice must not prevent reading the rest of the PC's metadata/catalog.
func boundSpeechResponse(resp Response, strict bool) Response {
	if resp.Speech == nil {
		return resp
	}
	raw, err := json.Marshal(resp)
	if err == nil && len(raw) <= maxCatalogFrame {
		return resp
	}
	if strict {
		return fail(resp.ID, resp.Path, resp.SessionID, "audio_config_limit", "Voice configuration does not fit one encrypted frame.")
	}
	resp.Speech, resp.SpeechError = nil, "audio_config_limit"
	return resp
}

func (p *LinkPump) audioRevision() [32]byte {
	cfg := p.eng.Config().Snapshot()
	// Hash private configuration only in memory: credential replacement must
	// revoke a pending call even when its public model label did not change.
	return sha256.Sum256([]byte(fmt.Sprintf("%#v:%#v:%#v", cfg.Product, cfg.Speech, cfg.Models)))
}

func (p *LinkPump) audioAdmission(input bool) (*speech.SnapshotView, string) {
	view := speechSnapshot(p.eng)
	if view == nil || view.Execution != "pc_local" || p.eng.Providers().OfficialOnly() {
		return nil, "audio_pc_local_required"
	}
	if err := p.eng.RequireReady(); err != nil {
		return nil, "restoring"
	}
	if err := p.eng.RejectIfBackup(); err != nil {
		return nil, "backup_busy"
	}
	if input && (!view.Input.Enabled || !view.Input.Ready) {
		return nil, "audio_input_unavailable"
	}
	if !input && (!view.Output.Enabled || !view.Output.Ready) {
		return nil, "audio_output_unavailable"
	}
	return view, ""
}

func (p *LinkPump) audioFailure(req Request, code string) Response {
	return fail(req.ID, p.path, p.sessionID, code, "This PC voice request is unavailable. Check Voice settings on the PC.")
}

func (p *LinkPump) refuseAudio(req Request, code string) {
	p.audioMu.Lock()
	defer p.audioMu.Unlock()
	if !p.audioClosed {
		p.reply(p.audioFailure(req, code))
	}
}

func (p *LinkPump) dispatchAudio(req Request) {
	if len(req.ID) > 256 {
		req.ID = ""
		p.refuseAudio(req, "bad_request")
		return
	}
	if req.V != 0 && req.V != ProtocolV {
		p.refuseAudio(req, "bad_version")
		return
	}
	id := req.AudioID
	if req.Op == OpPut {
		id = req.PutID
	}
	if !putIDOK(id) || strings.TrimSpace(id) != id {
		p.refuseAudio(req, "bad_request")
		return
	}
	if req.Op == OpAudioCancel {
		p.cancelAudio(req, id)
		return
	}
	input := req.Op == OpPut || req.Op == OpAudioTranscribe
	view, code := p.audioAdmission(input)
	if code != "" {
		p.refuseAudio(req, code)
		return
	}
	p.audioMu.Lock()
	defer p.audioMu.Unlock()
	if p.audioClosed {
		return
	}
	p.pruneAudioReceiptsLocked()
	if p.audioJob != nil && p.audioJob.id != id {
		p.reply(p.audioFailure(req, "audio_busy"))
		return
	}
	if p.audioJob == nil {
		if _, seen := p.audioSeen[id]; seen {
			p.reply(p.audioFailure(req, "audio_replayed"))
			return
		}
		if len(p.audioSeen) >= maxAudioReceipts {
			p.reply(p.audioFailure(req, "audio_busy"))
			return
		}
		if req.Op != OpPut && req.Op != OpAudioSpeech {
			p.reply(p.audioFailure(req, "audio_not_found"))
			return
		}
		p.audioJob = &audioJob{id: id, revision: p.audioRevision(), view: *view}
		p.audioJob.stage = &Staging{puts: map[string]*partial{}, byteLimit: int(view.Limits.InputBytes), putLimit: 1}
	}
	j := p.audioJob
	if j.cancelled || j.revision != p.audioRevision() {
		p.reply(p.audioFailure(req, "audio_configuration_changed"))
		return
	}
	switch req.Op {
	case OpPut:
		p.putAudioLocked(req, j)
	case OpAudioTranscribe:
		if req.PutID != "" && req.PutID != id {
			p.reply(p.audioFailure(req, "bad_request"))
			return
		}
		if j.running || j.data != nil {
			p.reply(p.audioFailure(req, "audio_busy"))
			return
		}
		blobs, err := j.stage.Take([]string{id})
		if err != nil {
			p.reply(p.audioFailure(req, "audio_incomplete"))
			return
		}
		p.startAudioLocked(req, j, &speech.Audio{Filename: blobs[0].Name, MIME: blobs[0].MIME, Data: blobs[0].Data})
	case OpAudioSpeech:
		if j.running || j.data != nil || j.stage.bytes != 0 {
			p.reply(p.audioFailure(req, "audio_busy"))
			return
		}
		if strings.TrimSpace(req.Text) == "" || !utf8.ValidString(req.Text) || utf8.RuneCountInString(req.Text) > view.Limits.ChunkChars {
			p.reply(p.audioFailure(req, "audio_text_limit"))
			p.retireAudioLocked(j)
			return
		}
		p.startAudioLocked(req, j, nil)
	case OpAudioChunk:
		p.readAudioLocked(req, j)
	}
}

func (p *LinkPump) putAudioLocked(req Request, j *audioJob) {
	if j.running || j.data != nil {
		p.reply(p.audioFailure(req, "audio_busy"))
		return
	}
	if len(req.Name) > 256 {
		p.reply(p.audioFailure(req, "audio_invalid_upload"))
		if j.stage.bytes == 0 {
			p.retireAudioLocked(j)
		}
		return
	}
	view, err := j.stage.Accept(req)
	if err != nil {
		p.reply(p.audioFailure(req, "audio_invalid_upload"))
		if j.stage.bytes == 0 {
			p.retireAudioLocked(j)
		}
		return
	}
	p.armAudioExpiryLocked(j)
	resp := okBase(req.ID, p.path, p.sessionID)
	resp.Put = &view
	p.replyAudioLocked(req, resp)
}

func (p *LinkPump) readAudioLocked(req Request, j *audioJob) {
	if j.running || j.data == nil {
		p.reply(p.audioFailure(req, "audio_not_ready"))
		return
	}
	if req.Offset < 0 || req.Offset >= int64(len(j.data)) {
		p.reply(p.audioFailure(req, "bad_request"))
		return
	}
	end := min(req.Offset+PutChunkRaw, int64(len(j.data)))
	resp := okBase(req.ID, p.path, p.sessionID)
	resp.Audio = &AudioView{AudioID: j.id, MIME: j.mime, Size: len(j.data), Offset: req.Offset, Next: end,
		Data: base64.StdEncoding.EncodeToString(j.data[req.Offset:end]), More: end < int64(len(j.data))}
	p.armAudioExpiryLocked(j)
	p.replyAudioLocked(req, resp)
}

func (p *LinkPump) startAudioLocked(req Request, j *audioJob, input *speech.Audio) {
	if j.timer != nil {
		j.timer.Stop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	j.cancel, j.done, j.running = cancel, make(chan struct{}), true
	p.audioWG.Add(1)
	go p.runAudio(ctx, req, j, input)
}

func (p *LinkPump) runAudio(ctx context.Context, req Request, j *audioJob, input *speech.Audio) {
	defer p.audioWG.Done()
	defer close(j.done)
	defer j.cancel()
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if j.revision != p.audioRevision() {
					j.cancel()
					return
				}
			}
		}
	}()
	service := speech.New(p.eng.Config(), p.eng.Providers())
	var text string
	var audio speech.Audio
	var err error
	if input != nil {
		text, err = service.Transcribe(ctx, *input)
	} else {
		audio, err = service.Synthesize(ctx, req.Text)
	}
	j.cancel()
	<-watchDone
	p.audioMu.Lock()
	defer p.audioMu.Unlock()
	if p.audioClosed || j.cancelled || p.audioJob != j {
		return
	}
	if j.revision != p.audioRevision() {
		p.reply(p.audioFailure(req, "audio_configuration_changed"))
		p.retireAudioLocked(j)
		return
	}
	if err != nil {
		p.reply(p.audioFailure(req, "audio_upstream_failed"))
		p.retireAudioLocked(j)
		return
	}
	resp := okBase(req.ID, p.path, p.sessionID)
	resp.Audio = &AudioView{AudioID: j.id, Text: text}
	if input == nil {
		j.data, j.mime = audio.Data, audio.MIME
		resp.Audio.MIME, resp.Audio.Size = audio.MIME, len(audio.Data)
	}
	p.replyAudioLocked(req, resp)
	j.running = false
	if input != nil {
		p.retireAudioLocked(j)
	} else {
		p.armAudioExpiryLocked(j)
	}
}

func (p *LinkPump) replyAudioLocked(req Request, resp Response) {
	raw, err := json.Marshal(withHost(resp, p.cfg))
	if err != nil || len(raw) > plaintextBudget() {
		p.reply(p.audioFailure(req, "audio_result_limit"))
		return
	}
	p.sendRaw(raw)
}

func (p *LinkPump) cancelAudio(req Request, id string) {
	p.audioMu.Lock()
	if p.audioClosed {
		p.audioMu.Unlock()
		return
	}
	j := p.audioJob
	if j == nil || j.id != id {
		resp := okBase(req.ID, p.path, p.sessionID)
		resp.Audio = &AudioView{AudioID: id}
		p.reply(resp)
		p.audioMu.Unlock()
		return
	}
	j.cancelled = true
	if j.timer != nil {
		j.timer.Stop()
	}
	if j.cancel != nil {
		j.cancel()
	}
	if j.done == nil {
		p.retireAudioLocked(j)
		resp := okBase(req.ID, p.path, p.sessionID)
		resp.Audio = &AudioView{AudioID: id}
		p.reply(resp)
		p.audioMu.Unlock()
		return
	}
	p.audioWG.Add(1)
	p.audioMu.Unlock()
	go func() {
		defer p.audioWG.Done()
		<-j.done
		p.audioMu.Lock()
		defer p.audioMu.Unlock()
		if p.audioJob == j {
			p.retireAudioLocked(j)
		}
		if p.audioClosed {
			return
		}
		resp := okBase(req.ID, p.path, p.sessionID)
		resp.Audio = &AudioView{AudioID: id}
		p.reply(resp)
	}()
}

func (p *LinkPump) armAudioExpiryLocked(j *audioJob) {
	j.expiry++
	expiry := j.expiry
	if j.timer != nil {
		j.timer.Stop()
	}
	j.timer = time.AfterFunc(time.Duration(j.view.Limits.StagingTTLSeconds)*time.Second, func() {
		p.audioMu.Lock()
		defer p.audioMu.Unlock()
		if p.audioJob == j && !j.running && j.expiry == expiry {
			p.retireAudioLocked(j)
		}
	})
}

func (p *LinkPump) retireAudioLocked(j *audioJob) {
	if j.timer != nil {
		j.timer.Stop()
	}
	if p.audioSeen == nil {
		p.audioSeen = make(map[string]time.Time)
	}
	p.audioSeen[j.id] = time.Now().Add(time.Duration(j.view.Limits.StagingTTLSeconds) * time.Second)
	j.data, j.stage = nil, nil
	if p.audioJob == j {
		p.audioJob = nil
	}
}

func (p *LinkPump) pruneAudioReceiptsLocked() {
	now := time.Now()
	for id, until := range p.audioSeen {
		if !until.After(now) {
			delete(p.audioSeen, id)
		}
	}
}

func (p *LinkPump) stopAudio() {
	p.audioMu.Lock()
	p.audioClosed = true
	if j := p.audioJob; j != nil {
		j.cancelled = true
		if j.timer != nil {
			j.timer.Stop()
		}
		if j.cancel != nil {
			j.cancel()
		}
	}
	p.audioMu.Unlock()
	p.audioWG.Wait()
	p.audioMu.Lock()
	if j := p.audioJob; j != nil {
		p.retireAudioLocked(j)
	}
	p.audioSeen = nil
	p.audioMu.Unlock()
}
