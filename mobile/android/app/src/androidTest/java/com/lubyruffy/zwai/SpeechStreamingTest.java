package com.lubyruffy.zwai;

import static org.junit.Assert.*;

import android.app.Instrumentation;
import android.app.UiAutomation;
import android.content.Intent;
import android.graphics.Bitmap;
import android.graphics.Rect;
import android.os.Bundle;
import android.os.SystemClock;
import android.system.Os;
import android.system.OsConstants;
import android.view.InputDevice;
import android.view.KeyEvent;
import android.view.MotionEvent;
import android.view.accessibility.AccessibilityNodeInfo;
import android.view.inputmethod.InputMethodManager;
import android.webkit.WebView;
import com.getcapacitor.BridgeActivity;
import androidx.test.ext.junit.runners.AndroidJUnit4;
import androidx.test.platform.app.InstrumentationRegistry;
import java.io.File;
import java.io.FileOutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.Set;
import java.util.List;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicReference;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;

/** Native touches and the shipping WebView/fetch/audio APIs against a fresh
 * loopback CAS + actual Pairlink PC (account-fixture --speech-stream). This is not physical speaker acceptance. */
@RunWith(AndroidJUnit4.class)
public class SpeechStreamingTest {
    private final Instrumentation instrumentation = InstrumentationRegistry.getInstrumentation();
    private final UiAutomation ui = instrumentation.getUiAutomation();
    private BridgeActivity activity;
    private String origin;
    private File evidence;
    private final JSONArray actions = new JSONArray();

