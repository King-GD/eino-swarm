import XCTest

final class BindFlowTests: XCTestCase {
  func testLiveComposerOffersThreeDeliveryTimes() {
    continueAfterFailure = false
    let app = XCUIApplication()
    app.launch()

    let draft = element("Message", in: app)
    XCTAssertTrue(draft.waitForExistence(timeout: 20))
    draft.tap()
    XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 8))
    draft.typeText("Run after this turn")
    element("Follow-up", in: app).tap()
    XCTAssertTrue(element("Run after this turn", in: app).waitForExistence(timeout: 5))

    draft.tap()
    draft.typeText("Use on the next model call")
    element("Send options", in: app).tap()
    let insert = element("Insert into this turn", in: app)
    XCTAssertTrue(insert.waitForExistence(timeout: 8))
    XCTAssertTrue(insert.isHittable)
    XCTAssertTrue(element("Interrupt and insert", in: app).isHittable)
    capture(app, "composer-send-options")
    insert.tap()
    XCTAssertTrue(element("Waiting to insert", in: app).waitForExistence(timeout: 5))

    let selected = element("Send options for second waiting message", in: app)
    XCTAssertTrue(selected.waitForExistence(timeout: 5))
    selected.tap()
    element("Interrupt and insert", in: app).tap()
    XCTAssertTrue(element("Included in model input", in: app).firstMatch.waitForExistence(timeout: 5))
    XCTAssertTrue(element("first waiting message", in: app).exists)
    XCTAssertTrue(element("Run after this turn", in: app).exists)
    XCTAssertFalse(selected.exists)
    capture(app, "selected-message-inserted")
  }

  private func element(_ name: String, in app: XCUIApplication) -> XCUIElement {
    app.descendants(matching: .any).matching(identifier: name).firstMatch
  }

  private func capture(_ app: XCUIApplication, _ name: String) {
    let screenshot = XCTAttachment(screenshot: app.screenshot())
    screenshot.name = name
    screenshot.lifetime = .keepAlways
    add(screenshot)
  }
}
