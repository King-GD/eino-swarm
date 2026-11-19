"""SDK seams of the complete AX helper main; never opens or inspects a host app.

Root schedules this module under host validation admission. Set the explicit
source override only for the retained, compilable pre-fix RED comparison.
"""
import json
import os
from pathlib import Path
import platform
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent
SOURCE = Path(os.environ.get("ZWAI_AX_MAIN_TEST_SOURCE", str(ROOT / "native_macos_ax.m"))).resolve(strict=True)

PREFIX = r"""
#import <AppKit/AppKit.h>
#import <ApplicationServices/ApplicationServices.h>
#import <CoreGraphics/CoreGraphics.h>
#include <sys/sysctl.h>
#include <unistd.h>
static NSString *testMode, *testAction;
static double testClock=10;
static int kernelReads, trustedReads, captureReads, bundleReads;
static int globalCreates, globalSets, globalReleases, appSets, systemAttributeReads;
static int remoteReads, focusCalls, keyPosts, pressCalls, activationCalls, raiseCalls, scrollCalls, siblingRoleReads;
static BOOL timeoutOrderOK=YES;
@interface MainNode:NSObject
@property(nonatomic,strong) NSDictionary *attributes;
@property(nonatomic,strong) NSArray *nodes;
@end
@implementation MainNode
@end
static MainNode *testApp, *testWindow, *testUnknown, *testSystem, *testResult;
static MainNode *makeNode(NSString *role,NSDictionary *extra) {
    MainNode *n=[MainNode new];NSMutableDictionary *attrs=[@{@"AXRole":role} mutableCopy];
    [attrs addEntriesFromDictionary:extra ?: @{}];n.attributes=attrs;n.nodes=@[];return n;
}
static CFTypeID mainType(CFTypeRef value) {
    if ([(__bridge id)value isKindOfClass:MainNode.class]) return AXUIElementGetTypeID();
    return CFGetTypeID(value);
}
static int mainSysctlName(const char *name,void *out,size_t *length,void *newValue,size_t newLength) {
    if (strcmp(name,"kern.hv_vmm_present") || *length!=sizeof(int)) abort();
    *(int *)out=1;return 0;
}
static int mainSysctl(int *mib,u_int count,void *out,size_t *length,void *newValue,size_t newLength) {
    if (count!=3 || mib[1]!=KERN_PROCARGS2 || mib[2]!=41) abort();
    kernelReads++;
    NSMutableData *data=[NSMutableData data];int argc=1;[data appendBytes:&argc length:sizeof(argc)];
    const char *exe="/private/case/Contents/MacOS/zwai";char zero=0;
    [data appendBytes:exe length:strlen(exe)+1];[data appendBytes:&zero length:1];
    [data appendBytes:exe length:strlen(exe)+1];
    const char *marker="ZWAI_NATIVE_CASE_ID=case-main";[data appendBytes:marker length:strlen(marker)+1];
    if (!out) {*length=data.length;return 0;}
    memcpy(out,data.bytes,data.length);*length=data.length;return 0;
}
static BOOL mainTrusted(void) {trustedReads++;return YES;}
static bool mainCapture(void) {captureReads++;return true;}
@interface MainRunning:NSObject
+(instancetype)runningApplicationWithProcessIdentifier:(pid_t)pid;
@property(readonly) NSURL *bundleURL;
@property(readonly) pid_t processIdentifier;
@property(readonly,getter=isActive) BOOL active;
-(BOOL)activateWithOptions:(NSApplicationActivationOptions)options;
@end
@implementation MainRunning
+(instancetype)runningApplicationWithProcessIdentifier:(pid_t)pid {return [self new];}
-(NSURL *)bundleURL {bundleReads++;return [NSURL fileURLWithPath:@"/private/case"];}
-(pid_t)processIdentifier {return 41;}
-(BOOL)isActive {return YES;}
-(BOOL)activateWithOptions:(NSApplicationActivationOptions)options {activationCalls++;return YES;}
@end
@interface MainProcessInfo:NSObject
+(instancetype)processInfo;
-(double)systemUptime;
@end
@implementation MainProcessInfo
+(instancetype)processInfo {return [self new];}
-(double)systemUptime {return testClock;}
@end
@interface MainRunLoop:NSObject
+(instancetype)mainRunLoop;
-(BOOL)runMode:(NSRunLoopMode)mode beforeDate:(NSDate *)date;
@end
@implementation MainRunLoop
+(instancetype)mainRunLoop {return [self new];}
-(BOOL)runMode:(NSRunLoopMode)mode beforeDate:(NSDate *)date {testClock+=.01;return YES;}
@end
static AXUIElementRef mainCreateSystem(void) {
    globalCreates++;if (kernelReads!=2 || !bundleReads || !trustedReads || !captureReads) timeoutOrderOK=NO;
    return (AXUIElementRef)CFBridgingRetain(testSystem);
}
static AXUIElementRef mainCreateApp(pid_t pid) {
    if (pid!=41) abort();return (AXUIElementRef)CFBridgingRetain(testApp);
}
static AXError mainTimeout(AXUIElementRef element,float timeout) {
    if (timeout!=2) timeoutOrderOK=NO;
    if ((__bridge id)element==testSystem) {
        globalSets++;return [testMode isEqual:@"global-failure"] ? kAXErrorCannotComplete : kAXErrorSuccess;
    }
    appSets++;return kAXErrorSuccess;
}
static void mainRelease(CFTypeRef value) {
    if ((__bridge id)value==testSystem) globalReleases++;
    CFRelease(value);
}
static AXError mainCopy(AXUIElementRef element,CFStringRef key,CFTypeRef *out) {
    remoteReads++;
    MainNode *node=(__bridge MainNode *)element;
    if (node==testSystem) {systemAttributeReads++;return kAXErrorCannotComplete;}
    if (globalSets!=1 || globalReleases!=1) timeoutOrderOK=NO;
    if (node==testResult && CFEqual(key,kAXPositionAttribute)) {
        if ([testMode isEqual:@"result-frame-refused"]) return kAXErrorInvalidUIElement;
        if ([testMode hasPrefix:@"result-offscreen"] && !scrollCalls) {
            CGPoint offscreen = CGPointMake(10, 900);
            *out = AXValueCreate(kAXValueCGPointType, &offscreen);
            return kAXErrorSuccess;
        }
    }
    if (node==testResult && scrollCalls && [testMode isEqual:@"result-offscreen-invalidates-result"]
            && CFEqual(key,kAXRoleAttribute)) return kAXErrorInvalidUIElement;
    if (node==testResult && scrollCalls && [testMode isEqual:@"result-offscreen-changes-text"]
            && CFEqual(key,kAXValueAttribute)) {
        *out=CFBridgingRetain(@"Different answer"); return kAXErrorSuccess;
    }
    if (node==testUnknown && CFEqual(key,kAXRoleAttribute)) {
        siblingRoleReads++;
        if ([testMode isEqual:@"result-offscreen-stale-sibling"] ||
            (scrollCalls && [@[@"scroll-invalidates-sibling", @"result-offscreen-invalidates-sibling"] containsObject:testMode]))
            return kAXErrorInvalidUIElement;
    }
    if (node==testUnknown) {
        if ([testMode isEqual:@"role-failure"] && CFEqual(key,kAXRoleAttribute)) return kAXErrorCannotComplete;
        if ([testMode isEqual:@"children-failure"] && CFEqual(key,kAXChildrenAttribute)) return kAXErrorCannotComplete;
        if ([testMode isEqual:@"leaf-no-value"] && CFEqual(key,kAXChildrenAttribute)) return kAXErrorNoValue;
        if ([testMode isEqual:@"leaf-unsupported"] && CFEqual(key,kAXChildrenAttribute)) return kAXErrorAttributeUnsupported;
    }
    id value=nil;
    if (node==testApp && CFEqual(key,kAXWindowsAttribute)) value=@[testWindow];
    else if (CFEqual(key,kAXChildrenAttribute)) value=node.nodes;
    else value=node.attributes[(__bridge NSString *)key];
    if (!value) return kAXErrorNoValue;
    *out=CFBridgingRetain(value);return kAXErrorSuccess;
}
static AXError mainPid(AXUIElementRef element,pid_t *pid) {*pid=41;return kAXErrorSuccess;}
static AXError mainSet(AXUIElementRef element,CFStringRef key,CFTypeRef value) {
    if (CFEqual(key,kAXFocusedAttribute)) focusCalls++;return kAXErrorSuccess;
}
static AXError mainAction(AXUIElementRef element,CFStringRef action) {
    if (CFEqual(action,kAXPressAction)) pressCalls++;
    if (CFEqual(action,CFSTR("AXScrollToVisible"))) scrollCalls++;
    if (CFEqual(action,kAXRaiseAction)) raiseCalls++;
    return kAXErrorSuccess;
}
static CFArrayRef mainWindows(CGWindowListOption options,CGWindowID relative) {
    return (CFArrayRef)CFBridgingRetain(@[@{(id)kCGWindowOwnerPID:@41,(id)kCGWindowLayer:@0,(id)kCGWindowNumber:@17}]);
}
static CGEventSourceRef mainEventSource(CGEventSourceStateID state) {return (CGEventSourceRef)CFBridgingRetain([NSObject new]);}
static CGEventRef mainKeyboard(CGEventSourceRef source,CGKeyCode key,bool down) {return (CGEventRef)CFBridgingRetain([NSObject new]);}
static void mainFlags(CGEventRef event,CGEventFlags flags) {}
static void mainUnicode(CGEventRef event,UniCharCount count,const UniChar *characters) {}
static void mainPost(pid_t pid,CGEventRef event) {if(pid!=41)abort();keyPosts++;}
static int mainSleep(useconds_t delay) {testClock+=delay/1000000.;return 0;}
#define CFGetTypeID mainType
#define CFRelease mainRelease
#define sysctlbyname mainSysctlName
#define sysctl mainSysctl
#define NSRunningApplication MainRunning
#define NSProcessInfo MainProcessInfo
#define NSRunLoop MainRunLoop
#define AXIsProcessTrusted mainTrusted
#define CGPreflightScreenCaptureAccess mainCapture
#define AXUIElementCreateSystemWide mainCreateSystem
#define AXUIElementCreateApplication mainCreateApp
#define AXUIElementSetMessagingTimeout mainTimeout
#define AXUIElementCopyAttributeValue mainCopy
#define AXUIElementGetPid mainPid
#define AXUIElementSetAttributeValue mainSet
#define AXUIElementPerformAction mainAction
#define CGWindowListCopyWindowInfo mainWindows
#define CGEventSourceCreate mainEventSource
#define CGEventCreateKeyboardEvent mainKeyboard
#define CGEventSetFlags mainFlags
#define CGEventKeyboardSetUnicodeString mainUnicode
#define CGEventPostToPid mainPost
#define usleep mainSleep
#define main helper_actual_main
"""