    @Test public void pcmAndSSEStartBeforeEOFStopTheSourceAndReturnToIdle() throws Exception {
        origin = InstrumentationRegistry.getArguments().getString("fixture_origin", "");
        assertTrue("A fresh owned loopback fixture is required", origin.matches("http://127\\.0\\.0\\.1:[0-9]+"));
        evidence = new File(instrumentation.getTargetContext().getExternalFilesDir(null), "native-speech-stream-" + SystemClock.uptimeMillis());
        assertTrue("Evidence must be a fresh directory", evidence.mkdir()); Os.chmod(evidence.getPath(), 0700);
        Bundle location = new Bundle(); location.putString("evidence_directory", evidence.getAbsolutePath()); instrumentation.sendStatus(2, location);
        try {
            get("/fixture/control?review=approved&pc=show&api=on&start=normal&catalog=normal&model=normal");
            JSONObject config = request("/fixture/speech/setup", "POST");
            assertEquals("gateway", config.getString("execution")); assertEquals(origin, config.getString("gateway_origin"));
            assertEquals("pcm", config.getJSONObject("output").getString("format"));
            Intent launch = instrumentation.getTargetContext().getPackageManager().getLaunchIntentForPackage(instrumentation.getTargetContext().getPackageName());
            assertNotNull(launch); launch.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_CLEAR_TASK);
            android.app.Activity reached = instrumentation.startActivitySync(launch);
            assertTrue(reached instanceof BridgeActivity); activity = (BridgeActivity) reached;
            click("Sign in"); waitVisible("User center");
            click("Other server"); fill("Server address", origin); click("Use this server"); click("Sign in");
            browser("Sign in to test gateway"); click("Sign in to test gateway");
            browser("Authorize test device"); click("Authorize test device");
            waitVisible("Device authorized. Return to zwai.");
            Intent foreground = instrumentation.getTargetContext().getPackageManager().getLaunchIntentForPackage(instrumentation.getTargetContext().getPackageName());
            assertNotNull(foreground);
            foreground.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK | Intent.FLAG_ACTIVITY_REORDER_TO_FRONT | Intent.FLAG_ACTIVITY_SINGLE_TOP);
            instrumentation.getTargetContext().startActivity(foreground);
            // A fresh account with one online PC connects automatically after consent.
            waitForeground(); clickContaining("Native speech stream");
            waitVisible("Read aloud");
            // Reviewed pass-through observer; it cannot initiate media, mutate
            // auth/storage/DOM or simulate the Read/Stop actions below.
            evaluate(raw("/fixture/speech/observer.js", "GET"));
            assertEquals("true", evaluate("Boolean(window.__zwaiSpeechObserverInstalled)"));
            save("01-connected-real-PC-speech-config");
            for (String format : new String[]{"raw", "sse"}) {
                request("/fixture/speech/mode?value="+format+"-held", "POST");
                JSONObject prior = state(); int baseline = highestNode(prior);
                int before = starts(prior).length(), requestsBefore = requests(prior).length();
                click("Read aloud");
                JSONObject playing = waitAudio(before, requestsBefore);
                assertPCM(playing, before);
                JSONObject active = lastRequest(playing);
                assertEquals("Phone speech uses its own Mobile data plane", "/v1/audio/speech", active.getString("route"));
                assertFalse("Native start must precede HTTP EOF", active.getBoolean("eof"));
                waitVisible("Stop reading aloud"); save(format+"-started-before-EOF");
                assertFalse(lastRequest(state()).getBoolean("eof"));
                click("Stop reading aloud"); waitRequest("cancelled"); waitSettled(baseline,true); waitAbsent("Stop reading aloud"); waitVisible("Read aloud");
                save(format+"-native-Stop-disconnected-source");
                request("/fixture/speech/mode?value="+format+"-finite", "POST");
                JSONObject settled = state(); assertTrue(nodesSettled(settled,baseline,true));
                int normalBaseline = highestNode(settled);
                before = starts(settled).length(); requestsBefore = requests(settled).length();
                click("Read aloud");
                assertPCM(waitAudio(before, requestsBefore), before);
                waitRequest("eof"); waitSettled(normalBaseline,false); waitAbsent("Stop reading aloud"); waitVisible("Read aloud");
                assertFalse("No player failure is allowed", visibleText().contains("Speech could not be completed"));
                save(format+"-successful-EOF-idle");
            }
            JSONArray calls = requests(state()); assertEquals(4, calls.length());
            for (int i=0; i<calls.length(); i++) assertEquals("/v1/audio/speech", calls.getJSONObject(i).getString("route"));
            write("final-fixture-state.json", state().toString(2));
        } catch (Exception | AssertionError failure) {
            try { save("failure"); write("failure-fixture-state.json", state().toString(2)); }
            catch (Exception | AssertionError observation) { failure.addSuppressed(observation); }
            throw failure;
        } finally { if (activity != null) instrumentation.runOnMainSync(() -> activity.finish()); }
    }

    private JSONArray requests(JSONObject state) { return state.optJSONArray("requests") == null ? new JSONArray() : state.optJSONArray("requests"); }
    private JSONArray starts(JSONObject state) throws Exception {
        JSONArray result=new JSONArray(), all=state.optJSONArray("observations");
        if (all != null) for (int i=0;i<all.length();i++) if ("start".equals(all.getJSONObject(i).optString("event"))) result.put(all.getJSONObject(i));
        return result;
    }
    private JSONObject lastRequest(JSONObject state) throws Exception { JSONArray calls=requests(state); assertTrue(calls.length()>0); return calls.getJSONObject(calls.length()-1); }
    private JSONObject waitAudio(int startsBefore, int requestsBefore) throws Exception {
        long deadline=SystemClock.uptimeMillis()+12000;
        do { JSONObject value=state(); if (starts(value).length()>startsBefore && requests(value).length()>requestsBefore && hasPCM(value,startsBefore)) return value; SystemClock.sleep(100); } while (SystemClock.uptimeMillis()<deadline);
        throw new AssertionError("The actual native audio source never started");
    }
    private int highestNode(JSONObject value) throws Exception {
        JSONArray events=value.optJSONArray("observations");int maximum=0;if(events!=null)for(int i=0;i<events.length();i++)maximum=Math.max(maximum,events.getJSONObject(i).optInt("node_id"));return maximum;
    }
    private boolean nodesSettled(JSONObject value,int baseline,boolean stopped) throws Exception {
        JSONArray events=value.optJSONArray("observations");if(events==null)return false;
        Set<Integer> started=new HashSet<>(),ended=new HashSet<>(),stopIDs=new HashSet<>(),disconnected=new HashSet<>(),contexts=new HashSet<>(),closed=new HashSet<>();int samples=0,firstStop=Integer.MAX_VALUE;
        for(int i=0;i<events.length();i++){
            JSONObject event=events.getJSONObject(i);int id=event.optInt("node_id");
            if("context_closed".equals(event.optString("event"))&&"closed".equals(event.optString("context_state")))closed.add(event.optInt("context_id"));
            if(id<=baseline)continue;
            switch(event.optString("event")){
                case "start":started.add(id);contexts.add(event.optInt("context_id"));samples+=event.optInt("length");break;
                case "ended":ended.add(id);break;
                case "disconnect":disconnected.add(id);break;
                case "stop":stopIDs.add(id);firstStop=Math.min(firstStop,event.optInt("sequence",Integer.MAX_VALUE));break;
                default:break;
            }
        }
        if(started.isEmpty())return false;
        if(!stopped)return ended.equals(started)&&stopIDs.isEmpty()&&samples==24000;
        if(firstStop==Integer.MAX_VALUE)return false;
        Set<Integer> active=new HashSet<>();
        for(int i=0;i<events.length();i++){
            JSONObject begin=events.getJSONObject(i);int id=begin.optInt("node_id");if(id<=baseline||!"start".equals(begin.optString("event"))||begin.optInt("sequence",Integer.MAX_VALUE)>=firstStop)continue;
            boolean endedBefore=false;for(int j=0;j<events.length();j++){JSONObject end=events.getJSONObject(j);if(id==end.optInt("node_id")&&"ended".equals(end.optString("event"))&&end.optInt("sequence",Integer.MAX_VALUE)<firstStop)endedBefore=true;}
            if(!endedBefore)active.add(id);
        }
        return !active.isEmpty()&&stopIDs.containsAll(active)&&started.containsAll(stopIDs)&&disconnected.containsAll(active)&&!contexts.isEmpty()&&closed.containsAll(contexts);
    }
    private void waitSettled(int baseline,boolean stopped) throws Exception {
        long until=SystemClock.uptimeMillis()+12000;
        do{JSONObject value=state();if(nodesSettled(value,baseline,stopped)){write("settled-nodes-"+baseline+".json",value.toString(2));return;}SystemClock.sleep(100);}while(SystemClock.uptimeMillis()<until);
        throw new AssertionError(stopped?"Every active source must actually stop/disconnect and its context close promise resolve":"Normal EOF must play all 24000 samples, end every node and never stop early");
    }
    private boolean hasPCM(JSONObject state,int before) throws Exception {
        JSONArray values=starts(state);for(int i=before;i<values.length();i++){JSONObject sample=values.getJSONObject(i);if(sample.optBoolean("nonzero")&&sample.optDouble("minimum")<0&&sample.optDouble("maximum")>0)return true;}return false;
    }
    private void assertPCM(JSONObject state,int before) throws Exception {
        JSONArray samples=starts(state); boolean nonSilent=false;
        for (int i=before;i<samples.length();i++) {
            JSONObject sample=samples.getJSONObject(i);
            assertEquals("Actual context must run after the native gesture", "running", sample.getString("context_state"));
            assertEquals(24000,sample.getInt("sample_rate")); assertEquals(1,sample.getInt("channels")); assertTrue(sample.getInt("length")>0);
            nonSilent |= sample.optBoolean("nonzero") && sample.optDouble("minimum")<0 && sample.optDouble("maximum")>0;
        }
        assertTrue("Observe actual non-silent native PCM samples",nonSilent);
        write("native-audio-start-"+before+".json",state.toString(2));
    }
    private void waitRequest(String flag) throws Exception {
        long deadline=SystemClock.uptimeMillis()+12000;
        do { if (lastRequest(state()).optBoolean(flag)) return; SystemClock.sleep(100); } while(SystemClock.uptimeMillis()<deadline);
        throw new AssertionError("The actual HTTP source did not reach "+flag);
    }
    private JSONObject state() throws Exception { return get("/fixture/speech/state"); }
    private JSONObject get(String path) throws Exception { return request(path,"GET"); }
    private JSONObject request(String path,String method) throws Exception { return new JSONObject(raw(path,method)).getJSONObject("data"); }
    private String raw(String path,String method) throws Exception {
        HttpURLConnection connection=(HttpURLConnection)new URL(origin+path).openConnection();
        connection.setRequestMethod(method); connection.setConnectTimeout(5000); connection.setReadTimeout(5000);
        try { assertEquals(200,connection.getResponseCode()); return new String(connection.getInputStream().readAllBytes(),StandardCharsets.UTF_8); }
        finally { connection.disconnect(); }
    }
    private String evaluate(String script) throws Exception {
        CountDownLatch done=new CountDownLatch(1); AtomicReference<String> result=new AtomicReference<>();
        instrumentation.runOnMainSync(() -> activity.getBridge().getWebView().evaluateJavascript(script,value->{result.set(value);done.countDown();}));
        assertTrue("The packaged WebView observation must return",done.await(5,TimeUnit.SECONDS)); return result.get();
    }
    private JSONObject dom() throws Exception {
        String script="JSON.stringify({width:innerWidth,height:innerHeight,entries:[...document.querySelectorAll('button,input,textarea')].map(e=>{const r=e.getBoundingClientRect(),x=r.x+r.width/2,y=r.y+r.height/2;return {label:e.getAttribute('aria-label')||e.labels?.[0]?.innerText||e.placeholder||e.innerText,editable:e.matches('input,textarea'),value:e.matches('input,textarea')?e.value:'',enabled:!e.disabled,visible:r.width>0&&r.height>0&&x>0&&y>0&&x<innerWidth&&y<innerHeight&&e.contains(document.elementFromPoint(x,y)),x:r.x,y:r.y,width:r.width,height:r.height}})})";
        return new JSONObject(new JSONArray("["+evaluate(script)+"]").getString(0));
    }
    private boolean foreground() { AccessibilityNodeInfo root=ui.getRootInActiveWindow(); return root!=null && instrumentation.getTargetContext().getPackageName().contentEquals(root.getPackageName()==null?"":root.getPackageName()); }
    private void waitForeground() { long until=SystemClock.uptimeMillis()+10000; while (!foreground() && SystemClock.uptimeMillis()<until) SystemClock.sleep(50); assertTrue("The owned app must return to foreground",foreground()); }
    private List<AccessibilityNodeInfo> nodes() {
        List<AccessibilityNodeInfo> all=new ArrayList<>(); AccessibilityNodeInfo root=ui.getRootInActiveWindow(); if(root==null)return all;
        root.refresh();all.add(root); for(int i=0;i<all.size();i++)for(int j=0;j<all.get(i).getChildCount();j++){AccessibilityNodeInfo child=all.get(i).getChild(j);if(child!=null&&child.refresh())all.add(child);} return all;
    }
    private String label(AccessibilityNodeInfo node) { return String.valueOf(node.getText()==null?node.getContentDescription():node.getText()); }
    private boolean present(String text) throws Exception {
        if(foreground()) { JSONArray entries=dom().getJSONArray("entries"); for(int i=0;i<entries.length();i++){JSONObject e=entries.getJSONObject(i);if(e.optBoolean("visible")&&text.equals(e.optString("label")))return true;} }
        for(AccessibilityNodeInfo node:nodes())if(node.isVisibleToUser()&&text.equals(label(node)))return true; return false;
    }
    private void waitVisible(String text) throws Exception { long until=SystemClock.uptimeMillis()+20000; do{if(present(text))return;SystemClock.sleep(100);}while(SystemClock.uptimeMillis()<until);throw new AssertionError("No native visible control: "+text); }
    private void waitAbsent(String text) throws Exception { long until=SystemClock.uptimeMillis()+15000; do{if(!present(text))return;SystemClock.sleep(100);}while(SystemClock.uptimeMillis()<until);throw new AssertionError("Native playback did not return to idle: "+text); }
    private void browser(String target) throws Exception { long until=SystemClock.uptimeMillis()+25000; do{if(present(target))return;for(String initial:new String[]{"Accept & continue","Use without an account","No thanks"})if(present(initial))click(initial);SystemClock.sleep(100);}while(SystemClock.uptimeMillis()<until);throw new AssertionError("Browser did not reach "+target); }
    private void click(String text) throws Exception { click(text,false); }
    private void clickContaining(String text) throws Exception { click(text,true); }
    private void click(String text,boolean contains) throws Exception {
        long until=SystemClock.uptimeMillis()+20000;
        do {
            if(foreground()) {
                JSONObject viewport=dom();JSONArray entries=viewport.getJSONArray("entries");
                for(int i=0;i<entries.length();i++){JSONObject e=entries.getJSONObject(i);String name=e.optString("label");if(e.optBoolean("visible")&&e.optBoolean("enabled")&&(contains?name.contains(text):name.equals(text))){touch(viewport,e);return;}}
            } else for(AccessibilityNodeInfo node:nodes())if(node.isVisibleToUser()&&node.isEnabled()&&(contains?label(node).contains(text):label(node).equals(text))){Rect r=new Rect();node.getBoundsInScreen(r);tap(r.centerX(),r.centerY());return;}
            SystemClock.sleep(100);
        }while(SystemClock.uptimeMillis()<until);throw new AssertionError("No unoccluded native target "+text);
    }
    private void fill(String name,String value) throws Exception {
        JSONObject viewport=dom();JSONArray entries=viewport.getJSONArray("entries");JSONObject target=null;
        for(int i=0;i<entries.length();i++){JSONObject e=entries.getJSONObject(i);if(name.equals(e.optString("label"))&&e.optBoolean("editable")&&e.optBoolean("visible"))target=e;}
        assertNotNull(target);touch(viewport,target);
        instrumentation.sendKeySync(new KeyEvent(0,0,KeyEvent.ACTION_DOWN,KeyEvent.KEYCODE_A,0,KeyEvent.META_CTRL_ON));
        instrumentation.sendKeySync(new KeyEvent(0,0,KeyEvent.ACTION_UP,KeyEvent.KEYCODE_A,0,KeyEvent.META_CTRL_ON));instrumentation.sendStringSync(value);
        instrumentation.runOnMainSync(() -> ((InputMethodManager)activity.getSystemService(android.app.Activity.INPUT_METHOD_SERVICE)).hideSoftInputFromWindow(activity.getBridge().getWebView().getWindowToken(),0));
        long until=SystemClock.uptimeMillis()+4000;
        do{JSONArray current=dom().getJSONArray("entries");for(int i=0;i<current.length();i++)if(name.equals(current.getJSONObject(i).optString("label"))&&value.equals(current.getJSONObject(i).optString("value")))return;SystemClock.sleep(100);}while(SystemClock.uptimeMillis()<until);
        throw new AssertionError("Native keyboard did not update "+name);
    }
    private void touch(JSONObject viewport,JSONObject entry) throws Exception {
        int[] geometry=new int[4];instrumentation.runOnMainSync(()->{WebView web=activity.getBridge().getWebView();web.getLocationOnScreen(geometry);geometry[2]=web.getWidth();geometry[3]=web.getHeight();});
        float x=(float)(geometry[0]+(entry.getDouble("x")+entry.getDouble("width")/2)*geometry[2]/viewport.getDouble("width"));
        float y=(float)(geometry[1]+(entry.getDouble("y")+entry.getDouble("height")/2)*geometry[3]/viewport.getDouble("height"));tap(x,y);
        actions.put(new JSONObject().put("action","native_touch_observed_DOM_bounds").put("label",entry.getString("label")).put("x",x).put("y",y));
    }
    private void tap(float x,float y) {
        long at=SystemClock.uptimeMillis();MotionEvent down=MotionEvent.obtain(at,at,MotionEvent.ACTION_DOWN,x,y,0),up=MotionEvent.obtain(at,at+50,MotionEvent.ACTION_UP,x,y,0);
        down.setSource(InputDevice.SOURCE_TOUCHSCREEN);up.setSource(InputDevice.SOURCE_TOUCHSCREEN);
        try{assertTrue(ui.injectInputEvent(down,true));assertTrue(ui.injectInputEvent(up,true));}finally{down.recycle();up.recycle();}
    }
    private String visibleText() { StringBuilder text=new StringBuilder();for(AccessibilityNodeInfo node:nodes())if(node.isVisibleToUser())text.append(label(node)).append('\n');return text.toString(); }
    private FileOutputStream output(String name) throws Exception { return new FileOutputStream(Os.open(new File(evidence,name).getPath(),OsConstants.O_WRONLY|OsConstants.O_CREAT|OsConstants.O_EXCL,0600)); }
    private void write(String name,String value) throws Exception { try(FileOutputStream stream=output(name)){stream.write(value.getBytes(StandardCharsets.UTF_8));} }
    private void save(String phase) throws Exception {
        if(foreground()) {
            CountDownLatch painted=new CountDownLatch(1);instrumentation.runOnMainSync(()->activity.getBridge().getWebView().postVisualStateCallback(SystemClock.uptimeMillis(),new WebView.VisualStateCallback(){@Override public void onComplete(long id){painted.countDown();}}));
            assertTrue("The observed DOM must reach the real native display",painted.await(5,TimeUnit.SECONDS));
        }
        Bitmap pixels=ui.takeScreenshot();assertNotNull(pixels);
        try(FileOutputStream stream=output(phase+".png")){assertTrue(pixels.compress(Bitmap.CompressFormat.PNG,100,stream));}
        int dark=0,light=0,total=0;for(int y=pixels.getHeight()/8;y<pixels.getHeight()*7/8;y+=8)for(int x=0;x<pixels.getWidth();x+=8){int color=pixels.getPixel(x,y),luma=(((color>>16)&255)*299+((color>>8)&255)*587+(color&255)*114)/1000;if(luma<90)dark++;if(luma>165)light++;total++;}pixels.recycle();
        write(phase+"-pixels.json",new JSONObject().put("dark",dark).put("light",light).put("samples",total).toString());
        assertTrue("The target image must contain real app content, not a blank black or white frame",dark*500>total&&light*500>total);
        write(phase+"-native-actions.json",actions.toString(2));write(phase+"-accessibility.txt",visibleText());
    }
}
