import XCTest
import UIKit

// Run in a fresh private simulator container through ios-layout-fixture.py:
// Start the owned account-fixture with --speech-stream.
// PRESERVE_PUBLIC=1, PHONE_MODE=account, QUERY=/, loopback GATEWAY_ORIGIN and
// SPEECH_OBSERVER=1. No production web bytes, state or media APIs are replaced.
final class BindFlowTests: XCTestCase {
  private let fixtureOrigin = "__ZWAI_GATEWAY_FIXTURE_ORIGIN__"

  func testNativePCMAndSSEStartBeforeEOFStopTheSourceAndFinishNormally() throws {
    continueAfterFailure = false
    try assertBlankImagesRejected()
    let app = XCUIApplication()
    defer { app.terminate() }
    _ = try read("/fixture/control?review=approved&pc=show&api=on&start=normal&catalog=normal&model=normal")
    let projection = try read("/fixture/speech/setup", method: "POST")
    XCTAssertEqual(projection["execution"] as? String, "gateway")
    XCTAssertEqual(projection["gateway_origin"] as? String, fixtureOrigin)
    XCTAssertEqual((projection["output"] as? [String: Any])?["format"] as? String, "pcm")
    app.launch()
    let login = app.buttons["Sign in"].firstMatch
    XCTAssertTrue(login.waitForExistence(timeout: 15)); login.tap()
    let browserLogin = app.buttons["Sign in to test gateway"].firstMatch
    if !browserLogin.waitForExistence(timeout: 3), login.exists && login.isEnabled && login.isHittable { login.tap() }
    XCTAssertTrue(browserLogin.waitForExistence(timeout: 15)); browserLogin.tap()
    let allow = app.buttons["Authorize test device"].firstMatch
    XCTAssertTrue(allow.waitForExistence(timeout: 15)); allow.tap()
    try returnFromBrowser(app)
    // A fresh account with one online PC connects automatically after consent.
    let conversation = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "Native speech stream")).firstMatch
    XCTAssertTrue(conversation.waitForExistence(timeout: 20)); conversation.tap()
    let readAloud = app.buttons["Read aloud"].firstMatch
    let stop = app.buttons["Stop reading aloud"].firstMatch
    XCTAssertTrue(readAloud.waitForExistence(timeout: 15))
    _ = try waitState { self.observations($0).contains { $0["event"] as? String == "installed" } }
    record(app, "connected-real-PC-config")

    for format in ["raw", "sse"] {
      _ = try read("/fixture/speech/mode?value=\(format)-held", method: "POST")
      let before = try state()
      let nodeBaseline = highestNode(before)
      let startCount = starts(before).count
      let requestCount = requests(before).count
      XCTAssertTrue(readAloud.isHittable); readAloud.tap()
      let playing = try waitState { self.hasPCM($0, after: startCount) && self.requests($0).count > requestCount }
      try assertNativePCM(playing, after: startCount)
      let request = try XCTUnwrap(requests(playing).last)
      XCTAssertEqual(request["route"] as? String, "/v1/audio/speech", "The phone must exercise its own Mobile data-plane route")
      XCTAssertEqual(request["eof"] as? Bool, false, "Successful AudioBufferSource.start must precede HTTP EOF")
      XCTAssertTrue(stop.waitForExistence(timeout: 5)); XCTAssertTrue(stop.isHittable)
      record(app, "\(format)-started-before-EOF")
      XCTAssertEqual(try XCTUnwrap(requests(state()).last)["eof"] as? Bool, false)
      stop.tap()
      _ = try waitState { self.requests($0).last?["cancelled"] as? Bool == true && self.nodesSettled($0, after: nodeBaseline, stopped: true) }
      XCTAssertTrue(stop.waitForNonExistence(timeout: 8)); XCTAssertTrue(readAloud.exists)
      record(app, "\(format)-native-Stop-disconnected-source")

      _ = try read("/fixture/speech/mode?value=\(format)-finite", method: "POST")
      let settled = try state()
      XCTAssertTrue(nodesSettled(settled, after: nodeBaseline, stopped: true))
      let normalBaseline = highestNode(settled)
      let normalStart = starts(settled).count
      readAloud.tap()
      let normal = try waitState { self.hasPCM($0, after: normalStart) }
      try assertNativePCM(normal, after: normalStart)
      let complete = try waitState { self.requests($0).last?["eof"] as? Bool == true && self.nodesSettled($0, after: normalBaseline, stopped: false) }
      XCTAssertTrue(nodesSettled(complete, after: normalBaseline, stopped: false))
      XCTAssertTrue(stop.waitForNonExistence(timeout: 12), "Successful EOF must drain the real native sources and return to idle")
      XCTAssertTrue(readAloud.exists)
      XCTAssertFalse(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "Speech could not be completed")).firstMatch.exists)
      record(app, "\(format)-successful-EOF-idle")
    }
    XCTAssertEqual(requests(try state()).count, 4)
    XCTAssertTrue(requests(try state()).allSatisfy { $0["route"] as? String == "/v1/audio/speech" })
  }

  private func assertNativePCM(_ state: [String: Any], after: Int) throws {
    let samples = Array(starts(state).dropFirst(after))
    XCTAssertFalse(samples.isEmpty)
    XCTAssertTrue(hasPCM(state, after: after), "Observe actual non-silent native PCM samples")
    for sample in samples {
      XCTAssertEqual(sample["context_state"] as? String, "running", "A scheduled node in a suspended context is not native playback")
      XCTAssertEqual(sample["sample_rate"] as? Int, 24000)
      XCTAssertEqual(sample["channels"] as? Int, 1)
      XCTAssertGreaterThan(sample["length"] as? Int ?? 0, 0)
    }
    let evidence = XCTAttachment(string: String(data: try JSONSerialization.data(withJSONObject: state, options: .prettyPrinted), encoding: .utf8)!)
    evidence.name = "speech-stream-native-start-and-source-state"; evidence.lifetime = .keepAlways; add(evidence)
  }

  private func highestNode(_ value: [String: Any]) -> Int {
    observations(value).compactMap { $0["node_id"] as? Int }.max() ?? 0
  }
  // Match lifecycle by stable node identity, not a cumulative start counter.
  private func nodesSettled(_ value: [String: Any], after baseline: Int, stopped: Bool) -> Bool {
    let allEvents = observations(value)
    let events = allEvents.filter { ($0["node_id"] as? Int ?? 0) > baseline }
    let started = events.filter { $0["event"] as? String == "start" }
    if started.isEmpty { return false }
    let ended = Set(events.filter { $0["event"] as? String == "ended" }.compactMap { $0["node_id"] as? Int })
    let stoppedEvents = events.filter { $0["event"] as? String == "stop" }
    let stoppedIDs = Set(stoppedEvents.compactMap { $0["node_id"] as? Int })
    let startedIDs = Set(started.compactMap { $0["node_id"] as? Int })
    if !stopped {
      return ended == startedIDs && stoppedEvents.isEmpty && started.reduce(0) { $0 + ($1["length"] as? Int ?? 0) } == 24000
    }
    guard let firstStop = stoppedEvents.compactMap({ $0["sequence"] as? Int }).min() else { return false }
    let activeAtStop = Set(started.filter { start in
      let node = start["node_id"] as? Int ?? 0
      let begin = start["sequence"] as? Int ?? Int.max
      let priorEnd = events.contains { ($0["event"] as? String == "ended") && ($0["node_id"] as? Int == node) && ($0["sequence"] as? Int ?? Int.max) < firstStop }
      return begin < firstStop && !priorEnd
    }.compactMap { $0["node_id"] as? Int })
    let disconnected = Set(events.filter { $0["event"] as? String == "disconnect" }.compactMap { $0["node_id"] as? Int })
    let contexts = Set(started.compactMap { $0["context_id"] as? Int })
    let closed = Set(allEvents.filter { ($0["event"] as? String == "context_closed") && ($0["context_state"] as? String == "closed") }.compactMap { $0["context_id"] as? Int })
    return !activeAtStop.isEmpty && activeAtStop.isSubset(of: stoppedIDs) && stoppedIDs.isSubset(of: startedIDs) && activeAtStop.isSubset(of: disconnected) && !contexts.isEmpty && contexts.isSubset(of: closed)
  }
  private func hasPCM(_ value: [String: Any], after: Int) -> Bool {
    starts(value).dropFirst(after).contains { ($0["nonzero"] as? Bool == true) && ($0["minimum"] as? Double ?? 0) < 0 && ($0["maximum"] as? Double ?? 0) > 0 }
  }
  private func requests(_ value: [String: Any]) -> [[String: Any]] { value["requests"] as? [[String: Any]] ?? [] }
  private func observations(_ value: [String: Any]) -> [[String: Any]] { value["observations"] as? [[String: Any]] ?? [] }
  private func starts(_ value: [String: Any]) -> [[String: Any]] { observations(value).filter { $0["event"] as? String == "start" } }
  private func state() throws -> [String: Any] { try read("/fixture/speech/state") }
  private func waitState(_ ready: ([String: Any]) -> Bool) throws -> [String: Any] {
    let deadline = ProcessInfo.processInfo.systemUptime + 12
    repeat {
      let value = try state()
      if ready(value) { return value }
      Thread.sleep(forTimeInterval: 0.1)
    } while ProcessInfo.processInfo.systemUptime < deadline
    throw NSError(domain: "NativeSpeechStream", code: 1, userInfo: [NSLocalizedDescriptionKey: "The real audio source/observer did not reach the required state"])
  }
  private func read(_ path: String, method: String = "GET") throws -> [String: Any] {
    var request = URLRequest(url: try XCTUnwrap(URL(string: fixtureOrigin + path)))
    request.httpMethod = method; request.timeoutInterval = 5
    let done = expectation(description: "loopback audio fixture")
    var value: [String: Any]?, failure: Error?
    let task = URLSession.shared.dataTask(with: request) { data, response, error in
      defer { done.fulfill() }
      if let error = error { failure = error; return }
      guard let response = response as? HTTPURLResponse, response.statusCode == 200, let data = data else { failure = NSError(domain: "NativeSpeechStream", code: 2); return }
      do { value = (try JSONSerialization.jsonObject(with: data) as? [String: Any])?["data"] as? [String: Any] } catch { failure = error }
    }
    task.resume()
    if XCTWaiter.wait(for: [done], timeout: 6) != .completed { task.cancel(); throw NSError(domain: "NativeSpeechStream", code: 3) }
    if let failure = failure { throw failure }
    return try XCTUnwrap(value)
  }
  private func returnFromBrowser(_ app: XCUIApplication) throws {
    let deadline = ProcessInfo.processInfo.systemUptime + 8
    repeat {
      let close = app.otherElements["TopBrowserBar"].buttons.matching(identifier: "Close").allElementsBoundByIndex
      let done = app.buttons.matching(identifier: "Done").allElementsBoundByIndex
      let choices = (close + done).filter { $0.exists && $0.isHittable }
      if choices.count == 1 { choices[0].tap(); return }
      if choices.count > 1 { throw NSError(domain: "NativeSpeechStream", code: 4) }
      RunLoop.current.run(until: Date().addingTimeInterval(0.1))
    } while ProcessInfo.processInfo.systemUptime < deadline
    throw NSError(domain: "NativeSpeechStream", code: 5)
  }
  override func tearDownWithError() throws {
    if (testRun?.failureCount ?? 0) > 0 { record(XCUIApplication(), "failure", validate: false) }
    XCUIApplication().terminate()
  }
  private func record(_ app: XCUIApplication, _ phase: String, validate: Bool = true) {
    let captured = app.screenshot()
    let screenshot = XCTAttachment(screenshot: captured)
    screenshot.name = "speech-stream-" + phase
    screenshot.lifetime = .keepAlways
    add(screenshot)
    let tree = XCTAttachment(string: app.debugDescription)
    tree.name = "speech-stream-" + phase + "-accessibility"
    tree.lifetime = .keepAlways
    add(tree)
    if validate {
      do {
        let pixels = try hasVisibleContent(captured.image)
        let metrics = XCTAttachment(string: "dark=\(pixels.dark),light=\(pixels.light),samples=\(pixels.samples),visible=\(pixels.visible)")
        metrics.name = "speech-stream-" + phase + "-pixel-contrast"
        metrics.lifetime = .keepAlways
        add(metrics)
        XCTAssertTrue(pixels.visible, "the target screenshot must contain visible content, not only status bars or a microphone dot")
      } catch { XCTFail("could not inspect the captured target pixels: \(error)") }
    }
  }

  /// Exercise the actual classifier against images with no visible content.
  private func assertBlankImagesRejected() throws {
    let bounds = CGRect(x: 0, y: 0, width: 400, height: 800)
    for color in [UIColor.white, UIColor.black, UIColor.clear] {
      let image = UIGraphicsImageRenderer(size: bounds.size).image { context in
        color.setFill()
        context.fill(bounds)
      }
      XCTAssertFalse(try hasVisibleContent(image).visible, "blank images must never accept native visibility")
    }
  }

  /// Crop away OS status/home areas. Both dark and light samples are required,
  /// so neither a white nor a black image can pass on elapsed time or AX alone.
  private func hasVisibleContent(_ image: UIImage) throws -> (visible: Bool, dark: Int, light: Int, samples: Int) {
    let full = try XCTUnwrap(image.cgImage)
    let crop = try XCTUnwrap(full.cropping(to: CGRect(x: 0, y: CGFloat(full.height) * 0.12, width: CGFloat(full.width), height: CGFloat(full.height) * 0.80)))
    // Downscaling sparse text can blend every dark glyph into the background.
    // Sample original pixels so the same contrast rule measures actual content.
    let width = crop.width, height = crop.height
    var bytes = [UInt8](repeating: 0, count: width * height * 4)
    let space = CGColorSpaceCreateDeviceRGB()
    try bytes.withUnsafeMutableBytes { buffer in
      let context = try XCTUnwrap(CGContext(data: buffer.baseAddress, width: width, height: height, bitsPerComponent: 8,
        bytesPerRow: width * 4, space: space, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
      context.draw(crop, in: CGRect(x: 0, y: 0, width: CGFloat(width), height: CGFloat(height)))
    }
    var dark = 0, light = 0, samples = 0
    for y in stride(from: 0, to: height, by: 8) {
      for x in stride(from: 0, to: width, by: 8) {
        let offset = (y * width + x) * 4
        let red = Int(bytes[offset])
        let green = Int(bytes[offset + 1])
        let blue = Int(bytes[offset + 2])
        let luma = (red * 299 + green * 587 + blue * 114) / 1000
        if luma < 90 { dark += 1 }
        if luma > 165 { light += 1 }
        samples += 1
      }
    }
    return (dark * 500 > samples && light * 500 > samples, dark, light, samples)
  }
}
