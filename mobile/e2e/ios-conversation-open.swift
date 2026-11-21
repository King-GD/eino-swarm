import XCTest

// Copy into a disposable ios-layout-fixture. Select hidden&pause=open&tick=0
// for the first method, streamed-agents&tick=150 for the independent live case.
final class BindFlowTests: XCTestCase {
  func testConversationReopenRetainsItsLiveEdgeAndEarlierHistory() throws {
    continueAfterFailure = false
    let app = XCUIApplication()
    app.launch()
    for visit in 0...1 {
      let answer = app.staticTexts["Earlier answer"]
      XCTAssertTrue(answer.waitForExistence(timeout: 15),
        "The latest stored answer must appear without any Earlier tap")
      XCTAssertTrue(answer.isHittable, "The answer must be on the native screen before any paging action")
      XCTAssertEqual(app.staticTexts.matching(identifier: "Earlier answer").count, 1)
      XCTAssertFalse(app.staticTexts["No visible messages in this turn yet. Open earlier history."].exists)
      XCTAssertTrue(app.buttons.matching(identifier: "Stop").allElementsBoundByIndex.contains { $0.isHittable },
        "Backfilling history must preserve a visible native Stop control")
      record(app, "conversation-open-\(visit)-automatic-answer")
      if visit == 0 { reopenFromPlatform(app) }
    }
  }

  func testLiveManagerUpdatesSurviveConversationReopen() throws {
    continueAfterFailure = false
    let app = XCUIApplication()
    app.launch()
    XCTAssertTrue(app.staticTexts["Manager update 0"].waitForExistence(timeout: 15),
      "The existing streamed-agents fixture must deliver a natural live update")
    XCTAssertTrue(app.staticTexts["Manager update 2"].waitForExistence(timeout: 8))
    for visit in 0...1 {
      for update in 0...2 {
        let message = app.staticTexts.matching(identifier: "Manager update \(update)")
        XCTAssertTrue(message.element(boundBy: 0).waitForExistence(timeout: 8))
        XCTAssertEqual(message.count, 1, "A reopening snapshot must not erase or duplicate live manager output")
      }
      XCTAssertTrue(app.buttons.matching(identifier: "Stop").allElementsBoundByIndex.contains { $0.isHittable })
      record(app, "conversation-live-\(visit)")
      if visit == 0 { reopenFromPlatform(app) }
    }
  }

