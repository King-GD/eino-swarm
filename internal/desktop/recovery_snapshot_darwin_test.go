//go:build darwin

package desktop

import (
	"context"
	"errors"
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestNativeSnapshotCancellationDropsLateCallbacksWithoutRetainingRequests(t *testing.T) {
	previous := requestRecoverySnapshot
	defer func() { requestRecoverySnapshot = previous }()
	for _, status := range []int{0, 1, 2} {
		requestRecoverySnapshot = func(_ context.Context, _ application.Window, id uint64) {
			pixels := make([]byte, recoverySnapshotSide*recoverySnapshotSide*4)
			if status == 2 {
				pixels = nil
			}
			deliverRecoverySnapshot(id, status, pixels)
		}
		pixels, err := captureRecoveryPixels(context.Background(), nil)
		if status == 0 {
			if err != nil || len(pixels) != recoverySnapshotSide*recoverySnapshotSide*4 {
				t.Fatalf("valid snapshot missing: %d %v", len(pixels), err)
			}
		} else if err == nil {
			t.Fatal("native snapshot failure was accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := captureRecoveryPixels(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	deliverRecoverySnapshot(snapshotSequence.Load(), 0, make([]byte, recoverySnapshotSide*recoverySnapshotSide*4))
	count := 0
	snapshotRequests.Range(func(_, _ any) bool { count++; return true })
	if count != 0 {
		t.Fatalf("timed out snapshots retained %d requests", count)
	}
	reply := make(chan snapshotReply, 1)
	snapshotRequests.Store(uint64(999999), reply)
	defer snapshotRequests.Delete(uint64(999999))
	deliverRecoverySnapshot(999999, 0, nil)
	deliverRecoverySnapshot(999999, 0, nil)
	if result := <-reply; result.err == nil {
		t.Fatal("invalid empty pixels became healthy")
	}
}

// The framework is loaded by name at runtime. A missed lookup would leave a
// flat window marked healthy, and the compiler cannot see that.
func TestCurrentProcessWindowCaptureIsAvailable(t *testing.T) {
	if !presentedCaptureAvailable() {
		t.Fatal("current-process window capture is not available")
	}
}

func TestPresentedFrameExportMatchesTheSampleDecision(t *testing.T) {
	const width, height = 80, 60
	flat := solidFrame(width, height, 249, 250, 251, 255)
	if presentedFrameStatusFromSample(nil, width, height) != 0 || presentedFrameStatusFromSample(flat, width, height) != recoveryPresentationBlank {
		t.Fatal("export did not preserve the blank decision")
	}
	content := append([]byte(nil), flat...)
	x0, y0, x1, y1 := presentationCellBounds(width, height, 3, 2)
	paintRect(content, width, x0, y0, x1, y1, 32, 36, 48, 255)
	if presentedFrameStatusFromSample(content, width, height) != 0 {
		t.Fatal("export reported readable content as blank")
	}
}

func TestBlankPresentationIsNotAGenericSnapshotFailure(t *testing.T) {
	previous := requestRecoverySnapshot
	defer func() { requestRecoverySnapshot = previous }()
	requestRecoverySnapshot = func(_ context.Context, _ application.Window, id uint64) {
		deliverRecoverySnapshot(id, recoveryPresentationBlank, nil)
	}
	_, err := captureRecoveryPixels(context.Background(), nil)
	if !errors.Is(err, errPresentedFrameBlank) {
		t.Fatalf("blank frame became %v", err)
	}
}
