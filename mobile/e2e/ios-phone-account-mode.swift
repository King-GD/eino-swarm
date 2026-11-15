import XCTest

// Fresh private simulator container; ships the unmodified production web bundle.
final class BindFlowTests: XCTestCase {
  func testNativePhoneAccountGateAndPortraitModeSwitch() throws {
    continueAfterFailure = false
    let app = XCUIApplication()
    app.launch()
    defer { app.terminate() }
    XCTAssertTrue(app.buttons["Sign in"].waitForExistence(timeout: 20))
    XCTAssertFalse(app.buttons["Menu"].exists)
    XCTAssertFalse(app.buttons["Other ways"].exists)
    XCTAssertFalse(app.textViews["Message"].exists)
    record(app, "account-login-gate")

    let portrait = app.buttons["User center"]
    XCTAssertTrue(portrait.exists)
    portrait.tap(withNumberOfTaps: 3, numberOfTouches: 1)
    record(app, "after-native-three-taps")
    // The offline PC may open its most recent conversation on reconnect.
    if app.buttons["Back"].waitForExistence(timeout: 3) { app.buttons["Back"].tap() }
    let menu = app.buttons["Menu"]
    XCTAssertTrue(menu.waitForExistence(timeout: 15))
    menu.tap()
    XCTAssertTrue(app.menuItems["Models"].waitForExistence(timeout: 5))
    XCTAssertTrue(app.menuItems["Add a PC"].exists)
    XCTAssertTrue(app.menuItems["Unlink"].exists)
    record(app, "local-menu-restored")

    app.menuItems["User center"].tap()
    XCTAssertTrue(app.buttons["Sign in"].waitForExistence(timeout: 5))
    app.buttons["User center"].firstMatch.tap(withNumberOfTaps: 3, numberOfTouches: 1)
    XCTAssertFalse(app.buttons["Close"].exists)
    XCTAssertFalse(app.buttons["Menu"].exists)
    XCTAssertFalse(app.textViews["Message"].exists)
    record(app, "account-gate-restored")

    app.terminate()
    app.launch()
    XCTAssertTrue(app.buttons["Sign in"].waitForExistence(timeout: 15))
    XCTAssertFalse(app.buttons["Menu"].exists)
    record(app, "account-mode-persists")
  }

  private func record(_ app: XCUIApplication, _ name: String) {
    let screenshot = XCTAttachment(screenshot: app.screenshot())
    screenshot.name = name
    screenshot.lifetime = .keepAlways
    add(screenshot)
    let tree = XCTAttachment(string: app.debugDescription)
    tree.name = name + "-accessibility"
    tree.lifetime = .keepAlways
    add(tree)
  }
}
