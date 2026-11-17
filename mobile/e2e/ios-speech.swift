import XCTest
import UIKit

/// Run the private packaged fixture with speech=local&speech_delay=1500.
/// This checks native permissions and controls; a physical microphone's acoustic
/// transcription accuracy remains a separate real-device acceptance result.
final class BindFlowTests: XCTestCase {
  func testVisibleContentRejectsBlankLightAndDarkImages() throws {
    for dark in [false, true] {
      let blank = UIGraphicsImageRenderer(size: CGSize(width: 320, height: 640)).image { context in
        (dark ? UIColor.black : UIColor.white).setFill()
        context.fill(CGRect(x: 0, y: 0, width: 320, height: 640))
      }
      XCTAssertFalse(try hasVisibleContent(blank).visible, "a uniform light or dark screenshot is not UI evidence")
      let content = UIGraphicsImageRenderer(size: CGSize(width: 320, height: 640)).image { context in
        (dark ? UIColor.black : UIColor.white).setFill()
        context.fill(CGRect(x: 0, y: 0, width: 320, height: 640))
        (dark ? UIColor.white : UIColor.black).setFill()
        context.fill(CGRect(x: 60, y: 300, width: 200, height: 60))
      }
      XCTAssertTrue(try hasVisibleContent(content).visible, "real contrast must pass in both appearances")
    }
  }

  func testVoicePanelCancelsWithoutSendingAndCompletedAnswerCanBeStopped() throws {
    continueAfterFailure = false
    let app = XCUIApplication()
    let allowMicrophone = addUIInterruptionMonitor(withDescription: "Speech microphone permission") { alert in
      let description = alert.debugDescription.lowercased()
      guard description.contains("microphone") || description.contains("麦克风") else { return false }
      for label in ["Allow", "OK", "允许", "好"] {
        if alert.buttons[label].exists { alert.buttons[label].tap(); return true }
      }
      return false
    }
    defer { removeUIInterruptionMonitor(allowMicrophone) }
    app.launch()
    let message = app.textViews["Message"].firstMatch
    XCTAssertTrue(message.waitForExistence(timeout: 20), "open the identified offline PC conversation")
    record(app, "initial-visible-admission")
    message.tap()
    app.typeText("native voice draft")
    let microphone = app.buttons["Start voice input"].firstMatch
    XCTAssertTrue(microphone.waitForExistence(timeout: 8))
    microphone.tap()
    app.tap() // Deliver any native permission interruption to the monitor.
    let finish = app.buttons["Finish and transcribe"].firstMatch
    XCTAssertTrue(finish.waitForExistence(timeout: 12), "the recording controls must be in the packaged app")
    let ready = XCTNSPredicateExpectation(predicate: NSPredicate(format: "enabled == true"), object: finish)
    XCTAssertEqual(XCTWaiter.wait(for: [ready], timeout: 8), .completed, "permission must lead to a live capture before completion is offered")
    XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH 'Recording'")).firstMatch.exists)
    let cancel = app.buttons["Cancel voice input"].firstMatch
    XCTAssertTrue(cancel.isHittable)
    record(app, "recording-controls")
    cancel.tap()
    XCTAssertTrue(finish.waitForNonExistence(timeout: 5))
    XCTAssertEqual(message.value as? String, "native voice draft", "cancel cannot send or replace the original draft")
    microphone.tap()
    XCTAssertTrue(finish.waitForExistence(timeout: 8))
    let duration = app.staticTexts.matching(NSPredicate(format: "label MATCHES 'Recording · [1-9][0-9]*s'")).firstMatch
    XCTAssertTrue(duration.waitForExistence(timeout: 8), "collect a real native recording before transcribing")
    XCTAssertTrue(finish.isEnabled)
    record(app, "recording-before-transcription")
    finish.tap()
    let recognized = XCTNSPredicateExpectation(predicate: NSPredicate(format: "value CONTAINS 'Voice draft'"), object: message)
    XCTAssertEqual(XCTWaiter.wait(for: [recognized], timeout: 15), .completed,
      "actual MediaRecorder bytes must decode in the offline ASR adapter and return to the draft")
    let draft = try XCTUnwrap(message.value as? String)
    XCTAssertTrue(draft.hasPrefix("native voice draft"))
    XCTAssertTrue(draft.hasSuffix("Voice draft"))
    record(app, "native-transcription-editable-draft")
    let submit = app.buttons["Follow-up"].firstMatch.exists ? app.buttons["Follow-up"].firstMatch : app.buttons["Send"].firstMatch
    XCTAssertTrue(submit.isEnabled)
    submit.tap()
    let read = app.buttons["Read aloud"].firstMatch
    XCTAssertTrue(read.waitForExistence(timeout: 12), "only a completed response has the read action")
    XCTAssertTrue(app.buttons["Copy response"].firstMatch.exists)
    read.tap()
    let stop = app.buttons["Stop reading aloud"].firstMatch
    XCTAssertTrue(stop.waitForExistence(timeout: 8), "the real player must expose cancellation while synthesis waits")
    record(app, "synthesis-pending")
    stop.tap()
    XCTAssertTrue(stop.waitForNonExistence(timeout: 5))
    XCTAssertTrue(read.exists)
    XCTAssertFalse(app.staticTexts["Speech could not be completed. Check the PC settings and try again."].exists)
    record(app, "playback-cancelled")
    read.tap()
    XCTAssertTrue(stop.waitForExistence(timeout: 8))
    record(app, "second-playback-started")
    XCTAssertTrue(stop.waitForNonExistence(timeout: 15), "a decoded native audio segment must finish playback without Stop")
    XCTAssertTrue(read.exists)
    XCTAssertFalse(app.staticTexts["Speech could not be completed. Check the PC settings and try again."].exists)
    record(app, "native-playback-finished")
  }

