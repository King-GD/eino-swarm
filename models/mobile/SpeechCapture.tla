--------------------------- MODULE SpeechCapture ---------------------------
EXTENDS Naturals, Sequences

(* C-048: startSpeechRecording in frontend/src/lib/speech-media.ts.
   RecordBody/RecordTail are MediaRecorder dataavailable events. Snapshot is
   onstop; Convert settles recordingWAV's asynchronous decode/encode promise.
   AutoMP4Unchanged restores the observed iPhone ASR failure. The other switches
   remove the final-chunk, converted-byte-limit and cancellation guards.
   Symbolic payload positions represent the complete decoded capture, not the
   codec's implementation or acoustic recognition accuracy. The real AAC/WAV
   regression binds that abstraction to decoded duration and both tone sections.
   Fair progress assumes recorder/decoder operations eventually return; a
   decoder may fail, and an explicit cancellation may win at any earlier step. *)

CONSTANTS
    AutoMP4Unchanged,
    SnapshotBeforeTail,
    SkipConvertedLimit,
    IgnoreCancelledConversion

VARIABLES
    phase, container, setting, byteLimit, captured, tailSeen,
    payload, outputFormat, outputBytes, aborted, cancelledDuringConversion

vars == <<phase, container, setting, byteLimit, captured, tailSeen,
          payload, outputFormat, outputBytes, aborted, cancelledDuringConversion>>

Terminal == {"ready", "rejected", "cancelled"}
WAVBytes == 8
CompleteCapture == <<1, 2>>

Init ==
    /\ phase = "recording"
    /\ container \in {"mp4", "webm"}
    /\ setting \in {"auto", "wav"}
    /\ byteLimit \in {4, WAVBytes}
    /\ captured = <<>>
    /\ tailSeen = FALSE
    /\ payload = <<>>
    /\ outputFormat = "none"
    /\ outputBytes = 0
    /\ aborted = FALSE
    /\ cancelledDuringConversion = FALSE

RecordBody ==
    /\ phase = "recording" /\ captured = <<>>
    /\ captured' = <<1>>
    /\ UNCHANGED <<phase, container, setting, byteLimit, tailSeen, payload,
                    outputFormat, outputBytes, aborted, cancelledDuringConversion>>

Stop ==
    /\ phase = "recording" /\ captured = <<1>>
    /\ phase' = "stopping"
    /\ UNCHANGED <<container, setting, byteLimit, captured, tailSeen, payload,
                    outputFormat, outputBytes, aborted, cancelledDuringConversion>>

RecordTail ==
    /\ phase = "stopping" /\ ~tailSeen
    /\ captured' = Append(captured, 2)
    /\ tailSeen' = TRUE
    /\ UNCHANGED <<phase, container, setting, byteLimit, payload, outputFormat,
                    outputBytes, aborted, cancelledDuringConversion>>

Snapshot ==
    /\ phase = "stopping" /\ (tailSeen \/ SnapshotBeforeTail)
    /\ phase' = "finalizing"
    /\ payload' = captured
    /\ UNCHANGED <<container, setting, byteLimit, captured, tailSeen,
                    outputFormat, outputBytes, aborted, cancelledDuringConversion>>

NeedsWAV == setting = "wav" \/ (container = "mp4" /\ ~AutoMP4Unchanged)

Finalize ==
    /\ phase = "finalizing"
    /\ IF NeedsWAV
       THEN /\ phase' = "converting"
            /\ UNCHANGED <<outputFormat, outputBytes>>
       ELSE /\ phase' = "ready"
            /\ outputFormat' = container
            /\ outputBytes' = Len(payload)
    /\ UNCHANGED <<container, setting, byteLimit, captured, tailSeen, payload,
                    aborted, cancelledDuringConversion>>

Convert ==
    /\ phase = "converting"
    /\ \E decoded \in BOOLEAN :
        /\ phase' = IF aborted /\ ~IgnoreCancelledConversion THEN "cancelled"
                    ELSE IF ~decoded \/ (WAVBytes > byteLimit /\ ~SkipConvertedLimit)
                         THEN "rejected" ELSE "ready"
        /\ outputFormat' = "wav"
        /\ outputBytes' = WAVBytes
    /\ UNCHANGED <<container, setting, byteLimit, captured, tailSeen, payload,
                    aborted, cancelledDuringConversion>>

Cancel ==
    /\ phase \notin Terminal /\ ~aborted
    /\ aborted' = TRUE
    /\ cancelledDuringConversion' = (phase = "converting")
    (* Conversion can return after cancellation; its callback must refuse it. *)
    /\ phase' = IF phase = "converting" THEN phase ELSE "cancelled"
    /\ UNCHANGED <<container, setting, byteLimit, captured, tailSeen, payload,
                    outputFormat, outputBytes>>

Progress == RecordBody \/ Stop \/ RecordTail \/ Snapshot \/ Finalize \/ Convert
Next == Progress \/ Cancel
Spec == Init /\ [][Next]_vars /\ WF_vars(Progress)

TypeOK ==
    /\ phase \in {"recording", "stopping", "finalizing", "converting"} \cup Terminal
    /\ container \in {"mp4", "webm"} /\ setting \in {"auto", "wav"}
    /\ byteLimit \in {4, WAVBytes}
    /\ captured \in {<<>>, <<1>>, CompleteCapture}
    /\ payload \in {<<>>, <<1>>, CompleteCapture}
    /\ outputFormat \in {"none", "mp4", "webm", "wav"}
    /\ outputBytes \in 0..WAVBytes
    /\ tailSeen \in BOOLEAN /\ aborted \in BOOLEAN
    /\ cancelledDuringConversion \in BOOLEAN

ASRCompatible == phase = "ready" => outputFormat \in {"wav", "webm"}
FinalChunkRetained == phase \in {"converting", "ready"} => payload = CompleteCapture
ConvertedBytesBounded == phase = "ready" => outputBytes <= byteLimit
CancelledNeverPublished == aborted => phase /= "ready"
CaptureEventuallySettles == <>(phase \in Terminal)

NeverWAVReady == ~(phase = "ready" /\ container = "mp4" /\ setting = "auto")
NeverNativeReady == ~(phase = "ready" /\ outputFormat = "webm")
NeverConvertedLimitRefusal == ~(phase = "rejected" /\ outputBytes > byteLimit)
NeverCancelledConversion == ~(phase = "cancelled" /\ cancelledDuringConversion)
NeverDecodeFailure == ~(phase = "rejected" /\ byteLimit = WAVBytes)

=============================================================================
