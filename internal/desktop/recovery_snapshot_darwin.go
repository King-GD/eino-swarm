//go:build darwin

package desktop

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa -framework WebKit
#include "recovery_snapshot_darwin.h"
#include <stdlib.h>
int zwaiPresentedCaptureAvailable(void);
*/
import "C"

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/application"
)

type snapshotReply struct {
	pixels []byte
	err    error
}

var snapshotRequests sync.Map
var snapshotSequence atomic.Uint64

var requestRecoverySnapshot = func(ctx context.Context, window application.Window, id uint64) {
	application.InvokeAsync(func() {
		if ctx.Err() == nil {
			C.zwaiRecoverySnapshot(window.NativeWindow(), C.uint64_t(id))
		}
	})
}

func rendererRecoveryAvailable() bool { return true }

func updateRecoveryCover(window application.Window, colour [3]byte) {
	application.InvokeAsync(func() {
		C.zwaiRecoveryCover(window.NativeWindow(), C.uchar(colour[0]), C.uchar(colour[1]), C.uchar(colour[2]))
	})
}

func executeRecoveryProbe(window application.Window, script string) {
	application.InvokeAsync(func() {
		text := C.CString(script)
		defer C.free(unsafe.Pointer(text))
		cover := C.CString(recoveryCoverScript)
		defer C.free(unsafe.Pointer(cover))
		C.zwaiRecoveryEvaluate(window.NativeWindow(), text, cover)
	})
}

func captureRecoveryPixels(ctx context.Context, window application.Window) ([]byte, error) {
	id := snapshotSequence.Add(1)
	reply := make(chan snapshotReply, 1)
	snapshotRequests.Store(id, reply)
	defer snapshotRequests.Delete(id)
	if ctx.Err() == nil {
		requestRecoverySnapshot(ctx, window, id)
	}
	select {
	case result := <-reply:
		return result.pixels, result.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

//export desktopSnapshotResult
func desktopSnapshotResult(request C.uint64_t, status C.int, pixels *C.uchar, length C.int) {
	var copied []byte
	if status == 0 && length == recoverySnapshotSide*recoverySnapshotSide*4 && pixels != nil {
		copied = C.GoBytes(unsafe.Pointer(pixels), length)
	}
	deliverRecoverySnapshot(uint64(request), int(status), copied)
}

//export presentedFrameStatus
func presentedFrameStatus(pixels *C.uchar, length C.int, width C.int, height C.int) C.int {
	if pixels == nil || length < 1 {
		return 0
	}
	view := unsafe.Slice((*byte)(unsafe.Pointer(pixels)), int(length))
	return C.int(presentedSampleStatus(view, int(width), int(height)))
}

func presentedCaptureAvailable() bool {
	return C.zwaiPresentedCaptureAvailable() != 0
}

func presentedFrameStatusFromSample(pixels []byte, width, height int) int {
	if len(pixels) == 0 {
		return int(presentedFrameStatus(nil, 0, C.int(width), C.int(height)))
	}
	buf := C.CBytes(pixels)
	defer C.free(buf)
	return int(presentedFrameStatus((*C.uchar)(buf), C.int(len(pixels)), C.int(width), C.int(height)))
}

func deliverRecoverySnapshot(request uint64, status int, pixels []byte) {
	value, ok := snapshotRequests.Load(request)
	if !ok {
		return
	} // A timed-out snapshot can complete after replacement began.
	result := snapshotReply{}
	switch {
	case status == recoveryPresentationBlank:
		result.err = errPresentedFrameBlank
	case status != 0:
		result.err = fmt.Errorf("native renderer snapshot failed (code %d)", status)
	case len(pixels) != recoverySnapshotSide*recoverySnapshotSide*4:
		result.err = fmt.Errorf("native renderer snapshot has invalid pixels")
	default:
		result.pixels = pixels
	}
	select {
	case value.(chan snapshotReply) <- result:
	default:
	}
}
