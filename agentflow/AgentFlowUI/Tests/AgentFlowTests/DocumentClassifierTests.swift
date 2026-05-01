import XCTest
@testable import AgentFlowUI

final class DocumentClassifierTests: XCTestCase {

    // MARK: - Doctype slug → category

    func testDoctypePleadings() {
        XCTAssertEqual(
            DocumentClassifier.classify(filename: "anything.pdf",
                                        doctype: "civil_complaint"),
            .pleadings
        )
        XCTAssertEqual(
            DocumentClassifier.classify(filename: "x.docx",
                                        doctype: "power_of_attorney"),
            .pleadings
        )
    }

    func testDoctypeEvidence() {
        XCTAssertEqual(
            DocumentClassifier.classify(filename: "scan.jpg",
                                        doctype: "wechat_pay_receipt"),
            .evidence
        )
        XCTAssertEqual(
            DocumentClassifier.classify(filename: "ledger.xlsx",
                                        doctype: "spreadsheet_shipment_ledger"),
            .evidence
        )
    }

    func testDoctypeIdAndContracts() {
        XCTAssertEqual(
            DocumentClassifier.classify(filename: "front.jpg",
                                        doctype: "resident_id_card"),
            .idAndContracts
        )
    }

    func testDoctypeCourtForms() {
        XCTAssertEqual(
            DocumentClassifier.classify(filename: "filing.pdf",
                                        doctype: "online_case_filing_confirmation"),
            .courtForms
        )
        XCTAssertEqual(
            DocumentClassifier.classify(filename: "statute.pdf",
                                        doctype: "legal_statute_excerpt"),
            .courtForms
        )
    }

    // MARK: - Filename heuristics

    func testFilenameChinesePleadings() {
        XCTAssertEqual(
            DocumentClassifier.classify(filename: "原告起诉状-final.pdf",
                                        doctype: nil),
            .pleadings
        )
    }

    func testFilenameChineseEvidence() {
        XCTAssertEqual(
            DocumentClassifier.classify(filename: "微信聊天截图-2025.png",
                                        doctype: ""),
            .evidence
        )
    }

    func testFilenameEnglishCommunications() {
        XCTAssertEqual(
            DocumentClassifier.classify(filename: "client_email_thread.eml",
                                        doctype: nil),
            .communications
        )
    }

    func testFilenameEnglishCourtForms() {
        XCTAssertEqual(
            DocumentClassifier.classify(filename: "summons_2024-03.pdf",
                                        doctype: nil),
            .courtForms
        )
    }

    func testFilenameIdAndContracts() {
        XCTAssertEqual(
            DocumentClassifier.classify(filename: "rental_agreement.pdf",
                                        doctype: nil),
            .idAndContracts
        )
    }

    func testFilenameOtherFallback() {
        XCTAssertEqual(
            DocumentClassifier.classify(filename: "random_file.bin",
                                        doctype: nil),
            .other
        )
    }

    // MARK: - Doctype "other" falls through to filename

    func testDoctypeOtherFallsThroughToFilenameHeuristics() {
        // doctype == "other" should be ignored and filename used instead
        XCTAssertEqual(
            DocumentClassifier.classify(filename: "complaint_v3.pdf",
                                        doctype: "other"),
            .pleadings
        )
    }

    // MARK: - Category surface

    func testEachCategoryHasNonEmptyPresentation() {
        for category in DocCategory.allCases {
            XCTAssertFalse(category.label.isEmpty,
                           "label empty for \(category)")
            XCTAssertFalse(category.systemImage.isEmpty,
                           "systemImage empty for \(category)")
            XCTAssertEqual(category.id, category.rawValue)
        }
    }
}
