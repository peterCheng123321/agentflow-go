import Foundation
import SwiftUI

// MARK: - Category model

/// Coarse-grained category buckets for documents shown in the materials hub.
/// Each case carries a presentation label, an SF Symbol name, and a tint so
/// callers can render a consistent badge without hard-coding strings.
enum DocCategory: String, CaseIterable, Identifiable {
    case pleadings
    case evidence
    case idAndContracts
    case communications
    case courtForms
    case other

    var id: String { rawValue }

    var label: String {
        switch self {
        case .pleadings:        return "Pleadings"
        case .evidence:         return "Evidence"
        case .idAndContracts:   return "ID & contracts"
        case .communications:   return "Communications"
        case .courtForms:       return "Court forms"
        case .other:            return "Other"
        }
    }

    var systemImage: String {
        switch self {
        case .pleadings:        return "doc.text.fill"
        case .evidence:         return "magnifyingglass.circle.fill"
        case .idAndContracts:   return "person.text.rectangle.fill"
        case .communications:   return "bubble.left.and.bubble.right.fill"
        case .courtForms:       return "building.columns.fill"
        case .other:            return "doc.fill"
        }
    }

    var tint: Color {
        switch self {
        case .pleadings:        return .blue
        case .evidence:         return .purple
        case .idAndContracts:   return .teal
        case .communications:   return .orange
        case .courtForms:       return .indigo
        case .other:            return .gray
        }
    }
}

// MARK: - Classifier

/// Pure-logic mapping from a document's backend doctype slug (or filename, if
/// no doctype is available) to a `DocCategory`.
///
/// The doctype slugs come from the Go backend's intake classifier; new slugs
/// added there should be reflected in `categoryForDoctype` below. The filename
/// heuristic is a best-effort fallback for legacy uploads or files where the
/// classifier hasn't run yet.
enum DocumentClassifier {

    /// Map a document to its category. Prefer the backend doctype slug when
    /// present; fall back to filename / extension heuristics otherwise.
    static func classify(filename: String, doctype: String?) -> DocCategory {
        if let slug = doctype?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased(),
           !slug.isEmpty,
           slug != "other",
           let category = categoryForDoctype(slug) {
            return category
        }
        return categoryForFilename(filename)
    }

    // MARK: Doctype slugs

    private static func categoryForDoctype(_ slug: String) -> DocCategory? {
        switch slug {
        case "civil_complaint",
             "civil_petition_fragment",
             "power_of_attorney",
             "litigation_service_address_confirmation":
            return .pleadings

        case "wechat_chat_screenshot",
             "printed_chat_evidence",
             "wechat_pay_receipt",
             "iou_debt_note",
             "spreadsheet_shipment_ledger":
            return .evidence

        case "resident_id_card",
             "household_registration_query_result":
            return .idAndContracts

        case "online_case_filing_confirmation",
             "litigation_fee_refund_account_form",
             "court_form_other",
             "legal_statute_excerpt":
            return .courtForms

        default:
            return nil
        }
    }

    // MARK: Filename heuristics

    private static func categoryForFilename(_ filename: String) -> DocCategory {
        let lowered = filename.lowercased()
        for (category, keywords) in filenameKeywords {
            if keywords.contains(where: { lowered.contains($0) }) {
                return category
            }
        }
        return .other
    }

    /// Ordered keyword table; first match wins. Order matters because some
    /// substrings overlap (e.g. an ID-card filename containing "id" should
    /// not be reclassified as evidence by a later rule).
    private static let filenameKeywords: [(DocCategory, [String])] = [
        (.pleadings,        ["起诉状", "complaint", "petition", "motion"]),
        (.evidence,         ["证据", "凭证", "截图", "evidence", "screenshot", "receipt"]),
        (.idAndContracts,   ["身份证", "户口", "id", "contract", "agreement"]),
        (.courtForms,       ["传票", "送达", "summons", "hearing"]),
        (.communications,   ["chat", "email", "letter", "correspondence"]),
    ]
}
