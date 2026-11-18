import XCTest

// Packaged offline fixture with /?mock=1&tick=4500&images=1 and IOS_LAYOUT_PHONE_MODE=local.
final class BindFlowTests: XCTestCase {
  func testPairedImageShowsProgressAndReopensFromHistory() {
    continueAfterFailure = false
    let app = XCUIApplication()
    app.launch()
    let back = app.buttons["Back"].firstMatch
    XCTAssertTrue(back.waitForExistence(timeout: 12))
    back.tap()
    openThread(app)

    let composer = app.textViews["Message"]
    XCTAssertTrue(composer.waitForExistence(timeout: 6))
    composer.tap()
    composer.typeText("/image Draw the coast")
    let send = app.buttons["Send"].firstMatch
    XCTAssertTrue(send.isHittable)
    send.tap()

    let pending = app.staticTexts["Generating image"].firstMatch
    XCTAssertTrue(pending.waitForExistence(timeout: 9))
    XCTAssertTrue(pending.isHittable)
    record(app, "generating")

    let preview = app.buttons["Open generated image"].firstMatch
    XCTAssertTrue(preview.waitForExistence(timeout: 14))
    XCTAssertFalse(pending.exists)
    assertCompleteImageVisible(app, preview: preview, composer: composer)
    record(app, "completed")
    preview.tap()

    let zoom = app.buttons["Zoom in"].firstMatch
    XCTAssertTrue(zoom.waitForExistence(timeout: 8))
    XCTAssertTrue(zoom.isEnabled)
    zoom.tap()
    record(app, "zoomed")
    let close = app.buttons["Close"].firstMatch
    XCTAssertTrue(close.isHittable)
    close.tap()
    XCTAssertTrue(zoom.waitForNonExistence(timeout: 6))

    XCTAssertTrue(back.isHittable)
    back.tap()
    openThread(app)
    XCTAssertTrue(preview.waitForExistence(timeout: 8))
    XCTAssertFalse(pending.exists)
    XCTAssertEqual(app.buttons.matching(identifier: "Open generated image").count, 1)
    assertCompleteImageVisible(app, preview: preview, composer: composer)
    record(app, "reopened")
  }

  private func assertCompleteImageVisible(_ app: XCUIApplication, preview: XCUIElement, composer: XCUIElement) {
    XCTAssertTrue(app.keyboards.firstMatch.waitForNonExistence(timeout: 5))
    XCTAssertTrue(preview.isHittable)
    let frame = preview.frame
    XCTAssertGreaterThan(frame.height, 100, "The card must show the generated image pixels")
    XCTAssertLessThanOrEqual(frame.maxY, composer.frame.minY, "The complete image must fit above the composer without a swipe")
    XCTAssertTrue(app.buttons["Download"].firstMatch.isHittable, "The image's save action must also be reachable")
  }

  private func openThread(_ app: XCUIApplication) {
    let thread = app.buttons.matching(NSPredicate(format: "label CONTAINS 'Sweep the unused exports'")).firstMatch
    XCTAssertTrue(thread.waitForExistence(timeout: 8))
    XCTAssertTrue(thread.isHittable)
    thread.tap()
  }

  override func tearDownWithError() throws {
    if (testRun?.failureCount ?? 0) > 0 { record(XCUIApplication(), "failure") }
  }

  private func record(_ app: XCUIApplication, _ phase: String) {
    let screen = XCTAttachment(screenshot: app.screenshot())
    screen.name = "paired-image-" + phase
    screen.lifetime = .keepAlways
    add(screen)
    let tree = XCTAttachment(string: app.debugDescription)
    tree.name = "paired-image-" + phase + "-accessibility"
    tree.lifetime = .keepAlways
    add(tree)
  }
}