SUFFIX = r"""
#undef main
int main(int argc,const char **argv) {@autoreleasepool {
    if(argc!=3)return 9;testMode=@(argv[1]);testAction=@(argv[2]);
    testApp=makeNode(@"AXApplication",nil);testSystem=makeNode(@"AXSystemWide",nil);
    CGPoint p=CGPointMake(10,20);CGSize z=CGSizeMake(800,600);
    testWindow=makeNode(@"AXWindow",@{@"AXTitle":@"case-main",@"AXPosition":CFBridgingRelease(AXValueCreate(kAXValueCGPointType,&p)),@"AXSize":CFBridgingRelease(AXValueCreate(kAXValueCGSizeType,&z))});
    MainNode *web=makeNode(@"AXWebArea",@{@"AXURL":@"http://127.0.0.1:42/?shell=desktop&pid=41"});
    MainNode *area=makeNode(@"AXTextArea",@{@"AXValue":[testAction isEqual:@"send"] ? @"draft" : @""});
    MainNode *button=makeNode(@"AXButton",@{@"AXTitle":@"Send",@"AXEnabled":@YES});
    testUnknown=makeNode(@"AXGroup",nil);
    // A second composer hidden behind an unreadable child must never disappear.
    if([testMode isEqual:@"children-failure"])testUnknown.nodes=@[makeNode(@"AXTextArea",@{@"AXValue":@"hidden"})];
    testWindow.nodes=@[testUnknown,web,area,button];
    if ([testMode hasPrefix:@"result-offscreen"] || [@[@"scroll-invalidates-sibling", @"result-frame-refused"] containsObject:testMode]) {
        testResult=makeNode(@"AXStaticText",@{@"AXValue":@"Rendered answer",
            @"AXPosition":testWindow.attributes[@"AXPosition"], @"AXSize":testWindow.attributes[@"AXSize"]});
        web.nodes=@[testResult,testUnknown]; testWindow.nodes=@[web,area,button];
    }
    const char *args[]={"helper","41",argv[2],"case-main","/private/case","http://127.0.0.1:42",[testAction isEqual:@"snapshot"] ? (testResult ? "Rendered answer" : "") : "draft"};
    int code=helper_actual_main(7,args);
    NSDictionary *stats=@{@"global_creates":@(globalCreates),@"global_sets":@(globalSets),@"global_releases":@(globalReleases),@"app_sets":@(appSets),@"system_attribute_reads":@(systemAttributeReads),@"timeout_order_ok":@(timeoutOrderOK),@"remote_reads":@(remoteReads),@"focus":@(focusCalls),@"key_posts":@(keyPosts),@"press":@(pressCalls),@"activation":@(activationCalls),@"raise":@(raiseCalls),@"scroll":@(scrollCalls),@"sibling_role_reads":@(siblingRoleReads)};
    NSData *data=[NSJSONSerialization dataWithJSONObject:stats options:0 error:nil];fwrite(data.bytes,1,data.length,stderr);fputc('\n',stderr);
    return code;
}}
"""


