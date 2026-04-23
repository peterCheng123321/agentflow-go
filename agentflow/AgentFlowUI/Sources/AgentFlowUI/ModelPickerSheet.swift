import SwiftUI

// Full-sheet model picker — used when invoked from Settings.
// The AI inspector uses an inline toolbar `Menu` instead.
struct ModelPickerSheet: View {
    @EnvironmentObject var api: APIClient
    @EnvironmentObject var ai: AIController
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Picker("Model", selection: $ai.selectedModelID) {
                        ForEach(ai.models) { m in
                            modelRow(m).tag(m.id)
                        }
                    }
                    .pickerStyle(.inline)
                    .labelsHidden()
                } footer: {
                    if ai.models.isEmpty {
                        Text("Loading available models…")
                    } else {
                        Text("Used for all new AI runs until changed.")
                    }
                }
            }
            .formStyle(.grouped)
            .navigationTitle("Select model")
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                        .keyboardShortcut(.defaultAction)
                }
            }
        }
        .frame(minWidth: 520, minHeight: 420)
        .task { await ai.loadModelsIfNeeded(api: api) }
    }

    @ViewBuilder
    private func modelRow(_ m: APIClient.LLMModel) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: AF.Space.xs) {
                Text(m.name).font(.callout.weight(.semibold))
                if let b = m.backend, !b.isEmpty {
                    Text(b)
                        .font(.caption2.weight(.semibold))
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 1)
                        .background(
                            Capsule().fill(Color.secondary.opacity(0.15))
                        )
                }
            }
            if let d = m.description, !d.isEmpty {
                Text(d).font(.caption).foregroundStyle(.secondary).lineLimit(3)
            }
        }
    }
}
