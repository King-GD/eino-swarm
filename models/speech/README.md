# Streaming speech playback protocol

`StreamingPlayback.tla` checks incremental PCM playback and ownership. It is an
independent model, not registered in `tools/model_check.py`; the existing
`make model-check` command does not run it.

A frame in this model means one complete scheduling piece of PCM (at most
250 ms), not a byte or one transport read. `QueueLimit = 4` matches the
player's maximum scheduled sources. `Frames = 6` permits the producer to
outpace that queue. It also permits an in-flight read to complete after
cancellation. Partial PCM samples, SSE framing, byte budgets and MIME validation
remain implementation-test obligations. This model follows one admitted
playback owner; refusal of a stale signal from a previous owner remains an
implementation regression, not a multiple-owner proof here.

The model separates HTTP/model admission ownership from playback and authority
ownership. `requestHeld` represents the upstream Go model admission hold: it
survives response headers and closes with the HTTP source after EOF or
cancellation. `authorityWatch` represents the mobile stream's retained authority
observer and abort connection. It survives successful HTTP EOF and remains until
the player explicitly calls `audio.cancel()` after its scheduled sources finish.
An expiry during that tail must abort the stream's exposed signal and stop the
same playback owner, without requiring a global account/refusal event. No model
admission lock is required while only the already received audio tail remains.

`Stop` abstracts completed propagation of a user cancellation or an observed
configuration/authority invalidation. The model does not claim synchronous
network cleanup, a revocation detection deadline, browser scheduling latency or
audio-device output. `ProgressSpec` assumes a finite successful producer and
fair delivery of the listed events; it is not a timeout guarantee for an
upstream that never responds. Native playback and actual cancellation must
still be accepted through their implementation boundaries.

## Mapping to implementation

| Model action/state | Implementation boundary | Relevant regression |
| --- | --- | --- |
| `AcceptHeaders`, `requestHeld`, `CloseSource` | `internal/speech/stream.go`: `SynthesizeStream` / `requestStream.Close`; `frontend/src/lib/speech-stream.ts`: body reader close | `TestPCMStreamHoldsModelOwnershipUntilIdempotentClose`; body consumption and closure |
| `BeginRead`, `CompleteRead`, late-result discard | `frontend/src/lib/speech-stream.ts`: reader ownership and abort checks; Go `requestStream.Read` checks before and after the actual read | reader abort / early consumer exit; `TestPCMStreamCancellationInterruptsAnAlreadyOpenRead` |
| immediate scheduling, queue bound, `PlayFrame` | `frontend/src/lib/speech-media.ts`: `playPCM`, scheduled sources, `onended` | starts playback before synthesis finishes; drains queued sources |
| `Cancel`, queue discard, `playbackHeld` | `speech-media.ts`: `stop`; `mobile/src/lib/phone-speech.ts`: retained request set and `cancelPhoneSpeech` | cancels every queued source; an old owner cannot stop a new owner |
| `Revoke`, `authorityWatch`, `tailRevocation` | `mobile/src/lib/gateway-authority.ts`: current-authority/expiry/refusal observer; `speech-transport.ts` and `phone-speech.ts`: retain stream ownership until explicit cancel; `SpeechAudioStream.signal` and `playPCM`: abort only the current playback owner | logout after headers; expiry after successful HTTP EOF with queued audio; configuration and pairing invalidation |

## Independent TLC invocation

Use the pinned `tla2tools.jar` located by `tools/model_check.py` (v1.7.4,
SHA-256 `936a262061c914694dfd669a543be24573c45d5aa0ff20a8b96b23d01e050e88`).
Create private evidence files with mode `0600`, preserving the subprocess
umask. Run in a fresh directory outside the checkout, with its own Java temporary
and TLC state directories, retaining the full command, configuration, stdout,
stderr and exit status. The following is the normal safety configuration:

```sh
speech_model_evidence=$(mktemp -d /tmp/zwai-speech-model.XXXXXX)
install -m 600 models/speech/StreamingPlayback.tla models/speech/StreamingPlayback.cfg "$speech_model_evidence/"
mkdir "$speech_model_evidence/java-tmp"
java -Djava.io.tmpdir="$speech_model_evidence/java-tmp" -XX:+UseParallelGC \
  -cp "$HOME/.cache/tlaplus/v1.7.4/tla2tools.jar" tlc2.TLC \
  -deadlock -workers 1 -metadir "$speech_model_evidence/states" \
  -config "$speech_model_evidence/StreamingPlayback.cfg" \
  "$speech_model_evidence/StreamingPlayback.tla"
```

`-deadlock` disables terminal-state deadlock reporting, as in the repository's
existing runner. A successful safety run must exit zero and report completed
checking with no error. For a separate progress check, change the copied cfg's
specification to `ProgressSpec` and append `PROPERTY EventuallySettled`.

For every row below, create another private copied module/config directory.
Turn on only the named switch; replace `INVARIANTS` with `TypeOK` and that
row's expected invariant. Each run must exit nonzero and explicitly report
**that invariant** violated. A parser error, timeout or unrelated violation is
not acceptance. `BufferUntilEOF` reproduces the pre-streaming complete-Blob
behavior. `ReleaseWatchAtEOF` reproduces the reviewed premature release in the
initial streaming implementation: HTTP succeeds, the expiry observer is removed,
then pure expiry leaves queued audio playing. Other switches are seeded faults,
not claims about shipped bugs.

| Switch enabled | Expected violated invariant |
| --- | --- |
| `BufferUntilEOF` | `FirstChunkStartsBeforeEOF` |
| `UnboundedQueue` | `QueueBounded` |
| `ReleaseAtHeaders` | `SourceOwnsLease` |
| `IgnoreCancellation` | `CancelledClosesSource` |
| `IgnoreRevocation` | `RevokedClosesSource` |
| `KeepQueuedOnStop` | `TerminalQueueIsEmpty` |
| `AcceptLateChunk` | `TerminalQueueIsEmpty` |
| `ReleaseWatchAtEOF` | `TailRevocationClearsQueue` |

Finally, leave all switches false and separately check `TypeOK` plus each
negative invariant below. **All seven must be violated**. Their traces witness
useful behavior and prevent a safety result accepted only because no audio was
consumed:

- `NeverEarlyPlayback`: scheduled PCM exists while EOF has not arrived.
- `NeverBackpressure`: four sources occupy the queue and more input remains.
- `NeverCancelledPendingRead`: a read is in flight when cancellation settles.
- `NeverRevokedQueuedAudio`: revocation discards already scheduled audio.
- `NeverDrainAfterClose`: the source has closed while the playback owner still
  holds an audible tail.
- `NeverRevokedQueuedTail`: after successful HTTP EOF and source closure, pure
  authority expiry stops playback and discards its queued tail.
- `NeverFinished`: finite successful input is completely played and released.

The acceptance matrix has 17 cases: normal safety, eight faults, seven reachability
witnesses and one fair-progress check. Run it again after implementation tests
pass, bound to the final candidate's HEAD, complete working-tree difference,
untracked input hashes, model/config hashes and the exact implementation files
listed above. Source changes invalidate a prior acceptance receipt. Preparation
results are not final candidate acceptance or native playback evidence.