@unittest.skipUnless(platform.system() == "Darwin", "actual Objective-C main SDK seam requires macOS")
class AXReadMainTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary = tempfile.TemporaryDirectory(prefix="native-ax-read-actual-main-")
        cls.addClassCleanup(cls.temporary.cleanup)
        directory = Path(cls.temporary.name)
        fixture = directory / "actual-main.m"
        fixture.write_text(PREFIX + '#include ' + json.dumps(str(SOURCE)) + '\n' + SUFFIX)
        cls.binary = directory / "actual-main"
        # Compile errors are setup errors, never a target RED assertion.
        result = subprocess.run(["/usr/bin/xcrun", "clang", "-fobjc-arc", "-mmacosx-version-min=13.0",
                                 "-framework", "AppKit", "-framework", "ApplicationServices",
                                 "-framework", "CoreGraphics", str(fixture), "-o", str(cls.binary)],
                                capture_output=True, text=True, timeout=30)
        if result.returncode:
            raise RuntimeError("actual-main fixture compile failed:\n" + result.stderr)

    def observe(self, mode, action="snapshot"):
        result = subprocess.run([str(self.binary), mode, action], capture_output=True, text=True, timeout=5)
        return result.returncode, json.loads(result.stdout), json.loads(result.stderr)

    def assert_no_input(self, stats):
        self.assertEqual([stats[key] for key in ("focus", "key_posts", "press")], [0, 0, 0])

    def test_result_already_in_view_does_not_invalidate_sibling(self):
        code, row, stats = self.observe("scroll-invalidates-sibling")
        self.assertEqual((code, row.get("expected_visible"), stats["scroll"]), (0, True, 0), (row, stats))
        self.assertNotIn("result_scroll_attempted", next(x for x in row["tree"] if x.get("result_intersects_window")))
        self.assert_no_input(stats)

    def test_offscreen_result_still_scrolls_and_measures_geometry(self):
        code, row, stats = self.observe("result-offscreen")
        self.assertEqual((code, row.get("expected_visible"), stats["scroll"]), (0, True, 1), (row, stats))
        self.assertIs(next(x for x in row["tree"] if x.get("result_intersects_window"))["result_scroll_attempted"], True)
        self.assert_no_input(stats)

    def test_offscreen_scroll_happens_after_the_last_sibling_read(self):
        code, row, stats = self.observe("result-offscreen-invalidates-sibling")
        self.assertEqual((code, row.get("expected_visible"), stats["scroll"]), (0, True, 1), (row, stats))
        self.assertEqual(stats["sibling_role_reads"], 1)
        self.assert_no_input(stats)

    def test_invalid_sibling_before_scroll_rejects_the_partial_graph(self):
        code, row, stats = self.observe("result-offscreen-stale-sibling")
        self.assertEqual((code, row.get("ax_attribute"), row.get("ax_error")), (2, "AXRole", -25202))
        self.assertEqual(stats["scroll"], 0)
        self.assert_no_input(stats)

    def test_invalid_expected_result_after_scroll_cannot_be_accepted(self):
        code, row, stats = self.observe("result-offscreen-invalidates-result")
        self.assertEqual((code, row.get("ax_attribute"), row.get("ax_error")), (2, "AXRole", -25202))
        self.assertEqual(stats["scroll"], 1)
        self.assertIs(row["result_scroll_attempted"], True)
        self.assertEqual(row["result_scroll_status"], 0)
        self.assert_no_input(stats)

    def test_changed_text_after_scroll_cannot_prove_the_expected_result(self):
        code, row, stats = self.observe("result-offscreen-changes-text")
        self.assertEqual((code, row.get("expected_visible"), stats["scroll"]), (0, False, 1), (row, stats))
        self.assert_no_input(stats)

    def test_unverified_result_geometry_refuses_before_scrolling(self):
        code, row, stats = self.observe("result-frame-refused")
        self.assertEqual((code, row.get("ax_attribute"), row.get("ax_error")), (2, "AXPosition", -25202))
        self.assertEqual(stats["scroll"], 0)
        self.assert_no_input(stats)

    def test_process_default_is_set_checked_and_released_before_remote_reads(self):
        for mode in ("leaf-no-value", "leaf-unsupported"):
            with self.subTest(mode=mode):
                code, row, stats = self.observe(mode)
                self.assertEqual(code, 0)
                self.assertIs(row["ready"], True)
                self.assertEqual([stats[key] for key in ("global_creates", "global_sets", "global_releases", "app_sets")], [1]*4)
                self.assertIs(stats["timeout_order_ok"], True)
                self.assertEqual(stats["system_attribute_reads"], 0)
                self.assert_no_input(stats)

    def test_failed_global_timeout_stops_before_app_and_remote_reads(self):
        code, row, stats = self.observe("global-failure", "fill")
        self.assertEqual(code, 2)
        self.assertIs(row.get("ax_timeout_failed"), True)
        self.assertEqual(row.get("ax_error"), -25204)
        self.assertEqual([stats[key] for key in ("global_creates", "global_sets", "global_releases")], [1]*3)
        self.assertEqual([stats[key] for key in ("app_sets", "remote_reads", "activation", "raise")], [0]*4)
        self.assert_no_input(stats)

    def test_child_role_unknown_reaches_actual_main_refusal_before_fill_and_send(self):
        self.assert_child_unknown("role-failure", "AXRole")

    def test_child_children_unknown_cannot_hide_composer_before_fill_and_send(self):
        self.assert_child_unknown("children-failure", "AXChildren")

    def assert_child_unknown(self, mode, attribute):
        for action in ("snapshot", "fill", "send"):
            with self.subTest(action=action):
                code, row, stats = self.observe(mode, action)
                self.assertEqual(code, 2)
                self.assertIs(row.get("ax_read_failed"), True)
                self.assertEqual((row.get("ax_error"), row.get("ax_attribute"), row.get("ax_read_reason")),
                                 (-25204, attribute, "api_error"))
                self.assertNotIn("ready", row)
                self.assertEqual(stats["global_releases"], 1)
                self.assertIs(stats["timeout_order_ok"], True)
                self.assertEqual(stats["system_attribute_reads"], 0)
                self.assert_no_input(stats)


if __name__ == "__main__":
    unittest.main(verbosity=2)
