import Foundation

// MARK: - API response envelopes

struct CasesResponse: Decodable {
    let cases: [Case]
    let count: Int
}

struct StatusResponse: Decodable {
    let cases: [Case]?
    let count: Int?
    let rag: RAGSummary?
    let websocket_clients: Int?
}

struct RAGSummary: Decodable {
    let document_count: Int?
    let chunk_count: Int?
    let tokenized: Bool?
}

// MARK: - Domain

struct Case: Decodable, Identifiable, Hashable {
    let case_id: String
    let client_name: String
    let matter_type: String
    let source_channel: String
    let initial_msg: String
    let state: String
    let notes: [Note]?
    let uploaded_documents: [String]?
    let created_at: Date
    let updated_at: Date
    let evaluation: String?
    let hitl_approvals: [String: Bool]?
    let is_paid: Bool
    let node_history: [String]?

    var id: String { case_id }

    var displayName: String {
        client_name.isEmpty ? case_id : client_name
    }

    var docCount: Int { uploaded_documents?.count ?? 0 }
    var noteCount: Int { notes?.count ?? 0 }

    var needsLawyerAttention: Bool {
        guard let approvals = hitl_approvals else { return false }
        return approvals.values.contains(false)
    }
}

struct Note: Decodable, Hashable {
    let text: String
    let timestamp: Date
}

// MARK: - Workflow states

enum WorkflowState: String, CaseIterable {
    case clientCapture      = "CLIENT_CAPTURE"
    case initialContact     = "INITIAL_CONTACT"
    case caseEvaluation     = "CASE_EVALUATION"
    case feeCollection      = "FEE_COLLECTION"
    case caseIntake         = "CASE_INTAKE"
    case evidenceGathering  = "EVIDENCE_GATHERING"
    case draftPreparation   = "DRAFT_PREPARATION"
    case clientReview       = "CLIENT_REVIEW"
    case filing             = "FILING"
    case caseClosed         = "CASE_CLOSED"

    static func ordered(from raw: String) -> Int {
        WorkflowState.allCases.firstIndex(where: { $0.rawValue == raw }) ?? 0
    }

    var pretty: String {
        rawValue.split(separator: "_")
            .map { $0.prefix(1).uppercased() + $0.dropFirst().lowercased() }
            .joined(separator: " ")
    }

    var accent: AFAccent {
        switch self {
        case .clientCapture, .initialContact: return .blue
        case .caseEvaluation, .feeCollection, .caseIntake: return .blue
        case .evidenceGathering, .draftPreparation: return .purple
        case .clientReview: return .amber
        case .filing: return .green
        case .caseClosed: return .gray
        }
    }
}

enum AFAccent {
    case neutral, blue, purple, amber, green, gray
}

enum WorkflowGuidance {
    static func headline(for state: String) -> String {
        switch state {
        case "CLIENT_CAPTURE":     return "Capture client details"
        case "INITIAL_CONTACT":    return "Make initial contact"
        case "CASE_EVALUATION":    return "Evaluate the case"
        case "FEE_COLLECTION":     return "Collect retainer"
        case "CASE_INTAKE":        return "Intake materials"
        case "EVIDENCE_GATHERING": return "Gather evidence"
        case "DRAFT_PREPARATION":  return "Prepare drafts"
        case "CLIENT_REVIEW":      return "Awaiting client review"
        case "FILING":             return "Ready to file"
        case "CASE_CLOSED":        return "Case closed"
        default:                   return state
        }
    }

    static func detail(for state: String) -> String {
        switch state {
        case "CLIENT_CAPTURE":     return "Confirm client identity, contact, and matter type."
        case "INITIAL_CONTACT":    return "Reach out to the client and log the conversation."
        case "CASE_EVALUATION":    return "Review the materials and decide whether to take the case."
        case "FEE_COLLECTION":     return "Send the engagement letter and collect the retainer."
        case "CASE_INTAKE":        return "Run quick intake on the document folder."
        case "EVIDENCE_GATHERING": return "Upload supporting documents and tag evidence."
        case "DRAFT_PREPARATION":  return "Generate drafts and refine sections."
        case "CLIENT_REVIEW":      return "Share the draft with the client and capture feedback."
        case "FILING":             return "Export the packet and file with the court."
        case "CASE_CLOSED":        return "All actions complete. Archive when ready."
        default:                   return "Next step depends on the workflow."
        }
    }
}

// MARK: - Document metadata

/// Per-file metadata returned by `/v1/cases/{id}/documents/list`.
struct DocumentInfo: Codable, Identifiable, Hashable {
    var id: String { filename }
    let filename: String
    let doctype: String?       // backend slug; nil/empty when unknown
    let ocr_indexed: Bool?
    let rag_indexed: Bool?
    let size_bytes: Int64?
    let modified_at: Date?
}
