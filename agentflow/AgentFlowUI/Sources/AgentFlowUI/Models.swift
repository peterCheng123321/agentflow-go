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
