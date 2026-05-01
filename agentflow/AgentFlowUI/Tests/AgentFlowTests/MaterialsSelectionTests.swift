import XCTest
@testable import AgentFlowUI

@MainActor
final class MaterialsSelectionTests: XCTestCase {

    func testToggleAddsThenRemoves() {
        let s = MaterialsSelection()
        XCTAssertTrue(s.selected.isEmpty)

        s.toggle("a.pdf")
        XCTAssertEqual(s.selected, ["a.pdf"])

        s.toggle("a.pdf")
        XCTAssertTrue(s.selected.isEmpty)
    }

    func testContainsReflectsMembership() {
        let s = MaterialsSelection()
        XCTAssertFalse(s.contains("a.pdf"))

        s.toggle("a.pdf")
        XCTAssertTrue(s.contains("a.pdf"))
        XCTAssertFalse(s.contains("b.pdf"))
    }

    func testClearEmptiesSelection() {
        let s = MaterialsSelection()
        s.selectAll(["a.pdf", "b.pdf", "c.docx"])
        XCTAssertEqual(s.selected.count, 3)

        s.clear()
        XCTAssertTrue(s.selected.isEmpty)
    }

    func testSelectAllReplacesSelectionAndDedupes() {
        let s = MaterialsSelection()
        s.toggle("old.pdf")

        s.selectAll(["a.pdf", "b.pdf", "a.pdf"])
        XCTAssertEqual(s.selected, ["a.pdf", "b.pdf"])
        XCTAssertFalse(s.contains("old.pdf"))
    }
}
