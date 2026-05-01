import SwiftUI

struct ModelPickerSheet: View {
    @EnvironmentObject var api: APIClient
    @EnvironmentObject var ai: AIController
    @Environment(\.dismiss) private var dismiss

    @State private var search: String = ""
    @State private var hoverID: String?

    private var grouped: [(backend: String, models: [APIClient.LLMModel])] {
        let q = search.trimmingCharacters(in: .whitespaces).lowercased()
        let filtered = q.isEmpty ? ai.models : ai.models.filter {
            $0.name.lowercased().contains(q)
            || ($0.description ?? "").lowercased().contains(q)
            || ($0.backend ?? "").lowercased().contains(q)
            || $0.id.lowercased().contains(q)
        }
        let dict = Dictionary(grouping: filtered) { $0.backend?.uppercased() ?? "OTHER" }
        return dict.keys.sorted().map { k in (k, dict[k] ?? []) }
    }

    var body: some View {
        NavigationStack {
            Group {
                if ai.models.isEmpty {
                    ContentUnavailableView {
                        Label("No models available", systemImage: "cpu")
                    } description: {
                        Text("The engine hasn't reported any models yet. Make sure the backend is running.")
                    }
                } else {
                    List {
                        ForEach(grouped, id: \.backend) { group in
                            Section(providerLabel(group.backend)) {
                                ForEach(group.models) { m in
                                    ModelRow(
                                        model: m,
                                        selected: m.id == ai.selectedModelID
                                    )
                                    .contentShape(Rectangle())
                                    .onTapGesture {
                                        ai.selectedModelID = m.id
                                        dismiss()
                                    }
                                }
                            }
                        }
                    }
                    .listStyle(.inset)
                    .searchable(text: $search, prompt: "Search models, providers, or capabilities")
                }
            }
            .navigationTitle("Choose a model")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Done") { dismiss() }
                        .keyboardShortcut(.defaultAction)
                }
            }
        }
        .frame(minWidth: 560, idealWidth: 620, minHeight: 480, idealHeight: 560)
        .task { await ai.loadModelsIfNeeded(api: api) }
    }

    private func providerLabel(_ raw: String) -> String {
        switch raw {
        case "ANTHROPIC": return "Anthropic"
        case "OPENAI":    return "OpenAI"
        case "GOOGLE":    return "Google"
        case "OLLAMA":    return "Ollama · Local"
        case "LOCAL":     return "Local"
        case "OTHER":     return "Other"
        default:          return raw.capitalized
        }
    }
}

private struct ModelRow: View {
    let model: APIClient.LLMModel
    let selected: Bool

    var body: some View {
        HStack(alignment: .top, spacing: AF.Space.m) {
            ZStack {
                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                    .fill(providerTint(model.backend).opacity(0.18))
                    .frame(width: 36, height: 36)
                Image(systemName: providerIcon(model.backend))
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundStyle(providerTint(model.backend))
            }

            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: AF.Space.xs) {
                    Text(model.name)
                        .font(.body.weight(.semibold))
                    if model.is_default == true {
                        Text("Default")
                            .font(.caption2.weight(.semibold))
                            .padding(.horizontal, 6).padding(.vertical, 2)
                            .background(Capsule().fill(AF.Palette.tint(.blue).opacity(0.18)))
                            .foregroundStyle(AF.Palette.tint(.blue))
                    }
                }
                if let d = model.description, !d.isEmpty {
                    Text(d)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .lineLimit(3)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Text(model.id)
                    .font(.caption.monospaced())
                    .foregroundStyle(.tertiary)
                    .textSelection(.enabled)
            }

            Spacer(minLength: 0)

            Image(systemName: selected ? "checkmark.circle.fill" : "circle")
                .font(.title3)
                .foregroundStyle(selected ? AF.Palette.tint(.blue) : Color.secondary.opacity(0.4))
                .padding(.top, 4)
        }
        .padding(.vertical, 4)
    }

    private func providerIcon(_ backend: String?) -> String {
        switch (backend ?? "").lowercased() {
        case "anthropic": return "a.circle.fill"
        case "openai":    return "sparkle"
        case "google":    return "g.circle.fill"
        case "ollama", "local": return "cpu"
        default: return "brain"
        }
    }

    private func providerTint(_ backend: String?) -> Color {
        switch (backend ?? "").lowercased() {
        case "anthropic": return AF.Palette.tint(.purple)
        case "openai":    return AF.Palette.tint(.green)
        case "google":    return AF.Palette.tint(.blue)
        case "ollama", "local": return AF.Palette.tint(.amber)
        default: return AF.Palette.tint(.blue)
        }
    }
}