  func testLegacyTurnPreviewsStayBrowsableBeforeJumping() throws {
    continueAfterFailure = false
    let app = XCUIApplication()
    app.launch()
    XCTAssertTrue(app.buttons["Back"].firstMatch.waitForExistence(timeout: 15))
    reopenFromPlatform(app)
    let newest = app.buttons["Jump to turn 3: Third prompt"].firstMatch
    let first = app.buttons["Jump to turn 1: First prompt"].firstMatch
    let second = app.buttons["Jump to turn 2: Second prompt"].firstMatch
    XCTAssertTrue(newest.waitForExistence(timeout: 15))
    XCTAssertTrue(first.waitForExistence(timeout: 10), "the legacy snapshot must recover the input label")
    XCTAssertTrue(second.exists)
    let railY = (newest.frame.midY - app.frame.minY) / app.frame.height
    let left = app.coordinate(withNormalizedOffset: CGVector(dx: 0.36, dy: railY))
    let right = app.coordinate(withNormalizedOffset: CGVector(dx: 0.97, dy: railY))
    left.press(forDuration: 0.06, thenDragTo: right)
    let olderX = second.frame.minX
    right.press(forDuration: 0.06, thenDragTo: left)
    let reachedNewer = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
      second.frame.minX < olderX - 15
    }, object: nil)
    XCTAssertEqual(XCTWaiter.wait(for: [reachedNewer], timeout: 5), .completed,
      "a native swipe must move the navigation strip")
    let newerX = second.frame.minX
    left.press(forDuration: 0.06, thenDragTo: right)
    let reachedOlder = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
      first.isHittable && second.frame.minX > newerX + 15
    }, object: nil)
    XCTAssertEqual(XCTWaiter.wait(for: [reachedOlder], timeout: 5), .completed,
      "a swipe ending at the right edge must scroll the rail, not navigate back")
    XCTAssertTrue(app.buttons["Back"].firstMatch.isHittable)
    XCTAssertFalse(app.staticTexts["Response 1"].firstMatch.exists, "browsing labels must not load the transcript")
    record(app, "legacy-turn-previews-before-jump")
    first.tap()
    XCTAssertTrue(app.staticTexts["Response 1"].firstMatch.waitForExistence(timeout: 8))
    XCTAssertTrue(app.staticTexts["First prompt"].firstMatch.isHittable)
    record(app, "legacy-turn-previews-after-jump")
  }

  func testPreviousTurnRemainsFullyTappable() throws {
    continueAfterFailure = false
    let app = XCUIApplication()
    app.launch()
    XCTAssertTrue(app.buttons["Back"].firstMatch.waitForExistence(timeout: 15))
    reopenFromPlatform(app)
    let rail = app.otherElements.matching(NSPredicate(format: "label BEGINSWITH %@", "Conversation turns")).firstMatch
    let latest = app.buttons["Jump to turn 7: Seventh prompt"].firstMatch
    let sixth = app.buttons["Jump to turn 6: Sixth prompt"].firstMatch
    let fifth = app.buttons["Jump to turn 5: Fifth prompt"].firstMatch
    XCTAssertTrue(latest.waitForExistence(timeout: 10))

    func tapVisible(_ button: XCUIElement) {
      let visible = XCTNSPredicateExpectation(predicate: NSPredicate { _, _ in
        let rect = button.frame
        let bounds = rail.frame
        return button.isHittable && rect.width > 0 && rect.minX >= bounds.minX - 1 && rect.maxX <= bounds.maxX + 1
      }, object: nil)
      XCTAssertEqual(XCTWaiter.wait(for: [visible], timeout: 5), .completed,
        "the whole turn button must fit before tapping")
      let rect = button.frame
      let appBounds = app.frame
      // A coordinate tap prevents XCTest from scrolling the element into view first.
      app.coordinate(withNormalizedOffset: CGVector(
        dx: (rect.midX - appBounds.minX) / appBounds.width,
        dy: (rect.midY - appBounds.minY) / appBounds.height
      )).tap()
    }

    tapVisible(sixth)
    XCTAssertTrue(app.staticTexts["Sixth prompt"].firstMatch.waitForExistence(timeout: 8))
    XCTAssertFalse(app.staticTexts["Fifth prompt"].firstMatch.exists)
    record(app, "turn-six-before-tapping-five")
    tapVisible(fifth)
    XCTAssertTrue(app.staticTexts["Fifth prompt"].firstMatch.waitForExistence(timeout: 8))
    XCTAssertTrue(app.staticTexts["Fifth prompt"].firstMatch.isHittable)
    record(app, "turn-five-after-coordinate-tap")
  }

  private func reopenFromPlatform(_ app: XCUIApplication) {
    let back = app.buttons["Back"]
    XCTAssertTrue(back.isHittable)
    back.tap()
    let platform = app.buttons.matching(NSPredicate(format: "label ==[c] %@", "Platform"))
    let following = app.buttons.matching(NSPredicate(format: "label ==[c] %@", "Field notes"))
    XCTAssertTrue(platform.element(boundBy: 0).waitForExistence(timeout: 8))
    XCTAssertEqual(platform.count, 1)
    XCTAssertEqual(following.count, 1)
    guard platform.count == 1 && following.count == 1 else { return }
    let start = platform.element(boundBy: 0).frame.maxY
    let end = following.element(boundBy: 0).frame.minY
    let allRows = app.buttons.matching(identifier: "Open Trim the layout pass").allElementsBoundByIndex
    // This source thread is also in In progress; choose only its Platform section.
    let rows = allRows.filter { $0.frame.midY > start && $0.frame.midY < end }
    let scope = XCTAttachment(string: "Platform=\(platform.element(boundBy: 0).frame), Field notes=\(following.element(boundBy: 0).frame), all Open frames=\(allRows.map { $0.frame }), scoped Open frames=\(rows.map { $0.frame })")
    scope.name = "conversation-open-native-project-scope"
    scope.lifetime = .keepAlways
    add(scope)
    XCTAssertEqual(rows.count, 1, "The same source host/project/title must identify exactly one native Open row")
    guard rows.count == 1 else { return }
    XCTAssertTrue(rows[0].isHittable)
    rows[0].tap()
  }

  override func tearDownWithError() throws {
    if (testRun?.failureCount ?? 0) > 0 {
      record(XCUIApplication(), "conversation-open-native-failure")
    }
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
