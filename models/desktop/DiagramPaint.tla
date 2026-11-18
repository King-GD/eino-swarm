----------------------------- MODULE DiagramPaint -----------------------------
EXTENDS Naturals

(* One Mermaid SVG is admitted as an image or kept as source. The page paints
   an admitted image on the WebContent main thread, so a mounted diagram has
   to be inside every paint axis. This is mermaidSVGDataURL in
   frontend/src/lib/markdown-mermaid.ts:

   width and height are the viewBox edges;
   width * height is the pixel area;
   elements, text and bytes are the same counters.

   The TypeScript caps are the product limits. MaxEdge, MaxPixels and Bound
   only keep this state space finite, and the ASSUME makes the two geometry
   overflows distinguishable. IgnoreEdge, IgnorePixels, IgnoreElements,
   IgnoreText and IgnoreBytes each drop one check. *)

CONSTANTS
    IgnoreEdge,
    IgnorePixels,
    IgnoreElements,
    IgnoreText,
    IgnoreBytes,
    MaxEdge,
    MaxPixels,
    MaxElements,
    MaxText,
    MaxBytes,
    Bound

ASSUME
    /\ Bound > MaxEdge
    /\ MaxEdge <= MaxPixels
    /\ MaxEdge * 2 > MaxPixels
    /\ Bound > MaxElements
    /\ Bound > MaxText
    /\ Bound > MaxBytes

VARIABLES
    width, height, elements, text, bytes, decision

vars == <<width, height, elements, text, bytes, decision>>

Decisions == {"none", "mount", "source"}

PaintFits(w, h, e, t, b) ==
    /\ IgnoreEdge \/ (w <= MaxEdge /\ h <= MaxEdge)
    /\ IgnorePixels \/ (w * h <= MaxPixels)
    /\ IgnoreElements \/ (e <= MaxElements)
    /\ IgnoreText \/ (t <= MaxText)
    /\ IgnoreBytes \/ (b <= MaxBytes)

Init ==
    /\ width = 0 /\ height = 0 /\ elements = 0 /\ text = 0 /\ bytes = 0
    /\ decision = "none"

Admit ==
    /\ width' \in 0..Bound
    /\ height' \in 0..Bound
    /\ elements' \in 0..Bound
    /\ text' \in 0..Bound
    /\ bytes' \in 0..Bound
    /\ decision' = IF PaintFits(width', height', elements', text', bytes')
                   THEN "mount"
                   ELSE "source"

Next == Admit \/ UNCHANGED vars

Spec == Init /\ [][Next]_vars

TypeOK ==
    /\ width \in 0..Bound /\ height \in 0..Bound
    /\ elements \in 0..Bound /\ text \in 0..Bound /\ bytes \in 0..Bound
    /\ decision \in Decisions

MountedEdge == decision = "mount" => width <= MaxEdge /\ height <= MaxEdge
MountedPixels == decision = "mount" => width * height <= MaxPixels
MountedElements == decision = "mount" => elements <= MaxElements
MountedText == decision = "mount" => text <= MaxText
MountedBytes == decision = "mount" => bytes <= MaxBytes

NeverMounted == decision /= "mount"
NeverSource == decision /= "source"

=============================================================================
