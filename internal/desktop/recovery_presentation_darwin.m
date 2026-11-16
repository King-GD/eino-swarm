#import <Cocoa/Cocoa.h>
#import <objc/message.h>
#include <dlfcn.h>
#include <os/lock.h>
#include <stdlib.h>

extern void desktopSnapshotResult(uint64_t request, int status, unsigned char *pixels, int length);
extern int presentedFrameStatus(unsigned char *pixels, int length, int width, int height);
extern void (*zwaiAfterProbeSnapshot)(void *window, uint64_t request, unsigned char *pixels, int length);

// Nearest samples of our own window. Averaging would erase text and make a
// healthy window look flat. The Go decision ignores the top band.
enum { zwaiPresentedSampleWidth = 480, zwaiPresentedSampleHeight = 270 };

typedef struct {
    os_unfair_lock lock;
    int done;
    int releases;
    uint64_t request;
    unsigned char *probe;
    int probeLength;
} zwaiPresentDelivery;

typedef void (^zwaiShareableHandler)(id content, NSError *error);
typedef void (^zwaiImageHandler)(CGImageRef image, NSError *error);

// Two callers always run: the capture completion and the timeout. The first
// publishes one result. A missing or slow capture publishes the WebKit snapshot.
static void zwaiFinishPresented(zwaiPresentDelivery *delivery, int status) {
    int publish = 0;
    int chosen = 0;
    uint64_t request = 0;
    unsigned char *probe = NULL;
    int length = 0;
    os_unfair_lock_lock(&delivery->lock);
    if (!delivery->done) {
        delivery->done = 1;
        publish = 1;
        chosen = status;
        request = delivery->request;
        probe = delivery->probe;
        length = delivery->probeLength;
        delivery->probe = NULL;
    }
    delivery->releases++;
    int freeDelivery = delivery->releases == 2;
    os_unfair_lock_unlock(&delivery->lock);
    if (publish) {
        if (chosen != 0) desktopSnapshotResult(request, chosen, NULL, 0);
        else desktopSnapshotResult(request, 0, probe, length);
        free(probe);
    }
    if (freeDelivery) free(delivery);
}

