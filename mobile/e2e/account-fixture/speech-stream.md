# Native streaming speech fixture

Use a fresh owned account-fixture process with **`--speech-stream`** and a
fresh app container for this case. Without this explicit flag, the ordinary
fixture keeps its scripted mock pool and setup refuses speech acceptance.
The flag selects a real account model pool against the same loopback listener;
a separate PC-authenticated desktop chat endpoint completes the seed answer. The
fixture supplies synthetic CAS authorization, real loopback HTTP and Pairlink,
and a real PC speech-config projection. It does not contact production models.
Its default account behavior is unchanged until the test explicitly POSTs
`/fixture/speech/setup`.

Setup enables the managed PC speech role, selects PCM, binds `gateway_origin`
to the actual fixture listener and starts a real loopback-backed turn in **Native speech
stream**. The phone logs in through the actual browser; its fresh account automatically connects to the only online PC.
The audio request must use **`/v1/audio/speech` with the phone credential**.
The separately supported `/client/desktop/v1/audio/speech` accepts only the
fixture PC credential; it cannot substitute for mobile acceptance.

POST `/fixture/speech/mode?value=raw-held`, `sse-held`, `raw-finite` or
`sse-finite` before a native Read action. Held responses flush non-silent 24kHz
mono S16LE pieces while keeping the HTTP source open. Finite responses emit four
pieces, and SSE adds an explicit `speech.audio.done`. The state route reports
real flushes, HTTP-context cancellation and successful EOF; it stores no audio.

The reviewed `speech-observer.js` calls the original native source `start` and
`stop`, preserves their arguments, return value and exceptions, and reports
metadata only after native success, with stable node IDs and event sequences. Observation failures cannot change the
native result. No audio is initiated, native API result replaced, storage/auth
changed or UI action simulated by this observer. Tests also require native
screenshots with visible app content. These observations accept scheduling and
running audio contexts, not physical speaker acoustics.

For iOS use `mobile/e2e/ios-speech-stream.swift` through the private wrapper:

```sh
IOS_LAYOUT_SCENARIO=ios-speech-stream.swift \
IOS_LAYOUT_GATEWAY_ORIGIN="$speech_fixture_origin" \
IOS_LAYOUT_PRESERVE_PUBLIC=1 IOS_LAYOUT_PHONE_MODE=account IOS_LAYOUT_QUERY=/ \
IOS_LAYOUT_SPEECH_OBSERVER=1 python3 mobile/scripts/ios-layout-fixture.py
```

Run `BindFlowTests/testNativePCMAndSSEStartBeforeEOFStopTheSourceAndFinishNormally`
in that disposable project. The opt-in observer goes only into the private
Bridge document-start script. Public assets remain byte-for-byte unchanged;
omitting the option injects no observer.

For Android select instrumentation class/method
`com.lubyruffy.zwai.SpeechStreamingTest#pcmAndSSEStartBeforeEOFStopTheSourceAndReturnToIdle`
and pass `fixture_origin` for the owned, reversed loopback listener. The existing
account runner's hardcoded AccountLoginTest sequence does not select this case;
the native runner must explicitly choose it. The test installs the reviewed
observer through the existing bridge evaluation API, but Read, Stop, login and
connection remain native touch/keyboard actions against observed controls.

Both cases exercise held raw PCM and SSE, require successful native start and
sample/context evidence before EOF, prove native Stop disconnects that exact
HTTP source, require every active node to receive a real native stop and disconnect and its context
close promise to resolve with state `closed` (Stop does not require `ended`), then require
successful finite EOF, exactly 24000 scheduled samples, every node ended with
no premature stop, and idle playback. Each round owns its node IDs and settles
before the next mode is selected. Never reuse
an older screenshot, fixture state or native receipt as acceptance of a new
candidate. Preserve original command streams, source/public bytes and signing
identities, app/simulator/emulator identities and final cleanup separately.
