package remote

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/engine"
)

// The phone must get its voice settings from the selected PC rather than
// invent a second, independently configured source.
func TestPhoneCanReadPCSpeechConfiguration(t *testing.T) {
	e := testEngine(t)
	r := Handle(e, e.Config().Snapshot().Remote, Request{ID: "voice-config", Op: "speech_config"}, "relay", "session")
	if !r.OK {
		t.Fatalf("PC voice configuration is unavailable to the phone: %+v", r)
	}
	if r.Speech == nil || r.Speech.Input.Enabled || r.Speech.Output.Enabled {
		t.Fatalf("voice directions must default off: %+v", r.Speech)
	}
}

func audioWAV() []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+320))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	for _, n := range []uint16{1, 1} {
		_ = binary.Write(&b, binary.LittleEndian, n)
	}
	for _, n := range []uint32{16000, 32000} {
		_ = binary.Write(&b, binary.LittleEndian, n)
	}
	for _, n := range []uint16{2, 16} {
		_ = binary.Write(&b, binary.LittleEndian, n)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(320))
	b.Write(make([]byte, 320))
	return b.Bytes()
}

func localVoiceEngine(t *testing.T, url string) *engine.Engine {
	t.Helper()
	e := catalogEngine(t)
	if err := e.Config().Update(func(next *config.Config) error {
		next.Speech.Input.Enabled, next.Speech.Output.Enabled = true, true
		next.Speech.Input.Source, next.Speech.Output.Source = config.SpeechSourceCustom, config.SpeechSourceCustom
		next.Speech.Input.BaseURL, next.Speech.Output.BaseURL = url, url
		next.Speech.Input.Model, next.Speech.Output.Model = "asr-fixture", "tts-fixture"
		next.Speech.Input.APIKey, next.Speech.Output.APIKey = "pc-private", "pc-private"
		next.Speech.Output.Format = "wav"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return e
}

func sendVoice(p *LinkPump, req Request) {
	raw, _ := json.Marshal(req)
	p.Dispatch(raw)
}

func voiceReply(t *testing.T, replies <-chan Response, id string) Response {
	t.Helper()
	select {
	case r := <-replies:
		if r.ID != id {
			t.Fatalf("reply id=%q, want %q: %+v", r.ID, id, r)
		}
		return r
	case <-time.After(3 * time.Second):
		t.Fatalf("voice operation %q did not respond", id)
		return Response{}
	}
}

func uploadVoice(t *testing.T, p *LinkPump, replies <-chan Response, id string, data []byte) {
	t.Helper()
	parts := (len(data) + PutChunkRaw - 1) / PutChunkRaw
	for part := 1; part <= parts; part++ {
		start, end := (part-1)*PutChunkRaw, min(part*PutChunkRaw, len(data))
		sendVoice(p, Request{ID: "upload", Op: OpPut, Purpose: "audio", PutID: id, Name: "capture.wav", MIME: "audio/wav", Part: part, Parts: parts, Data: base64.StdEncoding.EncodeToString(data[start:end])})
		if r := voiceReply(t, replies, "upload"); !r.OK || r.Put == nil || r.Put.Ready != (part == parts) {
			t.Fatalf("upload=%+v", r)
		}
	}
}

func TestLocalVoiceTransfersOriginalBytesWithoutCreatingConversationOrFile(t *testing.T) {
	wav := audioWAV()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer pc-private" {
			t.Error("voice credential missing")
		}
		if strings.HasSuffix(r.URL.Path, "transcriptions") {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			defer func() {
				if err := r.MultipartForm.RemoveAll(); err != nil {
					t.Error(err)
				}
			}()
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			defer file.Close()
			got, _ := io.ReadAll(file)
			if !bytes.Equal(got, wav) || r.FormValue("model") != "asr-fixture" {
				t.Error("ASR did not receive recording unchanged")
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"text":"speech becomes an editable draft"}`)
			return
		}
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav)
	}))
	defer server.Close()
	e := localVoiceEngine(t, server.URL+"/v1")
	p, replies := catalogPump(t, e)
	sendVoice(p, Request{ID: "config", Op: OpSpeechConfig})
	r := voiceReply(t, replies, "config")
	raw, _ := json.Marshal(r)
	if !r.OK || r.Speech == nil || !r.Speech.Input.Ready || !r.Speech.Output.Ready || bytes.Contains(raw, []byte("pc-private")) || bytes.Contains(raw, []byte(server.URL)) {
		t.Fatalf("unsafe/incomplete config=%s", raw)
	}
	uploadVoice(t, p, replies, "recording", wav)
	sendVoice(p, Request{ID: "recognize", Op: OpAudioTranscribe, AudioID: "recording", PutID: "recording"})
	if r := voiceReply(t, replies, "recognize"); !r.OK || r.Audio == nil || r.Audio.Text != "speech becomes an editable draft" {
		t.Fatalf("ASR result=%+v", r)
	}
	sendVoice(p, Request{ID: "replay", Op: OpAudioTranscribe, AudioID: "recording"})
	if r := voiceReply(t, replies, "replay"); r.OK || r.Code != "audio_replayed" {
		t.Fatalf("replayed ASR=%+v", r)
	}
	sendVoice(p, Request{ID: "speak", Op: OpAudioSpeech, AudioID: "playback", Text: "A finished answer."})
	if r := voiceReply(t, replies, "speak"); !r.OK || r.Audio == nil || r.Audio.MIME != "audio/wav" || r.Audio.Size != len(wav) {
		t.Fatalf("TTS result=%+v", r)
	}
	for range 2 {
		sendVoice(p, Request{ID: "chunk", Op: OpAudioChunk, AudioID: "playback"})
		r := voiceReply(t, replies, "chunk")
		if !r.OK || r.Audio == nil {
			t.Fatalf("chunk=%+v", r)
		}
		got, err := base64.StdEncoding.DecodeString(r.Audio.Data)
		if err != nil || !bytes.Equal(got, wav) || r.Audio.More || r.Audio.Next != int64(len(wav)) {
			t.Fatal("TTS bytes changed or last chunk cannot be retrieved again")
		}
	}
	sendVoice(p, Request{ID: "release", Op: OpAudioCancel, AudioID: "playback"})
	if r := voiceReply(t, replies, "release"); !r.OK {
		t.Fatalf("release=%+v", r)
	}
	sendVoice(p, Request{ID: "release-again", Op: OpAudioCancel, AudioID: "playback"})
	if r := voiceReply(t, replies, "release-again"); !r.OK {
		t.Fatal("audio release must be idempotent")
	}
	if calls.Load() != 2 {
		t.Fatalf("unexpected billable calls=%d", calls.Load())
	}
	list := Handle(e, e.Config().Snapshot().Remote, Request{ID: "list", Op: OpList}, "relay", "session")
	if !list.OK || len(list.Threads) != 0 {
		t.Fatal("voice created a conversation instead of editing the phone draft")
	}
	if p.stage != nil {
		t.Fatal("voice entered ordinary attachment staging")
	}
}

func TestGatewayVoiceCannotBeProxiedThroughPC(t *testing.T) {
	e := testEngine(t)
	p, replies := catalogPump(t, e)
	for _, op := range []string{OpAudioSpeech, OpAudioTranscribe, OpAudioChunk, OpPut} {
		sendVoice(p, Request{ID: op, Op: op, Purpose: "audio", AudioID: "voice", PutID: "voice", Text: "answer"})
		if r := voiceReply(t, replies, op); r.OK || r.Code != "audio_pc_local_required" {
			t.Fatalf("gateway request entered PC voice execution: %+v", r)
		}
	}
	r := Handle(e, e.Config().Snapshot().Remote, Request{Op: OpPut, Purpose: "audio"}, "relay", "session")
	if r.OK || r.Code != "bad_request" {
		t.Fatal("audio upload bypassed the connection-owned pool")
	}
}

func TestVoiceDoesNotBlockStopAndCancelReachesRealUpstream(t *testing.T) {
	entered, left := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(left)
	}))
	defer server.Close()
	e := localVoiceEngine(t, server.URL+"/v1")
	p, replies := catalogPump(t, e)
	sendVoice(p, Request{ID: "speak", Op: OpAudioSpeech, AudioID: "pending", Text: "answer"})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("voice did not reach HTTP")
	}
	sendVoice(p, Request{ID: "stop", Op: OpStop, ThreadID: "absent"})
	voiceReply(t, replies, "stop")
	sendVoice(p, Request{ID: "parallel", Op: OpAudioSpeech, AudioID: "another", Text: "answer"})
	if r := voiceReply(t, replies, "parallel"); r.Code != "audio_busy" {
		t.Fatalf("parallel=%+v", r)
	}
	sendVoice(p, Request{ID: "cancel-old", Op: OpAudioCancel, AudioID: "older"})
	if r := voiceReply(t, replies, "cancel-old"); !r.OK {
		t.Fatal("old cancellation failed")
	}
	select {
	case <-left:
		t.Fatal("old id cancelled the current request")
	default:
	}
	sendVoice(p, Request{ID: "cancel", Op: OpAudioCancel, AudioID: "pending"})
	if r := voiceReply(t, replies, "cancel"); !r.OK {
		t.Fatalf("cancel=%+v", r)
	}
	select {
	case <-left:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not receive cancellation")
	}
	if len(replies) != 0 {
		t.Fatal("cancelled voice published a late result")
	}
}

func TestClosingVoiceLinkCancelsAndJoinsHTTP(t *testing.T) {
	entered, left := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(left)
	}))
	defer server.Close()
	e := localVoiceEngine(t, server.URL+"/v1")
	p, replies := catalogPump(t, e)
	sendVoice(p, Request{ID: "speak", Op: OpAudioSpeech, AudioID: "pending", Text: "answer"})
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP not called")
	}
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not join owned voice request")
	}
	select {
	case <-left:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP was not cancelled")
	}
	sendVoice(p, Request{ID: "after-close", Op: OpAudioSpeech, AudioID: "new", Text: "answer"})
	if len(replies) != 0 {
		t.Fatal("closed voice link sent a late response")
	}
}

func TestVoiceRejectsMalformedAndIncompleteRequests(t *testing.T) {
	e := localVoiceEngine(t, "http://127.0.0.1:1/v1")
	p, replies := catalogPump(t, e)
	for _, tc := range []struct {
		req  Request
		code string
	}{
		{Request{V: ProtocolV + 1, Op: OpAudioSpeech, AudioID: "voice"}, "bad_version"},
		{Request{Op: OpAudioSpeech, AudioID: "../voice"}, "bad_request"},
		{Request{Op: OpAudioChunk, AudioID: "missing"}, "audio_not_found"},
		{Request{Op: OpAudioTranscribe, AudioID: "missing"}, "audio_not_found"},
		{Request{Op: OpAudioSpeech, AudioID: "empty", Text: " "}, "audio_text_limit"},
		{Request{Op: OpPut, Purpose: "audio", PutID: "bad", Name: "capture.wav", Part: 1, Parts: 1, Data: "not base64"}, "audio_invalid_upload"},
		{Request{Op: OpPut, Purpose: "other"}, "bad_request"},
	} {
		tc.req.ID = tc.code
		sendVoice(p, tc.req)
		if r := voiceReply(t, replies, tc.code); r.OK || r.Code != tc.code {
			t.Fatalf("invalid request=%+v, result=%+v", tc.req, r)
		}
	}
	sendVoice(p, Request{ID: "put", Op: OpPut, Purpose: "audio", PutID: "partial", Name: "capture.wav", MIME: "audio/wav", Part: 1, Parts: 2, Data: base64.StdEncoding.EncodeToString([]byte("partial"))})
	if r := voiceReply(t, replies, "put"); !r.OK {
		t.Fatal(r)
	}
	for _, tc := range []struct{ op, id, put, code string }{
		{OpAudioTranscribe, "partial", "other", "bad_request"},
		{OpAudioTranscribe, "partial", "partial", "audio_incomplete"},
		{OpAudioChunk, "partial", "", "audio_not_ready"},
		{OpAudioSpeech, "partial", "", "audio_busy"},
	} {
		sendVoice(p, Request{ID: tc.code, Op: tc.op, AudioID: tc.id, PutID: tc.put, Text: "answer"})
		if r := voiceReply(t, replies, tc.code); r.OK || r.Code != tc.code {
			t.Fatalf("partial result=%+v", r)
		}
	}
	sendVoice(p, Request{ID: "discard", Op: OpAudioCancel, AudioID: "partial"})
	voiceReply(t, replies, "discard")
}

func TestVoiceFailureAndSettingChangeDoNotPublishOldAudio(t *testing.T) {
	for _, settingChange := range []bool{false, true} {
		t.Run(map[bool]string{false: "upstream-error", true: "settings"}[settingChange], func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				close(entered)
				select {
				case <-release:
				case <-r.Context().Done():
				}
				w.WriteHeader(502)
				_, _ = io.WriteString(w, "private upstream payload")
			}))
			defer server.Close()
			e := localVoiceEngine(t, server.URL+"/v1")
			p, replies := catalogPump(t, e)
			defer once.Do(func() { close(release) })
			sendVoice(p, Request{ID: "speak", Op: OpAudioSpeech, AudioID: "pending", Text: "answer"})
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("HTTP not called")
			}
			want := "audio_upstream_failed"
			if settingChange {
				want = "audio_configuration_changed"
				if err := e.Config().Update(func(next *config.Config) error { next.Speech.Output.Enabled = false; return nil }); err != nil {
					t.Fatal(err)
				}
			} else {
				once.Do(func() { close(release) })
			}
			if r := voiceReply(t, replies, "speak"); r.OK || r.Code != want || strings.Contains(r.Error, "private") {
				t.Fatalf("failure=%+v", r)
			}
		})
	}
}

func TestVoiceOutputChunksAreBoundedAndLastChunkCanBeRetried(t *testing.T) {
	wav := audioWAV()
	wav = append(wav, make([]byte, PutChunkRaw*2)...)
	binary.LittleEndian.PutUint32(wav[4:8], uint32(len(wav)-8))
	binary.LittleEndian.PutUint32(wav[40:44], uint32(len(wav)-44))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		_, _ = w.Write(wav)
	}))
	defer server.Close()
	e := localVoiceEngine(t, server.URL+"/v1")
	p, replies := catalogPump(t, e)
	sendVoice(p, Request{ID: "speak", Op: OpAudioSpeech, AudioID: "large", Text: "answer"})
	if r := voiceReply(t, replies, "speak"); !r.OK {
		t.Fatal(r)
	}
	for _, offset := range []int64{-1, int64(len(wav))} {
		sendVoice(p, Request{ID: "invalid", Op: OpAudioChunk, AudioID: "large", Offset: offset})
		if r := voiceReply(t, replies, "invalid"); r.Code != "bad_request" {
			t.Fatal(r)
		}
	}
	var got []byte
	for offset := int64(0); offset < int64(len(wav)); {
		sendVoice(p, Request{ID: "chunk", Op: OpAudioChunk, AudioID: "large", Offset: offset})
		r := voiceReply(t, replies, "chunk")
		if !r.OK || r.Audio == nil || r.Audio.Offset != offset {
			t.Fatal(r)
		}
		raw, _ := json.Marshal(r)
		part, err := base64.StdEncoding.DecodeString(r.Audio.Data)
		if err != nil || len(part) > PutChunkRaw || len(raw) > plaintextBudget() {
			t.Fatal("audio exceeded sealed frame budget")
		}
		got = append(got, part...)
		offset = r.Audio.Next
	}
	if !bytes.Equal(got, wav) {
		t.Fatal("audio changed across frame boundaries")
	}
}

func TestVoiceTemporaryUploadsExpireAndDoNotOccupyNextRecording(t *testing.T) {
	e := localVoiceEngine(t, "http://127.0.0.1:1/v1")
	if err := e.Config().Update(func(next *config.Config) error {
		next.Speech.Limits.StagingTTLSeconds = 1
		next.Speech.Limits.InputBytes = 64
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	p, replies := catalogPump(t, e)
	sendVoice(p, Request{ID: "over", Op: OpPut, Purpose: "audio", PutID: "over", Name: "capture.wav", Part: 1, Parts: 1, Data: base64.StdEncoding.EncodeToString(make([]byte, 65))})
	if r := voiceReply(t, replies, "over"); r.Code != "audio_invalid_upload" {
		t.Fatal(r)
	}
	uploadVoice(t, p, replies, "waiting", []byte("pending"))
	deadline := time.Now().Add(2 * time.Second)
	for {
		p.audioMu.Lock()
		empty := p.audioJob == nil
		p.audioMu.Unlock()
		if empty {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expired audio retained private recording")
		}
		time.Sleep(10 * time.Millisecond)
	}
	uploadVoice(t, p, replies, "new", []byte("new recording"))
	p.audioMu.Lock()
	_, old := p.audioSeen["over"]
	p.audioMu.Unlock()
	if old {
		t.Fatal("expired terminal receipt accumulated")
	}
}

func TestVoiceConcurrentCloseAndAdmissionDoesNotLeakOwnedWork(t *testing.T) {
	e := localVoiceEngine(t, "http://127.0.0.1:1/v1")
	p, _ := catalogPump(t, e)
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				p.Close()
			} else {
				sendVoice(p, Request{Op: OpAudioSpeech, AudioID: "attempt", Text: "answer"})
			}
		}()
	}
	wg.Wait()
	p.Close()
	p.audioMu.Lock()
	defer p.audioMu.Unlock()
	if !p.audioClosed || p.audioJob != nil {
		t.Fatal("closed link retained a voice request")
	}
}

func TestOversizedTranscriptionIsRefusedInsteadOfSilentlyTruncated(t *testing.T) {
	text := strings.Repeat("spoken text ", 7000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"text": text})
	}))
	defer server.Close()
	e := localVoiceEngine(t, server.URL+"/v1")
	p, replies := catalogPump(t, e)
	uploadVoice(t, p, replies, "long-input", audioWAV())
	sendVoice(p, Request{ID: "recognize", Op: OpAudioTranscribe, AudioID: "long-input"})
	if r := voiceReply(t, replies, "recognize"); r.OK || r.Code != "audio_result_limit" || r.Audio != nil {
		t.Fatalf("oversized transcription was truncated or exceeded the sealed frame: %+v", r)
	}
}

func TestVoiceRequiresEachDirectionAndAReadyEngine(t *testing.T) {
	if speechSnapshot(nil) != nil {
		t.Fatal("missing PC must not advertise speech")
	}
	e := localVoiceEngine(t, "http://127.0.0.1:1/v1")
	p, replies := catalogPump(t, e)
	for _, input := range []bool{true, false} {
		if err := e.Config().Update(func(next *config.Config) error {
			next.Speech.Input.Enabled, next.Speech.Output.Enabled = !input, input
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		op, want := OpAudioSpeech, "audio_output_unavailable"
		if input {
			op, want = OpAudioTranscribe, "audio_input_unavailable"
		}
		sendVoice(p, Request{ID: want, Op: op, AudioID: "off", Text: "answer"})
		if r := voiceReply(t, replies, want); r.Code != want {
			t.Fatal(r)
		}
	}
	e.BeginRestore()
	sendVoice(p, Request{ID: "restore", Op: OpAudioSpeech, AudioID: "restore", Text: "answer"})
	if r := voiceReply(t, replies, "restore"); r.Code != "restoring" {
		t.Fatal(r)
	}
	e.FinishRestore()
}

func TestRenewedVoiceUploadSurvivesAnOldExpiryAlreadyWaitingForTheLock(t *testing.T) {
	e := localVoiceEngine(t, "http://127.0.0.1:1/v1")
	if err := e.Config().Update(func(next *config.Config) error { next.Speech.Limits.StagingTTLSeconds = 1; return nil }); err != nil {
		t.Fatal(err)
	}
	p, replies := catalogPump(t, e)
	uploadVoice(t, p, replies, "renewed", []byte("recorded bytes"))
	p.audioMu.Lock()
	j := p.audioJob
	// A socket action can hold admission when the old timer becomes runnable.
	// Renew through the actual upload handler before that old callback gets it.
	time.Sleep(1100 * time.Millisecond)
	p.putAudioLocked(Request{ID: "renew", Op: OpPut, Purpose: "audio", PutID: j.id, Name: "capture.wav", MIME: "audio/wav", Parts: 1, Part: 1, Data: base64.StdEncoding.EncodeToString([]byte("recorded bytes"))}, j)
	p.audioMu.Unlock()
	if r := voiceReply(t, replies, "renew"); !r.OK {
		t.Fatal(r)
	}
	time.Sleep(30 * time.Millisecond)
	p.audioMu.Lock()
	alive := p.audioJob == j
	p.audioMu.Unlock()
	if !alive {
		t.Fatal("the previous timer deleted a recording whose idle deadline had just been renewed")
	}
}

func TestEscapedVoiceConfigurationCannotOverflowThePhoneFrame(t *testing.T) {
	e := localVoiceEngine(t, "http://127.0.0.1:1/v1")
	if err := e.Config().Update(func(next *config.Config) error {
		next.Speech.Input.Prompt = strings.Repeat("<", 8192)
		next.Speech.Output.Instructions = strings.Repeat(">", 8192)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{OpSpeechConfig, OpMeta, OpCatalog} {
		t.Run(op, func(t *testing.T) {
			var got Response
			var frameSize int
			p := &LinkPump{eng: e, send: func(raw []byte) error {
				frameSize = len(raw)
				if frameSize > plaintextBudget() {
					return fmt.Errorf("sealed frame too large")
				}
				return json.Unmarshal(raw, &got)
			}}
			defer p.Close()
			sendVoice(p, Request{ID: "config-frame", Op: op})
			if frameSize > plaintextBudget() {
				t.Fatalf("valid voice parameters overflowed %s frame: %d bytes", op, frameSize)
			}
			if op == OpSpeechConfig {
				if got.OK || got.Code != "audio_config_limit" {
					t.Fatalf("config limit must be explicit: %+v", got)
				}
			} else if !got.OK || got.Speech != nil {
				t.Fatalf("voice configuration blocked ordinary PC metadata: %+v", got)
			}
		})
	}
}

func TestVoiceUploadRefusesANameThatWouldOverflowTheAcknowledgement(t *testing.T) {
	e := localVoiceEngine(t, "http://127.0.0.1:1/v1")
	var got Response
	var frameSize int
	p := &LinkPump{eng: e, send: func(raw []byte) error {
		frameSize = len(raw)
		if frameSize > plaintextBudget() {
			return fmt.Errorf("sealed frame too large")
		}
		return json.Unmarshal(raw, &got)
	}}
	defer p.Close()
	var request bytes.Buffer
	encoder := json.NewEncoder(&request)
	encoder.SetEscapeHTML(false) // The phone uses JSON.stringify.
	if err := encoder.Encode(Request{ID: "upload-name", Op: OpPut, Purpose: "audio", PutID: "recording", Name: strings.Repeat("<", 11000) + ".wav", MIME: "audio/wav", Part: 1, Parts: 1, Data: base64.StdEncoding.EncodeToString(audioWAV())}); err != nil {
		t.Fatal(err)
	}
	if request.Len() > plaintextBudget() {
		t.Fatal("fixture request must fit the actual encrypted link")
	}
	p.Dispatch(request.Bytes())
	if frameSize > plaintextBudget() || got.OK || got.Code != "audio_invalid_upload" {
		t.Fatalf("voice upload acknowledgement overflowed: bytes=%d code=%s", frameSize, got.Code)
	}
}

func TestVoiceReceiptBudgetAndRequestIdentityStayBounded(t *testing.T) {
	e := localVoiceEngine(t, "http://127.0.0.1:1/v1")
	p, replies := catalogPump(t, e)
	sendVoice(p, Request{ID: strings.Repeat("<", 300), Op: OpAudioSpeech, AudioID: "oversized-id", Text: "answer"})
	if r := voiceReply(t, replies, ""); r.OK || r.Code != "bad_request" {
		t.Fatal("unbounded caller identity entered the voice response")
	}
	for i := range maxAudioReceipts {
		sendVoice(p, Request{ID: "empty", Op: OpAudioSpeech, AudioID: fmt.Sprintf("receipt-%d", i), Text: ""})
		if r := voiceReply(t, replies, "empty"); r.OK || r.Code != "audio_text_limit" {
			t.Fatal(r)
		}
	}
	sendVoice(p, Request{ID: "full", Op: OpAudioSpeech, AudioID: "next", Text: "answer"})
	if r := voiceReply(t, replies, "full"); r.OK || r.Code != "audio_busy" {
		t.Fatal("replay receipt memory exceeded the per-link budget")
	}
	p.Close()
	sendVoice(p, Request{V: ProtocolV + 1, ID: "closed", Op: OpAudioSpeech, AudioID: "invalid"})
	sendVoice(p, Request{ID: "closed-cancel", Op: OpAudioCancel, AudioID: "receipt-0"})
	if len(replies) != 0 {
		t.Fatal("closed voice admission still published an error or cancel response")
	}
}
