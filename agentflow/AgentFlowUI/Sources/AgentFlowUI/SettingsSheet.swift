import SwiftUI

struct SettingsSheet: View {
    @EnvironmentObject var backend: BackendManager
    @Environment(\.dismiss) private var dismiss

    @State private var apiKey: String = ""
    @State private var showKey: Bool = false
    @State private var model: String = "qwen-max"
    @State private var busy = false
    @State private var message: String?

    var body: some View {
        VStack(alignment: .leading, spacing: AF.Space.m) {
            HStack {
                Text("Settings").font(.title2.weight(.semibold))
                Spacer()
                Button { dismiss() } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.title3).foregroundStyle(.secondary)
                }.buttonStyle(.plain)
            }

            SectionHeader(title: "AI Provider",
                          subtitle: "Your DashScope API key is stored locally in \n~/Library/Application Support/AgentFlow/config.env (owner-only)")

            VStack(alignment: .leading, spacing: 6) {
                Text("DASHSCOPE API KEY").font(.caption2).foregroundStyle(.secondary).tracking(0.6)
                HStack(spacing: 8) {
                    Group {
                        if showKey {
                            TextField("sk-…", text: $apiKey)
                        } else {
                            SecureField("sk-…", text: $apiKey)
                        }
                    }
                    .textFieldStyle(.plain)
                    .padding(10)
                    .background(
                        RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                            .fill(Color.black.opacity(0.22))
                    )
                    Button {
                        showKey.toggle()
                    } label: {
                        Image(systemName: showKey ? "eye.slash" : "eye")
                    }
                    .buttonStyle(.afGhost)
                }
            }

            VStack(alignment: .leading, spacing: 6) {
                Text("MODEL").font(.caption2).foregroundStyle(.secondary).tracking(0.6)
                TextField("qwen-max", text: $model)
                    .textFieldStyle(.plain)
                    .padding(10)
                    .background(
                        RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                            .fill(Color.black.opacity(0.22))
                    )
            }

            if let m = message {
                Text(m).font(.caption).foregroundStyle(.secondary)
            }

            HStack {
                Spacer()
                Button("Cancel") { dismiss() }
                    .buttonStyle(.afGhost)
                    .disabled(busy)
                Button {
                    save()
                } label: {
                    if busy {
                        HStack { ProgressView().controlSize(.small); Text("Restarting backend…") }
                    } else {
                        Text("Save & restart backend")
                    }
                }
                .buttonStyle(.afPrimary)
                .disabled(busy || apiKey.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .padding(AF.Space.l)
        .frame(width: 520)
        .background(
            RoundedRectangle(cornerRadius: AF.Radius.xl, style: .continuous)
                .fill(.ultraThinMaterial)
        )
        .overlay(
            RoundedRectangle(cornerRadius: AF.Radius.xl, style: .continuous)
                .strokeBorder(.white.opacity(0.1), lineWidth: 1)
        )
        .onAppear {
            let existing = BackendManager.readConfigEnv()
            apiKey = existing["AGENTFLOW_DASHSCOPE_API_KEY"] ?? ""
            model  = existing["AGENTFLOW_MODEL"] ?? "qwen-max"
        }
    }

    private func save() {
        busy = true
        let trimmed = apiKey.trimmingCharacters(in: .whitespaces)
        do {
            var kv = BackendManager.readConfigEnv()
            kv["AGENTFLOW_DASHSCOPE_API_KEY"] = trimmed
            kv["AGENTFLOW_MODEL"] = model.trimmingCharacters(in: .whitespaces)
            try BackendManager.writeConfigEnv(kv)
            backend.restart()
            message = "Saved — backend restarting…"
            Task {
                try? await Task.sleep(nanoseconds: 1_500_000_000)
                busy = false
                dismiss()
            }
        } catch {
            message = "Save failed: \(error.localizedDescription)"
            busy = false
        }
    }
}
