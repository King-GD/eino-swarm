import XCTest

final class BindFlowTests: XCTestCase {
    func testPairedProjectKnowledgeShowsSourceAndFollowsPageLinks() {
        continueAfterFailure = false
        let app = XCUIApplication()
        app.launch()
        let back = app.buttons["Back"].firstMatch
        XCTAssertTrue(back.waitForExistence(timeout: 15))
        back.tap()
        let knowledge = app.buttons["Knowledge for Platform"]
        XCTAssertTrue(knowledge.waitForExistence(timeout: 8))
        knowledge.tap()

        let entry = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "Design log")).firstMatch
        XCTAssertTrue(entry.waitForExistence(timeout: 8))
        entry.tap()
        let crossReference = app.buttons["delivery reference"]
        XCTAssertTrue(crossReference.waitForExistence(timeout: 8))
        crossReference.tap()
        XCTAssertTrue(app.staticTexts["The companion entry."].waitForExistence(timeout: 8))
        app.buttons["Back"].firstMatch.tap()

        let excerpt = app.descendants(matching: .any).matching(identifier: "Source excerpt").firstMatch
        XCTAssertTrue(excerpt.waitForExistence(timeout: 8))
        if !excerpt.isHittable { app.swipeUp() }
        excerpt.tap()
        XCTAssertTrue(app.staticTexts["The reading view keeps the reference visible."].waitForExistence(timeout: 8))
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "phone-knowledge-source"
        shot.lifetime = .keepAlways
        add(shot)
        let source = app.buttons["Event #12"]
        XCTAssertTrue(source.exists)
        source.tap()
        XCTAssertTrue(app.staticTexts["Trim the layout pass"].waitForExistence(timeout: 8))
    }
}