static int zwaiCopyPresentedSamples(CGImageRef image, unsigned char **out, int *width, int *height) {
    if (image == NULL || CGImageGetWidth(image) == 0 || CGImageGetHeight(image) == 0) return 0;
    size_t bytes = (size_t)zwaiPresentedSampleWidth * (size_t)zwaiPresentedSampleHeight * 4;
    unsigned char *grid = calloc(1, bytes);
    if (grid == NULL) return 0;
    CGColorSpaceRef space = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
    CGContextRef context = CGBitmapContextCreate(grid, zwaiPresentedSampleWidth, zwaiPresentedSampleHeight,
        8, zwaiPresentedSampleWidth * 4, space, kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
    CGColorSpaceRelease(space);
    if (context == NULL) {
        free(grid);
        return 0;
    }
    CGContextSetInterpolationQuality(context, kCGInterpolationNone);
    CGContextSetShouldAntialias(context, false);
    CGContextDrawImage(context, CGRectMake(0, 0, zwaiPresentedSampleWidth, zwaiPresentedSampleHeight), image);
    CGContextRelease(context);
    *out = grid;
    *width = zwaiPresentedSampleWidth;
    *height = zwaiPresentedSampleHeight;
    return 1;
}

static void zwaiDeliverWebKitProbe(uint64_t request, unsigned char *probe, int length) {
    desktopSnapshotResult(request, 0, probe, length);
    free(probe);
}

// ScreenCaptureKit is loaded on demand. cgo cannot weak-link it, and a
// strong link would stop older macOS from launching. The classes are not
// visible until the framework is loaded. A missing framework, class, or
// method keeps the WebKit result.
static BOOL zwaiLoadScreenCapture(void) {
    static dispatch_once_t once;
    static void *handle;
    dispatch_once(&once, ^{
        handle = dlopen("/System/Library/Frameworks/ScreenCaptureKit.framework/ScreenCaptureKit", RTLD_LAZY);
    });
    return handle != NULL;
}

static BOOL zwaiCanCapturePresentedFrame(void) {
    if (!zwaiLoadScreenCapture()) return NO;
    Class shareable = NSClassFromString(@"SCShareableContent");
    Class manager = NSClassFromString(@"SCScreenshotManager");
    Class filter = NSClassFromString(@"SCContentFilter");
    Class config = NSClassFromString(@"SCStreamConfiguration");
    return shareable != nil && manager != nil && filter != nil && config != nil &&
        [shareable respondsToSelector:@selector(getCurrentProcessShareableContentWithCompletionHandler:)] &&
        [manager respondsToSelector:@selector(captureImageWithFilter:configuration:completionHandler:)] &&
        [filter instancesRespondToSelector:@selector(initWithDesktopIndependentWindow:)];
}

int zwaiPresentedCaptureAvailable(void) {
    return zwaiCanCapturePresentedFrame() ? 1 : 0;
}

static void zwaiReadPresentedWindow(NSWindow *window, CGWindowID *windowID, BOOL *visible) {
    *windowID = 0;
    *visible = NO;
    if (window == nil) return;
    void (^read)(void) = ^{
        if ((window.occlusionState & NSWindowOcclusionStateVisible) != 0) *visible = YES;
        if (window.windowNumber > 0) *windowID = (CGWindowID)window.windowNumber;
    };
    if ([NSThread isMainThread]) read();
    else dispatch_sync(dispatch_get_main_queue(), read);
}

static void zwaiSetBool(id object, SEL setter, BOOL value) {
    if (object != nil && [object respondsToSelector:setter]) {
        ((void (*)(id, SEL, BOOL))objc_msgSend)(object, setter, value);
    }
}

static void zwaiCaptureOwnWindow(CGWindowID windowID, zwaiPresentDelivery *delivery) {
    Class shareable = NSClassFromString(@"SCShareableContent");
    ((void (*)(id, SEL, zwaiShareableHandler))objc_msgSend)(
        shareable, @selector(getCurrentProcessShareableContentWithCompletionHandler:),
        ^(id content, NSError *error) {
            if (error != nil || content == nil || ![content respondsToSelector:@selector(windows)]) {
                zwaiFinishPresented(delivery, 0);
                return;
            }
            id windows = ((id (*)(id, SEL))objc_msgSend)(content, @selector(windows));
            if (![windows isKindOfClass:[NSArray class]]) {
                zwaiFinishPresented(delivery, 0);
                return;
            }
            id match = nil;
            for (id candidate in (NSArray *)windows) {
                if (![candidate respondsToSelector:@selector(windowID)]) continue;
                CGWindowID candidateID = ((CGWindowID (*)(id, SEL))objc_msgSend)(candidate, @selector(windowID));
                if (candidateID == windowID) {
                    match = candidate;
                    break;
                }
            }
            Class filterClass = NSClassFromString(@"SCContentFilter");
            Class configClass = NSClassFromString(@"SCStreamConfiguration");
            Class manager = NSClassFromString(@"SCScreenshotManager");
            if (match == nil || filterClass == nil || configClass == nil || manager == nil) {
                zwaiFinishPresented(delivery, 0);
                return;
            }
            id filter = ((id (*)(id, SEL, id))objc_msgSend)([filterClass alloc], @selector(initWithDesktopIndependentWindow:), match);
            id config = [[configClass alloc] init];
            zwaiSetBool(config, @selector(setShowsCursor:), NO);
            zwaiSetBool(config, @selector(setIgnoreShadowsSingleWindow:), YES);
            if (filter == nil || config == nil) {
                [filter release];
                [config release];
                zwaiFinishPresented(delivery, 0);
                return;
            }
            ((void (*)(id, SEL, id, id, zwaiImageHandler))objc_msgSend)(
                manager, @selector(captureImageWithFilter:configuration:completionHandler:), filter, config,
                ^(CGImageRef image, NSError *captureError) {
                    int status = 0;
                    unsigned char *samples = NULL;
                    int width = 0;
                    int height = 0;
                    if (captureError == nil && image != NULL && zwaiCopyPresentedSamples(image, &samples, &width, &height)) {
                        status = presentedFrameStatus(samples, width * height * 4, width, height);
                        free(samples);
                    }
                    zwaiFinishPresented(delivery, status);
                });
            [filter release];
            [config release];
        });
}

static void zwaiPresentedFrameAfterProbe(void *native, uint64_t request, unsigned char *probe, int length) {
    if (probe == NULL || length != 64 * 64 * 4) {
        free(probe);
        desktopSnapshotResult(request, 0, NULL, 0);
        return;
    }
    CGWindowID windowID = 0;
    BOOL visible = NO;
    zwaiReadPresentedWindow((NSWindow *)native, &windowID, &visible);
    if (!visible || windowID == 0 || !zwaiCanCapturePresentedFrame()) {
        zwaiDeliverWebKitProbe(request, probe, length);
        return;
    }
    zwaiPresentDelivery *delivery = calloc(1, sizeof(*delivery));
    if (delivery == NULL) {
        zwaiDeliverWebKitProbe(request, probe, length);
        return;
    }
    delivery->request = request;
    delivery->probe = probe;
    delivery->probeLength = length;
    // Shorter than the probe interval, so a stuck capture cannot spend the
    // health deadline. The late result is ignored.
    dispatch_after(dispatch_time(DISPATCH_TIME_NOW, (int64_t)2 * NSEC_PER_SEC),
        dispatch_get_global_queue(QOS_CLASS_UTILITY, 0), ^{
            zwaiFinishPresented(delivery, 0);
        });
    zwaiCaptureOwnWindow(windowID, delivery);
}

__attribute__((constructor)) static void zwaiInstallPresentedFrameCheck(void) {
    zwaiAfterProbeSnapshot = zwaiPresentedFrameAfterProbe;
}
