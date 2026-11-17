/* Private QA observer. It never calls playback, fetches audio, alters storage,
 * simulates input or changes a native result. Install before the first Read. */
(() => {
  if (window.__zwaiSpeechObserverInstalled) return;
  const origin = "__ZWAI_SPEECH_FIXTURE_ORIGIN__";
  const nodes = new WeakMap(), contexts = new WeakMap();
  let nextNodeID = 0, nextContextID = 0, nextSequence = 0;
  const contextID = (context) => {
    if (!contexts.has(context)) contexts.set(context, ++nextContextID);
    return contexts.get(context);
  };
  const report = (value) => {
    try {
    const request = window.CapacitorWebFetch || window.fetch;
    void request.call(window, origin + "/fixture/speech/observe", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ...value, sequence: ++nextSequence }), credentials: "omit", cache: "no-store", redirect: "error",
    }).catch(() => undefined);
    } catch { /* Observation failure cannot change the successful native call. */ }
  };
  const prototype = window.AudioBufferSourceNode?.prototype;
  if (!prototype) return;
  const start = prototype.start, stop = prototype.stop, disconnect = prototype.disconnect;
  prototype.start = function (...args) {
    const result = Reflect.apply(start, this, args);
    const nodeID = ++nextNodeID;
    nodes.set(this, nodeID);
    try {
    const buffer = this.buffer;
    const samples = buffer?.numberOfChannels ? buffer.getChannelData(0) : new Float32Array();
    let minimum = 0, maximum = 0, nonzero = false;
    for (const value of samples) {
      minimum = Math.min(minimum, value); maximum = Math.max(maximum, value);
      nonzero ||= Number.isFinite(value) && value !== 0;
    }
    report({ event: "start", node_id: nodeID, context_id: contextID(this.context), context_state: this.context.state, sample_rate: buffer?.sampleRate || 0,
      channels: buffer?.numberOfChannels || 0, length: samples.length, minimum, maximum, nonzero });
    this.addEventListener("ended", () => report({ event: "ended", node_id: nodeID, context_id: contextID(this.context), context_state: this.context.state }), { once: true });
    } catch { /* Preserve native start even if an observation is unavailable. */ }
    return result;
  };
  prototype.stop = function (...args) {
    const result = Reflect.apply(stop, this, args);
    report({ event: "stop", node_id: nodes.get(this) || 0, context_id: contextID(this.context), context_state: this.context.state });
    return result;
  };
  prototype.disconnect = function (...args) {
    const result = Reflect.apply(disconnect, this, args);
    report({ event: "disconnect", node_id: nodes.get(this) || 0, context_id: contextID(this.context), context_state: this.context.state });
    return result;
  };
  const contextPrototype = window.AudioContext?.prototype;
  if (contextPrototype) {
    const close = contextPrototype.close;
    contextPrototype.close = function (...args) {
      const result = Reflect.apply(close, this, args);
      // Observe the original promise, preserving its exact identity and result.
      void result.then(() => report({ event: "context_closed", context_id: contextID(this), context_state: this.state })).catch(() => undefined);
      return result;
    };
  }
  window.__zwaiSpeechObserverInstalled = true;
  report({ event: "installed" });
})();
