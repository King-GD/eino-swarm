import XCTest

final class BindFlowTests: XCTestCase {
  func testComposerListsCommandsWithTheKeyboardOpen() {
    continueAfterFailure = false
    let app = XCUIApplication()
    app.launch()
    let back = app.buttons["Back"]
    XCTAssertTrue(back.waitForExistence(timeout: 20))
    back.tap()
    let newChat = app.buttons["New chat"]
    XCTAssertTrue(newChat.waitForExistence(timeout: 8))
    newChat.tap()
    let draft = app.descendants(matching: .any).matching(identifier: "New message").firstMatch
    XCTAssertTrue(draft.waitForExistence(timeout: 8))
    draft.tap()
    let keyboard = app.keyboards.firstMatch
    XCTAssertTrue(keyboard.waitForExistence(timeout: 8))
    draft.typeText("/")
    assertCommands(in: app)

    draft.typeText(XCUIKeyboardKey.delete.rawValue + "Draw a coast /")
    assertCommands(in: app)
    let image = command("image", in: app)
    XCTAssertLessThanOrEqual(image.frame.maxY, keyboard.frame.minY)
    let screenshot = XCTAttachment(screenshot: app.screenshot())
    screenshot.name = "slash-menu-after-draft"
    screenshot.lifetime = .keepAlways
    add(screenshot)
    image.tap()
    XCTAssertEqual(draft.value as? String, "/image Draw a coast")
    XCTAssertTrue(command("goal", in: app).waitForNonExistence(timeout: 5))
    XCTAssertTrue(keyboard.exists)
  }

  private func command(_ name: String, in app: XCUIApplication) -> XCUIElement {
    app.descendants(matching: .any)
      .matching(NSPredicate(format: "label BEGINSWITH %@", "/" + name)).firstMatch
  }

  private func assertCommands(in app: XCUIApplication, file: StaticString = #filePath, line: UInt = #line) {
    for name in ["goal", "plan", "image"] {
      let item = command(name, in: app)
      XCTAssertTrue(item.waitForExistence(timeout: 5), name, file: file, line: line)
      XCTAssertTrue(item.isHittable, name, file: file, line: line)
    }
  }
}
