import XCTest
@testable import AgentFlowUI

final class DocumentInfoSmokeTests: XCTestCase {
    /// Decodes a fixture JSON matching the `/v1/cases/{id}/documents/list`
    /// response shape into `[DocumentInfo]`.
    func test_decodesFixtureIntoDocumentInfoArray() throws {
        let json = """
        [
          {
            "filename": "complaint.pdf",
            "doctype": "complaint",
            "ocr_indexed": true,
            "rag_indexed": true,
            "size_bytes": 204813,
            "modified_at": "2026-04-30T12:34:56Z"
          },
          {
            "filename": "exhibit_a.png",
            "doctype": null,
            "ocr_indexed": false,
            "rag_indexed": null,
            "size_bytes": 18244,
            "modified_at": "2026-05-01T08:15:00.123Z"
          },
          {
            "filename": "scratch.txt"
          }
        ]
        """.data(using: .utf8)!

        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { dec in
            let s = try dec.singleValueContainer().decode(String.self)
            let iso = ISO8601DateFormatter()
            iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let d = iso.date(from: s) { return d }
            iso.formatOptions = [.withInternetDateTime]
            if let d = iso.date(from: s) { return d }
            throw DecodingError.dataCorruptedError(in: try dec.singleValueContainer(),
                debugDescription: "Unparseable date \(s)")
        }

        let docs = try decoder.decode([DocumentInfo].self, from: json)

        XCTAssertEqual(docs.count, 3)

        XCTAssertEqual(docs[0].filename, "complaint.pdf")
        XCTAssertEqual(docs[0].id, "complaint.pdf")           // id == filename
        XCTAssertEqual(docs[0].doctype, "complaint")
        XCTAssertEqual(docs[0].ocr_indexed, true)
        XCTAssertEqual(docs[0].rag_indexed, true)
        XCTAssertEqual(docs[0].size_bytes, 204813)
        XCTAssertNotNil(docs[0].modified_at)

        XCTAssertEqual(docs[1].filename, "exhibit_a.png")
        XCTAssertNil(docs[1].doctype)
        XCTAssertEqual(docs[1].ocr_indexed, false)
        XCTAssertNil(docs[1].rag_indexed)
        XCTAssertNotNil(docs[1].modified_at)

        // Sparse object — every optional should decode to nil without error.
        XCTAssertEqual(docs[2].filename, "scratch.txt")
        XCTAssertNil(docs[2].doctype)
        XCTAssertNil(docs[2].ocr_indexed)
        XCTAssertNil(docs[2].rag_indexed)
        XCTAssertNil(docs[2].size_bytes)
        XCTAssertNil(docs[2].modified_at)
    }
}
