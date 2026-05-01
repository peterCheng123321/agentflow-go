import Foundation
import Combine

@MainActor
final class APIClient: ObservableObject {
    let port: Int
    private let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .custom { dec in
            let s = try dec.singleValueContainer().decode(String.self)
            let iso = ISO8601DateFormatter()
            iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let d = iso.date(from: s) { return d }
            iso.formatOptions = [.withInternetDateTime]
            if let d = iso.date(from: s) { return d }
            if let idx = s.firstIndex(of: "."),
               let end = s.firstIndex(where: { "+-Z".contains($0) }) {
                var trimmed = s
                trimmed.removeSubrange(idx..<end)
                if let d = iso.date(from: trimmed) { return d }
            }
            throw DecodingError.dataCorruptedError(in: try dec.singleValueContainer(),
                debugDescription: "Unparseable date \(s)")
        }
        return d
    }()

    init(port: Int = 8080) {
        self.port = port
    }

    var base: URL { URL(string: "http://127.0.0.1:\(port)")! }

    // MARK: - Generic

    func get<T: Decodable>(_ path: String, as: T.Type = T.self) async throws -> T {
        let url = base.appendingPathComponent(path)
        let (data, resp) = try await URLSession.shared.data(from: url)
        try check(resp, data: data)
        return try decoder.decode(T.self, from: data)
    }

    func getData(_ path: String) async throws -> Data {
        let url = base.appendingPathComponent(path)
        let (data, resp) = try await URLSession.shared.data(from: url)
        try check(resp, data: data)
        return data
    }

    @discardableResult
    func post<T: Decodable>(_ path: String, body: [String: Any] = [:], as: T.Type = T.self) async throws -> T {
        var req = URLRequest(url: base.appendingPathComponent(path))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        let (data, resp) = try await URLSession.shared.data(for: req)
        try check(resp, data: data)
        if T.self == EmptyResponse.self { return EmptyResponse() as! T }
        return try decoder.decode(T.self, from: data)
    }

    func delete(_ path: String) async throws {
        var req = URLRequest(url: base.appendingPathComponent(path))
        req.httpMethod = "DELETE"
        let (data, resp) = try await URLSession.shared.data(for: req)
        try check(resp, data: data)
    }

    private func check(_ resp: URLResponse, data: Data) throws {
        guard let http = resp as? HTTPURLResponse else { return }
        if !(200..<300).contains(http.statusCode) {
            let msg = String(data: data, encoding: .utf8) ?? "HTTP \(http.statusCode)"
            throw APIError.http(status: http.statusCode, message: msg)
        }
    }

    // MARK: - Health / status

    func ping() async -> Bool {
        do {
            var req = URLRequest(url: base.appendingPathComponent("health"))
            req.timeoutInterval = 1.5
            let (_, resp) = try await URLSession.shared.data(for: req)
            return ((resp as? HTTPURLResponse)?.statusCode ?? 0) == 200
        } catch { return false }
    }

    // MARK: - Cases

    func listCases() async throws -> [Case] {
        let resp: CasesResponse = try await get("v1/cases")
        return resp.cases
    }

    func getCase(_ id: String) async throws -> Case {
        try await get("v1/cases/\(id)")
    }

    func createCase(clientName: String, matterType: String, initialMsg: String) async throws {
        _ = try await post("v1/cases/create", body: [
            "client_name": clientName,
            "matter_type": matterType,
            "source_channel": "AgentFlow UI",
            "initial_msg": initialMsg
        ], as: EmptyResponse.self)
    }

    func advance(_ id: String) async throws {
        _ = try await post("v1/cases/\(id)/advance", as: EmptyResponse.self)
    }

    func approveHITL(_ id: String, state: String, approved: Bool, reason: String = "") async throws {
        _ = try await post("v1/cases/\(id)/approve", body: [
            "state": state, "approved": approved, "reason": reason
        ], as: EmptyResponse.self)
    }

    func deleteCase(_ id: String) async throws {
        _ = try await post("v1/cases/\(id)/delete", as: EmptyResponse.self)
    }

    func addNote(_ id: String, text: String) async throws {
        _ = try await post("v1/cases/\(id)/notes", body: ["text": text], as: EmptyResponse.self)
    }

    // MARK: - Files

    /// Upload a file, optionally associated with a case.
    func uploadFile(url fileURL: URL, caseID: String? = nil, progress: @escaping (Double) -> Void = { _ in })
        async throws -> UploadResponse
    {
        let boundary = "AgentFlowBoundary-\(UUID().uuidString)"
        var req = URLRequest(url: base.appendingPathComponent("v1/upload"))
        req.httpMethod = "POST"
        req.setValue("multipart/form-data; boundary=\(boundary)", forHTTPHeaderField: "Content-Type")

        let (body, mimeType) = try multipartBody(boundary: boundary, fileURL: fileURL, caseID: caseID)
        req.httpBody = body
        req.setValue("\(body.count)", forHTTPHeaderField: "Content-Length")
        _ = mimeType // retained for potential use

        progress(0.2)
        let (data, resp) = try await URLSession.shared.data(for: req)
        progress(1.0)
        try check(resp, data: data)
        return try decoder.decode(UploadResponse.self, from: data)
    }

    private func multipartBody(boundary: String, fileURL: URL, caseID: String?) throws -> (Data, String) {
        var body = Data()
        let cr = "\r\n"
        let filename = fileURL.lastPathComponent
        let mime = mimeFor(url: fileURL)

        if let cid = caseID, !cid.isEmpty {
            body.append("--\(boundary)\(cr)".data(using: .utf8)!)
            body.append("Content-Disposition: form-data; name=\"case_id\"\(cr)\(cr)\(cid)\(cr)".data(using: .utf8)!)
        }

        body.append("--\(boundary)\(cr)".data(using: .utf8)!)
        body.append("Content-Disposition: form-data; name=\"file\"; filename=\"\(filename)\"\(cr)".data(using: .utf8)!)
        body.append("Content-Type: \(mime)\(cr)\(cr)".data(using: .utf8)!)
        body.append(try Data(contentsOf: fileURL))
        body.append(cr.data(using: .utf8)!)
        body.append("--\(boundary)--\(cr)".data(using: .utf8)!)
        return (body, mime)
    }

    private func mimeFor(url: URL) -> String {
        switch url.pathExtension.lowercased() {
        case "pdf":  return "application/pdf"
        case "txt":  return "text/plain"
        case "md":   return "text/markdown"
        case "json": return "application/json"
        case "png":  return "image/png"
        case "jpg", "jpeg": return "image/jpeg"
        case "heic": return "image/heic"
        case "gif":  return "image/gif"
        case "docx": return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
        case "doc":  return "application/msword"
        case "zip":  return "application/zip"
        default:     return "application/octet-stream"
        }
    }

    /// Remove a document from a case. Uses the POST shim because DELETE is also supported.
    func removeDocument(caseID: String, filename: String) async throws {
        let enc = filename.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? filename
        try await delete("v1/cases/\(caseID)/documents/\(enc)")
    }

    /// Returns the raw bytes for a document (binary safe).
    func documentBytes(filename: String) async throws -> Data {
        let enc = filename.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? filename
        return try await getData("v1/documents/\(enc)/view")
    }

    /// Build a file-like URL for the view endpoint.
    func documentViewURL(filename: String) -> URL {
        let enc = filename.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? filename
        return base.appendingPathComponent("v1/documents/\(enc)/view")
    }

    /// GET /v1/cases/{id}/documents/list — per-file metadata for the matter.
    func listDocumentInfo(caseID: String) async throws -> [DocumentInfo] {
        struct Resp: Decodable { let documents: [DocumentInfo]? }
        var req = URLRequest(url: base.appendingPathComponent("v1/cases/\(caseID)/documents/list"))
        req.timeoutInterval = 30
        let (data, resp) = try await URLSession.shared.data(for: req)
        try check(resp, data: data)
        // Tolerate either a bare array or {"documents": [...]} envelope.
        if let arr = try? decoder.decode([DocumentInfo].self, from: data) { return arr }
        return (try decoder.decode(Resp.self, from: data)).documents ?? []
    }

    /// GET /v1/documents/{name}/thumbnail?size=128 — returns PNG bytes.
    /// Caller wraps in NSImage / SwiftUI Image as needed.
    func documentThumbnail(filename: String, size: Int = 128) async throws -> Data {
        var req = URLRequest(url: documentThumbnailURL(filename: filename, size: size))
        req.timeoutInterval = 30
        let (data, resp) = try await URLSession.shared.data(for: req)
        try check(resp, data: data)
        return data
    }

    /// Convenience URL builder for AsyncImage callers.
    func documentThumbnailURL(filename: String, size: Int = 128) -> URL {
        let enc = filename.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? filename
        var comps = URLComponents(url: base.appendingPathComponent("v1/documents/\(enc)/thumbnail"),
                                  resolvingAgainstBaseURL: false)!
        comps.queryItems = [URLQueryItem(name: "size", value: String(size))]
        return comps.url!
    }

    // MARK: - Jobs

    struct JobStatus: Decodable {
        let id: String
        let type: String?
        let status: String
        let progress: Int?
        let result: CodableValue?
        let error: String?
    }

    func job(_ id: String) async throws -> JobStatus {
        try await get("v1/jobs/\(id)")
    }

    /// Poll until job is not processing/queued (max ~60s).
    func waitForJob(_ id: String) async throws -> JobStatus {
        for _ in 0..<120 {
            let s = try await job(id)
            if s.status != "processing" && s.status != "queued" {
                return s
            }
            try await Task.sleep(nanoseconds: 500_000_000)
        }
        throw APIError.http(status: 504, message: "Job \(id) did not finish in time")
    }

    // MARK: - RAG

    struct SearchHit: Decodable, Identifiable {
        let filename: String
        let chunk: String
        let score: Double
        let match_mode: String?
        var id: String { filename + String(score) }
    }

    // MARK: - LLM / Chat

    struct LLMModel: Decodable, Identifiable, Hashable {
        let id: String
        let name: String
        let backend: String?
        let description: String?
        let is_default: Bool?
    }

    struct ModelsResponse: Decodable {
        let models: [LLMModel]
        let backend: String?
        let current: String?
    }

    func listModels() async throws -> ModelsResponse {
        // Backend exposes /api/models (not under /v1).
        let url = base.appendingPathComponent("api/models")
        let (data, resp) = try await URLSession.shared.data(from: url)
        try check(resp, data: data)
        return try decoder.decode(ModelsResponse.self, from: data)
    }

    struct ChatMessage: Codable, Identifiable, Hashable {
        var id = UUID()
        let role: String     // "user" | "assistant" | "system"
        var content: String
        init(role: String, content: String) {
            self.role = role
            self.content = content
        }
        enum CodingKeys: String, CodingKey { case role, content }
    }

    struct ChatResponse: Decodable {
        let reply: String
        let sources: [String]?
        let model: String?
    }

    /// Send chat turn to /v1/chat. Returns assistant reply with optional RAG sources.
    func chat(messages: [ChatMessage], caseID: String? = nil, useRAG: Bool = true, model: String? = nil)
        async throws -> ChatResponse
    {
        var body: [String: Any] = [
            "messages": messages.map { ["role": $0.role, "content": $0.content] },
            "use_rag": useRAG
        ]
        if let cid = caseID, !cid.isEmpty { body["case_id"] = cid }
        if let m = model, !m.isEmpty { body["model"] = m }

        var req = URLRequest(url: base.appendingPathComponent("v1/chat"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        req.timeoutInterval = 120
        let (data, resp) = try await URLSession.shared.data(for: req)
        try check(resp, data: data)
        return try decoder.decode(ChatResponse.self, from: data)
    }

    func ragSearch(query: String, k: Int = 5) async throws -> [SearchHit] {
        struct Req: Codable { let query: String; let k: Int }
        struct Resp: Decodable { let results: [SearchHit] }
        var req = URLRequest(url: base.appendingPathComponent("v1/rag/search"))
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONEncoder().encode(Req(query: query, k: k))
        let (data, resp) = try await URLSession.shared.data(for: req)
        try check(resp, data: data)
        return try decoder.decode(Resp.self, from: data).results
    }
}

struct EmptyResponse: Decodable { }

struct UploadResponse: Decodable {
    let filename: String?
    let job_id: String?
    let status: String?
}

enum APIError: LocalizedError {
    case http(status: Int, message: String)
    var errorDescription: String? {
        switch self {
        case .http(let s, let m): return "HTTP \(s): \(m)"
        }
    }
}

/// Permissive JSON value for dynamic server payloads (e.g. job result maps).
struct CodableValue: Decodable {
    let value: Any

    init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if let b = try? container.decode(Bool.self)   { value = b; return }
        if let i = try? container.decode(Int.self)    { value = i; return }
        if let d = try? container.decode(Double.self) { value = d; return }
        if let s = try? container.decode(String.self) { value = s; return }
        if let a = try? container.decode([CodableValue].self) {
            value = a.map { $0.value }; return
        }
        if let o = try? container.decode([String: CodableValue].self) {
            value = o.mapValues { $0.value }; return
        }
        value = NSNull()
    }
}