  override func tearDownWithError() throws {
    if (testRun?.failureCount ?? 0) > 0 { record(XCUIApplication(), "failure", validate: false) }
  }

  private func record(_ app: XCUIApplication, _ phase: String, validate: Bool = true) {
    let captured = app.screenshot()
    let screenshot = XCTAttachment(screenshot: captured)
    screenshot.name = "speech-" + phase
    screenshot.lifetime = .keepAlways
    add(screenshot)
    let tree = XCTAttachment(string: app.debugDescription)
    tree.name = "speech-" + phase + "-accessibility"
    tree.lifetime = .keepAlways
    add(tree)
    if validate {
      do {
        let pixels = try hasVisibleContent(captured.image)
        let metrics = XCTAttachment(string: "dark=\(pixels.dark),light=\(pixels.light),samples=\(pixels.samples),visible=\(pixels.visible)")
        metrics.name = "speech-" + phase + "-pixel-contrast"
        metrics.lifetime = .keepAlways
        add(metrics)
        XCTAssertTrue(pixels.visible, "the target screenshot must contain visible content, not only status bars or a microphone dot")
      } catch { XCTFail("could not inspect the captured target pixels: \(error)") }
    }
  }

  /// Crop away OS status/home areas. Both dark and light samples are required,
  /// so neither a white nor a black image can pass on elapsed time or AX alone.
  private func hasVisibleContent(_ image: UIImage) throws -> (visible: Bool, dark: Int, light: Int, samples: Int) {
    let full = try XCTUnwrap(image.cgImage)
    let crop = try XCTUnwrap(full.cropping(to: CGRect(x: 0, y: CGFloat(full.height) * 0.12, width: CGFloat(full.width), height: CGFloat(full.height) * 0.80)))
    let width = 160, height = 320
    var bytes = [UInt8](repeating: 0, count: width * height * 4)
    let space = CGColorSpaceCreateDeviceRGB()
    try bytes.withUnsafeMutableBytes { buffer in
      let context = try XCTUnwrap(CGContext(data: buffer.baseAddress, width: width, height: height, bitsPerComponent: 8,
        bytesPerRow: width * 4, space: space, bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
      context.draw(crop, in: CGRect(x: 0, y: 0, width: CGFloat(width), height: CGFloat(height)))
    }
    var dark = 0, light = 0
    for offset in stride(from: 0, to: bytes.count, by: 4) {
      let luma = (Int(bytes[offset]) * 299 + Int(bytes[offset + 1]) * 587 + Int(bytes[offset + 2]) * 114) / 1000
      if luma < 90 { dark += 1 }
      if luma > 165 { light += 1 }
    }
    let samples = width * height
    return (dark * 500 > samples && light * 500 > samples, dark, light, samples)
  }
}
