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
        NavigationStack {
            Form {
                Section {
                    HStack(spacing: AF.Space.s) {
                        Group {
                            if showKey {
                                TextField("sk-…", text: $apiKey)
                            } else {
                                SecureField("sk-…", text: $apiKey)
                            }
                        }
                        .textFieldStyle(.roundedBorder)

                        Button {
                            showKey.toggle()
                        } label: {
                            Image(systemName: showKey ? "eye.slash" : "eye")
                        }
                        .buttonStyle(.borderless)
                        .help(showKey ? "Hide key" : "Show key")
                    }
                    .labelsHidden()
                } header: {
                    Text("DashScope API key")
                } footer: {
                    Text("Stored locally in ~/Library/Application Support/AgentFlow/config.env (owner-only).")
                }

                Section {
                    TextField("qwen-max", text: $model)
                        .textFieldStyle(.roundedBorder)
                        .labelsHidden()
                } header: {
                    Text("Default model")
                } footer: {
                    Text("The model used for new runs. Can be overridden per case from the AI inspector.")
                }

                if let m = message {
                    Section {
                        Text(m).font(.callout).foregroundStyle(.secondary)
                    }
                }
            }
            .formStyle(.grouped)
            .navigationTitle("Settings")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                        .disabled(busy)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button {
                        save()
                    } label: {
                        if busy {
                            HStack(spacing: AF.Space.xs) {
                                ProgressView().controlSize(.small)
                                Text("Restarting…")
                            }
                        } else {
                            Text("Save")
                        }
                    }
                    .keyboardShortcut(.defaultAction)
                    .disabled(busy || apiKey.trimmingCharacters(in: .whitespaces).isEmpty)
                }
            }
        }
        .frame(minWidth: 520, minHeight: 360)
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
