package com.lubyruffy.zwai;

import static org.junit.Assert.*;

import android.Manifest;
import android.app.Activity;
import android.app.Instrumentation;
import android.app.UiAutomation;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Bitmap;
import android.graphics.Rect;
import android.os.Bundle;
import android.os.SystemClock;
import android.system.Os;
import android.view.InputDevice;
import android.view.KeyEvent;
import android.view.MotionEvent;
import android.view.WindowInsets;
import android.view.accessibility.AccessibilityNodeInfo;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import java.io.File;
import java.io.FileOutputStream;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;

/** The private packaged fixture uses the shipped web bytes and offline speech.
 * Emulator media proves permission/UI/encoding boundaries, never physical
 * microphone acoustics or a production model's recognition quality. */
@RunWith(AndroidJUnit4.class)
public class SpeechTest {
    private final Instrumentation instrumentation = InstrumentationRegistry.getInstrumentation();
    private final UiAutomation ui = instrumentation.getUiAutomation();
    private Activity activity;

    @Test
    public void voicePermissionDraftAndCompletedAnswerPlayback() throws Exception {
        Intent launch = instrumentation.getTargetContext().getPackageManager()
            .getLaunchIntentForPackage(instrumentation.getTargetContext().getPackageName());
        assertNotNull("The packaged app must have a launch Activity", launch);
        launch.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TASK);
        activity = instrumentation.startActivitySync(launch);
        try {
            waitForButton("Start voice input", true, 20000);
            waitForConversation();
            AccessibilityNodeInfo message = waitForStableInput();
            saveEvidence("ready-composer");
            tapNode(message);
            instrumentation.sendStringSync("native voice draft");
            waitForInputContaining("native voice draft");
            boolean[] ime = new boolean[1];
            instrumentation.runOnMainSync(() -> {
                WindowInsets insets = activity.getWindow().getDecorView().getRootWindowInsets();
                ime[0] = insets != null && insets.isVisible(WindowInsets.Type.ime());
            });
            if (ime[0]) instrumentation.sendKeyDownUpSync(KeyEvent.KEYCODE_BACK);
            waitForInputContaining("native voice draft");
            saveEvidence("typed-draft");

            tap("Start voice input");
            allowMicrophoneIfRequested();
            waitForRecording();
            assertEquals("The native microphone permission must actually be granted", PackageManager.PERMISSION_GRANTED,
                instrumentation.getTargetContext().checkSelfPermission(Manifest.permission.RECORD_AUDIO));
            AccessibilityNodeInfo finish = waitForButton("Finish and transcribe", true, 8000);
            assertTrue("Recording must expose an enabled completion control", finish.isEnabled());
            saveEvidence("recording-controls");
            tap("Cancel voice input");
            waitForAbsentButton("Finish and transcribe", 5000);
            assertEquals("Cancelling must retain the original editable draft", "native voice draft", inputValue());
            saveEvidence("cancel-preserves-draft");

            tap("Start voice input");
            waitForRecording();
            SystemClock.sleep(700);
            saveEvidence("second-recording-controls");
            tap("Finish and transcribe");
            waitForInputContaining("Voice draft");
            assertTrue("Recognition must not overwrite the original draft", inputValue().contains("native voice draft"));
            saveEvidence("recognized-editable-draft");
            tapNode(waitForSubmit());
            waitForEmptyInput();

            waitForButton("Read aloud", true, 15000);
            assertNotNull("Completed answers retain Copy beside Read", findButton("Copy response", true));
            saveEvidence("answer-ready");
            tap("Read aloud");
            waitForButton("Stop reading aloud", true, 6000);
            saveEvidence("synthesis-pending");
            tap("Stop reading aloud");
            waitForAbsentButton("Stop reading aloud", 5000);
            waitForButton("Read aloud", true, 5000);
            saveEvidence("playback-stopped");

            // The second click lets a real, decodable offline WAV finish through
            // the packaged WebView player rather than accepting a spinner alone.
            tap("Read aloud");
            waitForButton("Stop reading aloud", true, 6000);
            waitForAbsentButton("Stop reading aloud", 20000);
            waitForButton("Read aloud", true, 5000);
            assertFalse("The offline audio must decode and finish without a user-visible error", visibleText().contains("Speech could not be completed"));
            saveEvidence("playback-completed");
            Bundle evidence = new Bundle();
            evidence.putString("evidence_directory", evidenceDirectory().getAbsolutePath());
            instrumentation.sendStatus(0, evidence);
        } catch (Exception | AssertionError failure) {
            saveEvidence("failure");
            Bundle evidence = new Bundle();
            evidence.putString("evidence_directory", evidenceDirectory().getAbsolutePath());
            instrumentation.sendStatus(0, evidence);
            throw failure;
        }
    }

    private void allowMicrophoneIfRequested() throws Exception {
        long deadline = SystemClock.uptimeMillis() + 10000;
        while (SystemClock.uptimeMillis() < deadline) {
            if (instrumentation.getTargetContext().checkSelfPermission(Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED
                && visibleText().contains("Recording") && findButton("Finish and transcribe", true) != null) return;
            for (AccessibilityNodeInfo node : nodes()) {
                String id = String.valueOf(node.getViewIdResourceName());
                String text = String.valueOf(node.getText());
                if (node.isVisibleToUser() && node.isEnabled() && node.isClickable()
                    && (id.endsWith("/permission_allow_foreground_only_button")
                        || text.equals("While using the app") || text.equals("Allow")
                        || text.equals("仅在使用该应用时") || text.equals("允许"))) {
                    saveEvidence("microphone-permission");
                    // The permission window can move when the IME finishes
                    // closing. Activate its actual native button by identity,
                    // then require the permission result rather than a tap ACK.
                    assertTrue("The native microphone approval must accept its action", node.performAction(AccessibilityNodeInfo.ACTION_CLICK));
                    while (SystemClock.uptimeMillis() < deadline) {
                        if (instrumentation.getTargetContext().checkSelfPermission(Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) return;
                        SystemClock.sleep(50);
                    }
                    throw new AssertionError("The microphone approval did not grant the requested native permission");
                }
            }
            SystemClock.sleep(50);
        }
        throw new AssertionError("Neither a native microphone permission prompt nor recording controls appeared");
    }

    private void waitForRecording() throws Exception {
        long deadline = SystemClock.uptimeMillis() + 10000;
        while (SystemClock.uptimeMillis() < deadline) {
            if (visibleText().matches("(?s).*Recording · [1-9][0-9]*s.*") && findButton("Finish and transcribe", true) != null) return;
            SystemClock.sleep(50);
        }
        throw new AssertionError("The packaged app did not reach actual recording state");
    }

    private void tap(String label) throws Exception {
        // Streaming completion and the chart can still move the answer footer.
        // Wait for this actual target to settle, then deliver exactly one touch.
        long deadline = SystemClock.uptimeMillis() + 10000;
        Rect previous = new Rect();
        int stableSamples = 0;
        while (SystemClock.uptimeMillis() < deadline) {
            AccessibilityNodeInfo node = findButton(label, true);
            if (node != null) {
                Rect current = new Rect(); node.getBoundsInScreen(current);
                stableSamples = !current.isEmpty() && current.equals(previous) ? stableSamples + 1 : 0;
                if (stableSamples >= 2) { tapNode(node); return; }
                previous = current;
            } else stableSamples = 0;
            SystemClock.sleep(100);
        }
        throw new AssertionError("Native touch target did not settle: " + label);
    }

    private void tapNode(AccessibilityNodeInfo node) {
        Rect bounds = new Rect();
        node.getBoundsInScreen(bounds);
        assertTrue("A native target must have nonempty visible bounds", onScreen(node) && !bounds.isEmpty());
        long now = SystemClock.uptimeMillis();
        MotionEvent down = MotionEvent.obtain(now, now, MotionEvent.ACTION_DOWN, bounds.centerX(), bounds.centerY(), 0);
        MotionEvent up = MotionEvent.obtain(now, now + 30, MotionEvent.ACTION_UP, bounds.centerX(), bounds.centerY(), 0);
        down.setSource(InputDevice.SOURCE_TOUCHSCREEN); up.setSource(InputDevice.SOURCE_TOUCHSCREEN);
        try {
            assertTrue("The native touch down must be delivered", ui.injectInputEvent(down, true));
            assertTrue("The native touch up must be delivered", ui.injectInputEvent(up, true));
        } finally { down.recycle(); up.recycle(); }
    }

    private AccessibilityNodeInfo waitForButton(String label, boolean enabled, long timeout) {
        long deadline = SystemClock.uptimeMillis() + timeout;
        while (SystemClock.uptimeMillis() < deadline) {
            AccessibilityNodeInfo node = findButton(label, enabled);
            if (node != null) return node;
            SystemClock.sleep(50);
        }
        throw new AssertionError("No native accessible button: " + label);
    }

    private void waitForAbsentButton(String label, long timeout) {
        long deadline = SystemClock.uptimeMillis() + timeout;
        while (SystemClock.uptimeMillis() < deadline) {
            if (findButton(label, false) == null) return;
            SystemClock.sleep(50);
        }
        throw new AssertionError("Native action remained present: " + label);
    }

    private AccessibilityNodeInfo findButton(String label, boolean enabled) {
        for (AccessibilityNodeInfo node : nodes()) {
            if ((label.contentEquals(node.getText() == null ? "" : node.getText())
                    || label.contentEquals(node.getContentDescription() == null ? "" : node.getContentDescription()))
                && "android.widget.Button".contentEquals(node.getClassName()) && onScreen(node)
                && (!enabled || node.isEnabled() && node.isClickable())) return node;
        }
        return null;
    }

    private AccessibilityNodeInfo waitForInput() {
        long deadline = SystemClock.uptimeMillis() + 20000;
        while (SystemClock.uptimeMillis() < deadline) {
            for (AccessibilityNodeInfo node : nodes()) {
                if ("android.widget.EditText".contentEquals(node.getClassName()) && node.isEditable() && onScreen(node)) return node;
            }
            SystemClock.sleep(50);
        }
        throw new AssertionError("The packaged offline conversation has no editable composer");
    }

    private void waitForConversation() {
        long deadline = SystemClock.uptimeMillis() + 15000;
        while (SystemClock.uptimeMillis() < deadline) {
            if (visibleText().contains("Trim the layout pass") && findButton("Outputs", true) != null) return;
            SystemClock.sleep(50);
        }
        throw new AssertionError("The identified offline PC conversation did not become visible");
    }

    private AccessibilityNodeInfo waitForStableInput() {
        long deadline = SystemClock.uptimeMillis() + 5000;
        Rect previous = new Rect();
        while (SystemClock.uptimeMillis() < deadline) {
            AccessibilityNodeInfo input = waitForInput();
            Rect current = new Rect(); input.getBoundsInScreen(current);
            if (!current.isEmpty() && previous.equals(current)) return input;
            previous = current;
            SystemClock.sleep(100);
        }
        throw new AssertionError("The current native composer did not retain stable visible bounds");
    }

    private String inputValue() { return String.valueOf(waitForInput().getText()); }

    private void waitForInputContaining(String text) {
        long deadline = SystemClock.uptimeMillis() + 15000;
        while (SystemClock.uptimeMillis() < deadline) {
            if (inputValue().contains(text)) return;
            SystemClock.sleep(50);
        }
        throw new AssertionError("The editable composer did not receive the expected text");
    }

    private void waitForEmptyInput() {
        long deadline = SystemClock.uptimeMillis() + 5000;
        while (SystemClock.uptimeMillis() < deadline) {
            if (inputValue().isEmpty()) return;
            SystemClock.sleep(50);
        }
        throw new AssertionError("The native Send action did not acknowledge and clear this draft");
    }

    private AccessibilityNodeInfo waitForSubmit() {
        long deadline = SystemClock.uptimeMillis() + 5000;
        while (SystemClock.uptimeMillis() < deadline) {
            AccessibilityNodeInfo followup = findButton("Follow-up", true);
            if (followup != null) return followup;
            AccessibilityNodeInfo send = findButton("Send", true);
            if (send != null) return send;
            SystemClock.sleep(50);
        }
        throw new AssertionError("The current composer did not enable Send or Follow-up after recognition");
    }

    private boolean onScreen(AccessibilityNodeInfo node) {
        AccessibilityNodeInfo root = ui.getRootInActiveWindow();
        if (root == null || !node.isVisibleToUser()) return false;
        Rect viewport = new Rect(), bounds = new Rect();
        root.getBoundsInScreen(viewport); node.getBoundsInScreen(bounds);
        return !bounds.isEmpty() && Rect.intersects(viewport, bounds);
    }

    private List<AccessibilityNodeInfo> nodes() {
        List<AccessibilityNodeInfo> result = new ArrayList<>();
        AccessibilityNodeInfo root = ui.getRootInActiveWindow();
        if (root == null) return result;
        result.add(root);
        for (int i = 0; i < result.size(); i++) {
            AccessibilityNodeInfo node = result.get(i);
            for (int child = 0; child < node.getChildCount(); child++) {
                AccessibilityNodeInfo next = node.getChild(child);
                if (next != null) result.add(next);
            }
        }
        return result;
    }

    private String visibleText() {
        StringBuilder value = new StringBuilder();
        for (AccessibilityNodeInfo node : nodes()) if (node.isVisibleToUser()) {
            value.append(node.getText()).append('\n').append(node.getContentDescription()).append('\n');
        }
        return value.toString();
    }

    private File evidenceDirectory() {
        return new File(instrumentation.getTargetContext().getFilesDir(), "speech-native");
    }

    private void saveEvidence(String name) throws Exception {
        File directory = evidenceDirectory();
        assertTrue("Native evidence directory must be writable", directory.isDirectory() || directory.mkdirs());
        Os.chmod(directory.getAbsolutePath(), 0700);
        Bitmap screenshot = ui.takeScreenshot();
        assertNotNull("The reached native screen must be captured", screenshot);
        File image = new File(directory, name + ".png");
        assertTrue(image.isFile() || image.createNewFile());
        Os.chmod(image.getAbsolutePath(), 0600);
        try (FileOutputStream stream = new FileOutputStream(image)) {
            assertTrue(screenshot.compress(Bitmap.CompressFormat.PNG, 100, stream));
        }
        screenshot.recycle();
        JSONArray tree = new JSONArray();
        for (AccessibilityNodeInfo node : nodes()) {
            Rect bounds = new Rect(); node.getBoundsInScreen(bounds);
            tree.put(new JSONObject().put("class", String.valueOf(node.getClassName()))
                .put("text", String.valueOf(node.getText())).put("label", String.valueOf(node.getContentDescription()))
                .put("enabled", node.isEnabled()).put("clickable", node.isClickable())
                .put("visible", node.isVisibleToUser()).put("bounds", bounds.flattenToString()));
        }
        File data = new File(directory, name + ".json");
        assertTrue(data.isFile() || data.createNewFile());
        Os.chmod(data.getAbsolutePath(), 0600);
        try (FileOutputStream stream = new FileOutputStream(data)) {
            stream.write(tree.toString(2).getBytes(StandardCharsets.UTF_8));
        }
    }
}
