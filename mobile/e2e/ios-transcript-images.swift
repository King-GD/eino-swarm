import XCTest

// Packaged offline fixture with /?mock=1&tick=0&history=input-images and Local mode.
final class BindFlowTests: XCTestCase {
  func testPCAttachedImagesLoadOnlyAfterTappingView() {
    continueAfterFailure = false
    let app = XCUIApplication()
    app.launch()
    let back = app.buttons["Back"].firstMatch
    XCTAssertTrue(back.waitForExistence(timeout: 12))
    back.tap()

    for phase in ["open", "reopen"] {
      let thread = app.buttons.matching(NSPredicate(format: "label CONTAINS 'Sweep the unused exports'")).firstMatch
      XCTAssertTrue(thread.waitForExistence(timeout: 8))
      thread.tap()
      XCTAssertTrue(app.staticTexts["Screenshots from the PC"].waitForExistence(timeout: 8))
      let pictures = app.images.matching(identifier: "Image")
      let actions = app.buttons.matching(identifier: "View image")
      XCTAssertTrue(actions.firstMatch.waitForExistence(timeout: 8))
      XCTAssertEqual(actions.count, 3)
      XCTAssertEqual(pictures.count, 0)
      record(app, phase + "-before-view")

      XCTAssertTrue(actions.firstMatch.isHittable)
      actions.firstMatch.tap()
      XCTAssertTrue(pictures.firstMatch.waitForExistence(timeout: 8))
      XCTAssertEqual(pictures.count, 1)
      XCTAssertEqual(actions.count, 2)
      XCTAssertTrue(pictures.firstMatch.isHittable)
      XCTAssertGreaterThan(pictures.firstMatch.frame.width, 100)
      record(app, phase + "-after-view")
      XCTAssertTrue(back.isHittable)
      back.tap()
    }
  }

  private func record(_ app: XCUIApplication, _ phase: String) {
    let screenshot = XCTAttachment(screenshot: app.screenshot())
    screenshot.name = "pc-input-images-" + phase
    screenshot.lifetime = .keepAlways
    add(screenshot)
  }
}
