package desktop

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"

	"github.com/wailsapp/wails/v3/pkg/application"
)

const recoveryMessageLimit = 2048
const recoverySnapshotSide = 64
const recoveryProbeGridSide = 8
const recoveryProbeCellSide = 2

// recoveryPresentationBlank is the native snapshot status for a flat window.
// The same number is what presentedFrameStatus returns to ScreenCaptureKit.
const recoveryPresentationBlank = 6

// errPresentedFrameBlank is the on-screen frame, not a failed capture.
// A missing, hidden, or refused sample must not use this error.
var errPresentedFrameBlank = errors.New("presented frame is blank")

const (
	presentationGridX      = 8
	presentationGridY      = 6
	presentationContrast   = 48
	presentationMinOpaque  = 64
	presentationCellOpaque = 4
)

func validRecoveryMessage(expected, session string, generation int, challenge, message string, origin *application.OriginInfo) bool {
	if origin == nil || !origin.IsMainFrame || !sameDirectoryOrigin(expected, origin.Origin) ||
		(origin.TopOrigin != "" && !sameDirectoryOrigin(expected, origin.TopOrigin)) || len(message) > recoveryMessageLimit {
		return false
	}
	var receipt struct {
		Type       string `json:"type"`
		Session    string `json:"session"`
		Generation int    `json:"generation"`
		Challenge  string `json:"challenge"`
	}
	return json.Unmarshal([]byte(message), &receipt) == nil && receipt.Type == "zwai:recovery" &&
		receipt.Session == session && receipt.Generation == generation && receipt.Challenge == challenge && challenge != ""
}

func snapshotMatchesProbe(pixels []byte, first, second color.NRGBA, challenge string) bool {
	if len(pixels) != recoverySnapshotSide*recoverySnapshotSide*4 {
		return false
	}
	nonce, err := hex.DecodeString(challenge)
	if err != nil || len(nonce) != 8 {
		return false
	}
	if absChannel(first.R, second.R) <= 24 && absChannel(first.G, second.G) <= 24 && absChannel(first.B, second.B) <= 24 {
		return false
	}
	const markerSide = recoveryProbeGridSide * recoveryProbeCellSide
	for y := 0; y <= recoverySnapshotSide-markerSide; y++ {
		for x := 0; x <= recoverySnapshotSide-markerSide; x++ {
			if matchesProbePattern(pixels, nonce, first, second, x, y, false) || matchesProbePattern(pixels, nonce, first, second, x, y, true) {
				return true
			}
		}
	}
	return false
}

func matchesProbePattern(pixels, nonce []byte, first, second color.NRGBA, left, top int, flipped bool) bool {
	for bit := 0; bit < 64; bit++ {
		row := bit / recoveryProbeGridSide
		if flipped {
			row = recoveryProbeGridSide - 1 - row
		}
		ink := first
		if nonce[bit/8]&(1<<uint(7-bit%8)) != 0 {
			ink = second
		}
		matches := 0
		for y := 0; y < recoveryProbeCellSide; y++ {
			for x := 0; x < recoveryProbeCellSide; x++ {
				i := ((top+row*recoveryProbeCellSide+y)*recoverySnapshotSide + left + (bit%recoveryProbeGridSide)*recoveryProbeCellSide + x) * 4
				if pixels[i+3] >= 240 && absChannel(pixels[i], ink.R) <= 12 && absChannel(pixels[i+1], ink.G) <= 12 && absChannel(pixels[i+2], ink.B) <= 12 {
					matches++
				}
			}
		}
		if matches < 3 {
			return false
		}
	}
	return true
}

func absChannel(a, b byte) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

func presentationCellBounds(width, height, cx, cy int) (x0, y0, x1, y1 int) {
	x0 = cx * width / presentationGridX
	x1 = (cx + 1) * width / presentationGridX
	y0 = cy * height / presentationGridY
	y1 = (cy + 1) * height / presentationGridY
	return
}

func presentationLuma(red, green, blue byte) int {
	return (54*int(red) + 183*int(green) + 19*int(blue)) >> 8
}

func histogramMedian(hist *[256]int, count int) int {
	target := count / 2
	seen := 0
	median := 0
	for luma, n := range hist {
		seen += n
		if seen > target {
			median = luma
			break
		}
	}
	return median
}

func histogramOutliers(hist *[256]int, median int) int {
	outliers := 0
	for luma, n := range hist {
		delta := luma - median
		if delta < 0 {
			delta = -delta
		}
		if delta >= presentationContrast {
			outliers += n
		}
	}
	return outliers
}

