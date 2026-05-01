import Foundation
import AppKit

@MainActor
final class BackendManager: ObservableObject {
    @Published var status: Status = .starting
    @Published var lastError: String?

    enum Status { case starting, running, failed }

    let port: Int = 8080
    private var process: Process?

    static let configURL: URL = {
        let support = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("AgentFlow", isDirectory: true)
        try? FileManager.default.createDirectory(at: support, withIntermediateDirectories: true)
        return support.appendingPathComponent("config.env")
    }()

    func start() {
        Task {
            if await ping() {
                self.status = .running
                return
            }
            launch()
        }
    }

    private func launch() {
        let home = NSHomeDirectory()
        let candidates: [String] = [
            Bundle.main.bundlePath + "/Contents/MacOS/agentflow-serve",
            ProcessInfo.processInfo.environment["AGENTFLOW_SERVE_PATH"] ?? "",
            home + "/Downloads/agentflow/agentflow-go/agentflow-serve",
            home + "/Downloads/agentflow/agentflow-serve",
            "/tmp/agentflow-serve",
            "/usr/local/bin/agentflow-serve"
        ]
        guard let binary = candidates.first(where: { !$0.isEmpty && FileManager.default.isExecutableFile(atPath: $0) }) else {
            self.status = .failed
            self.lastError = "agentflow-serve binary not found"
            return
        }

        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: binary)
        let dataDir = home + "/Library/Application Support/AgentFlow"
        try? FileManager.default.createDirectory(atPath: dataDir, withIntermediateDirectories: true)

        var env = ProcessInfo.processInfo.environment
        env["AGENTFLOW_PORT"] = "\(port)"
        env["AGENTFLOW_DATA_DIR"] = dataDir

        // Layered config: config.env > process env > placeholder
        for (k, v) in Self.readConfigEnv() {
            env[k] = v
        }
        if env["AGENTFLOW_DASHSCOPE_API_KEY"] == nil || env["AGENTFLOW_DASHSCOPE_API_KEY"]!.isEmpty {
            env["AGENTFLOW_DASHSCOPE_API_KEY"] = "sk-dev-placeholder"
        }
        proc.environment = env

        let logPath = dataDir + "/agentflow-serve.log"
        FileManager.default.createFile(atPath: logPath, contents: nil)
        if let handle = FileHandle(forWritingAtPath: logPath) {
            proc.standardOutput = handle
            proc.standardError = handle
        }

        do {
            try proc.run()
            self.process = proc
        } catch {
            self.status = .failed
            self.lastError = "Failed to start backend: \(error.localizedDescription)"
            return
        }

        Task {
            for _ in 0..<40 {
                try? await Task.sleep(nanoseconds: 250_000_000)
                if await self.ping() {
                    self.status = .running
                    return
                }
            }
            self.status = .failed
            self.lastError = "Backend did not respond on port \(self.port) within 10s"
        }
    }

    func restart() {
        process?.terminate()
        process = nil
        status = .starting
        // Let kernel release the port
        Task {
            try? await Task.sleep(nanoseconds: 700_000_000)
            launch()
        }
    }

    func stop() {
        process?.terminate()
        process = nil
    }

    private func ping() async -> Bool {
        guard let url = URL(string: "http://127.0.0.1:\(port)/health") else { return false }
        var req = URLRequest(url: url)
        req.timeoutInterval = 0.8
        do {
            let (_, resp) = try await URLSession.shared.data(for: req)
            return ((resp as? HTTPURLResponse)?.statusCode ?? 0) == 200
        } catch { return false }
    }

    // MARK: - config.env helpers

    static func readConfigEnv() -> [String: String] {
        guard let raw = try? String(contentsOf: configURL, encoding: .utf8) else { return [:] }
        var out: [String: String] = [:]
        for line in raw.split(whereSeparator: \.isNewline) {
            let t = line.trimmingCharacters(in: .whitespaces)
            guard !t.isEmpty, !t.hasPrefix("#"), let eq = t.firstIndex(of: "=") else { continue }
            let k = String(t[..<eq]).trimmingCharacters(in: .whitespaces)
            var v = String(t[t.index(after: eq)...]).trimmingCharacters(in: .whitespaces)
            if v.hasPrefix("\"") && v.hasSuffix("\"") { v = String(v.dropFirst().dropLast()) }
            if !k.isEmpty { out[k] = v }
        }
        return out
    }

    static func writeConfigEnv(_ kv: [String: String]) throws {
        let lines = kv.sorted(by: { $0.key < $1.key })
            .map { "\($0.key)=\($0.value)" }
        let data = (lines.joined(separator: "\n") + "\n").data(using: .utf8)!
        try data.write(to: configURL, options: .atomic)
        // Owner read/write only
        try? FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: configURL.path)
    }
}
