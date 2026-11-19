import XCTest

final class BindFlowTests: XCTestCase {
  func testInboxSearchKeepsTheHeaderAndResultsAboveTheKeyboard() throws {
    let app = XCUIApplication()
    app.launch()
    let back = app.buttons["Back"]
    XCTAssertTrue(back.waitForExistence(timeout: 20))
    back.tap()
    let search = app.descendants(matching: .any).matching(identifier: "Search conversations").firstMatch
    let menu = app.buttons["Menu"]
    let newChat = app.buttons["New chat"]
    XCTAssertTrue(search.waitForExistence(timeout: 10))
    XCTAssertTrue(menu.isHittable)
    let initialHeader = menu.frame
    let initialSearch = search.frame
    XCTAssertGreaterThan(initialSearch.minY, app.frame.midY, "Search starts in the bottom bar")
    capture(app, "inbox-before-search")

    search.tap()
    let keyboard = app.keyboards.firstMatch
    XCTAssertTrue(keyboard.waitForExistence(timeout: 8))
    search.typeText("Trim")
    let match = app.buttons["Open Trim the layout pass"]
    XCTAssertTrue(match.waitForExistence(timeout: 8))
    capture(app, "inbox-search-with-keyboard")
    XCTAssertTrue(match.isHittable)
    XCTAssertTrue(menu.isHittable)
    XCTAssertTrue(newChat.isHittable)
    XCTAssertEqual(menu.frame.minY, initialHeader.minY, accuracy: 2)
    XCTAssertEqual(search.frame.width, initialSearch.width, accuracy: 2)
    XCTAssertLessThanOrEqual(newChat.frame.maxX, app.frame.maxX)
    XCTAssertLessThanOrEqual(search.frame.maxY, keyboard.frame.minY + 2)
    XCTAssertLessThanOrEqual(match.frame.maxY, keyboard.frame.minY + 2)
    XCTAssertLessThanOrEqual(match.frame.maxY, search.frame.minY, "Search remains below the results with the keyboard open")

    let dismissal = app.buttons.matching(NSPredicate(format: "label IN %@", ["Done", "Hide keyboard", "Dismiss keyboard"])).firstMatch
    if dismissal.exists {
      dismissal.tap()
    } else {
      app.typeText("\n")
    }
    XCTAssertTrue(keyboard.waitForNonExistence(timeout: 6))
    capture(app, "inbox-search-after-dismissal")
    XCTAssertEqual(menu.frame.minY, initialHeader.minY, accuracy: 2)
    XCTAssertEqual(search.frame.minY, initialSearch.minY, accuracy: 2)
    XCTAssertTrue(match.isHittable, "Dismissal retains the query and reachable matches")
  }

  private func capture(_ app: XCUIApplication, _ name: String) {
    let screenshot = XCTAttachment(screenshot: app.screenshot())
    screenshot.name = name
    screenshot.lifetime = .keepAlways
    add(screenshot)
  }
}