// presentedFrameBlank reports a window whose body is one flat colour.
// The top band is the probe cover and window controls, so it cannot prove
// that the rest of the window has anything to read. A cell counts when its
// own pixels contain real contrast, or when its colour differs from another
// body cell. Faint rules and a few specks stay under that floor. Too little
// opaque sample is unknown, not a blank frame.
func presentedFrameBlank(pixels []byte, width, height int) bool {
	if width < presentationGridX*4 || height < presentationGridY*4 || len(pixels) != width*height*4 {
		return false
	}
	hist := make([][256]int, presentationGridY*presentationGridX)
	counts := make([]int, presentationGridY*presentationGridX)
	opaque := 0
	for y := 0; y < height; y++ {
		cy := y * presentationGridY / height
		if cy == 0 {
			continue
		}
		for x := 0; x < width; x++ {
			i := (y*width + x) * 4
			if pixels[i+3] < 240 {
				continue
			}
			cx := x * presentationGridX / width
			slot := cy*presentationGridX + cx
			hist[slot][presentationLuma(pixels[i], pixels[i+1], pixels[i+2])]++
			counts[slot]++
			opaque++
		}
	}
	if opaque < presentationMinOpaque {
		return false
	}
	minMedian, maxMedian, usable := 255, 0, 0
	for cy := 1; cy < presentationGridY; cy++ {
		for cx := 0; cx < presentationGridX; cx++ {
			slot := cy*presentationGridX + cx
			if counts[slot] < presentationCellOpaque {
				continue
			}
			median := histogramMedian(&hist[slot], counts[slot])
			if histogramOutliers(&hist[slot], median) >= presentationCellOpaque {
				return false
			}
			if usable == 0 || median < minMedian {
				minMedian = median
			}
			if usable == 0 || median > maxMedian {
				maxMedian = median
			}
			usable++
		}
	}
	return usable > 0 && maxMedian-minMedian < presentationContrast
}

func presentedSampleStatus(pixels []byte, width, height int) int {
	if width < 1 || height < 1 || width > 4096 || height > 4096 {
		return 0
	}
	need := int64(width) * int64(height) * 4
	if need != int64(len(pixels)) {
		return 0
	}
	if presentedFrameBlank(pixels, width, height) {
		return recoveryPresentationBlank
	}
	return 0
}

func recoveryProbeScript(expected, session string, generation int, challenge string, first, second color.NRGBA, document int) string {
	// A native sibling covers this corner on screen without entering the
	// WKWebView snapshot. The marker itself remains opaque and never handles input.
	// Colour tolerance cannot distinguish nearby palettes: every ordered nonce bit
	// must paint, so an old snapshot cannot acknowledge a new paint challenge.
	payload, _ := json.Marshal(map[string]any{"type": "zwai:recovery", "session": session, "generation": generation, "challenge": challenge, "document": document})
	origin, _ := json.Marshal(expected)
	return fmt.Sprintf(`(()=>{if(location.origin!==%s)return;const p=%s;const owner=window.__zwaiRecoveryDocument;if(owner&&owner.session===p.session&&owner.generation===p.generation&&owner.document!==p.document)return;window.__zwaiRecoveryDocument={session:p.session,generation:p.generation,document:p.document};if(!document.getElementById("root")?.firstElementChild)return;
const send=window._wails?.invoke?.bind(window._wails)||window.webkit?.messageHandlers?.external?.postMessage?.bind(window.webkit.messageHandlers.external)||window.chrome?.webview?.postMessage?.bind(window.chrome.webview);if(!send)return;
const previous=window.__zwaiRecoveryLocationBridge;if(!previous||previous.session!==p.session||previous.generation!==p.generation||previous.document!==p.document){if(previous)window.removeEventListener("zwai:recovery-location",previous.publish);const publish=()=>{const a=window.__zwaiRecoveryAttribution||{};send(JSON.stringify({type:"zwai:recovery-location",session:p.session,generation:p.generation,document:p.document,thread_id:typeof a.thread_id==="string"?a.thread_id:"",turn_id:typeof a.turn_id==="string"?a.turn_id:"",project_id:typeof a.project_id==="string"?a.project_id:"",draft_key:typeof a.draft_key==="string"?a.draft_key:""}));};window.__zwaiRecoveryLocationBridge={session:p.session,generation:p.generation,document:p.document,publish};window.addEventListener("zwai:recovery-location",publish);publish();}
if(!window.__zwaiNativeRuntimeReady){send("wails:runtime:ready");window.__zwaiNativeRuntimeReady=true;}let e=document.getElementById("zwai-native-render-probe");if(!e){e=document.createElement("div");e.id="zwai-native-render-probe";e.setAttribute("aria-hidden","true");document.documentElement.appendChild(e);}
e.style.cssText="position:fixed;left:0;top:0;width:16px;height:16px;pointer-events:none;z-index:2147483647;contain:strict;display:grid;grid-template-columns:repeat(8,2px);grid-template-rows:repeat(8,2px)";
const colours=["rgb(%d,%d,%d)","rgb(%d,%d,%d)"],bytes=p.challenge.match(/../g).map(v=>parseInt(v,16));if(e.children.length!==64)e.replaceChildren();for(let i=0;i<64;i++){let c=e.children[i];if(!c){c=document.createElement("i");e.appendChild(c);}c.style.cssText="display:block;width:2px;height:2px;background:"+colours[(bytes[i>>3]>>(7-(i&7)))&1];}
requestAnimationFrame(()=>requestAnimationFrame(()=>{const a=window.__zwaiRecoveryAttribution||{};send(JSON.stringify({...p,thread_id:typeof a.thread_id==="string"?a.thread_id:"",turn_id:typeof a.turn_id==="string"?a.turn_id:"",project_id:typeof a.project_id==="string"?a.project_id:"",draft_key:typeof a.draft_key==="string"?a.draft_key:""}));}));})();`, origin, string(payload), first.R, first.G, first.B, second.R, second.G, second.B)
}
