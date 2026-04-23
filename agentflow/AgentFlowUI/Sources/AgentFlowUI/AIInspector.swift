import SwiftUI

/// Right-pane AI inspector. Always-on chat surface with model picker, quick
/// actions, and citation chips that hand off to the root DocumentViewer sheet.
struct AIInspector: View {
    @EnvironmentObject var api: APIClient
    @EnvironmentObject var ai: AIController
    @EnvironmentObject var router: AppRouter
    let caseID: String?
    @State private var input: String = ""
    @FocusState private var focused: Bool

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider().opacity(0.2)
            transcript
            composer
        }
        .background(
            Rectangle()
                .fill(.ultraThinMaterial)
                .overlay(
                    LinearGradient(colors: [.black.opacity(0.25), .black.opacity(0.05)],
                                   startPoint: .top, endPoint: .bottom)
                )
        )
        .overlay(alignment: .leading) {
            Rectangle().fill(Color.white.opacity(0.06)).frame(width: 1)
        }
        .task { await ai.loadModelsIfNeeded(api: api) }
        .onChange(of: caseID) { _, new in ai.bind(toCase: new) }
    }

    // MARK: - Header

    @ViewBuilder private var header: some View {
        VStack(alignment: .leading, spacing: AF.Space.s) {
            HStack(spacing: 8) {
                Image(systemName: "sparkles")
                    .foregroundStyle(LinearGradient(
                        colors: [Color(red: 0.55, green: 0.6, blue: 1.0),
                                 Color(red: 0.85, green: 0.45, blue: 1.0)],
                        startPoint: .topLeading, endPoint: .bottomTrailing
                    ))
                Text("Ask AI").font(.headline)
                Spacer()
                Button { router.toggleInspector() } label: {
                    Image(systemName: "sidebar.right").foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
                .help("Hide inspector (⌘⌥I)")
            }

            modelPickerRow

            HStack(spacing: 6) {
                contextChip(
                    icon: caseID == nil ? "globe" : "folder.fill",
                    text: caseID.map { "Case " + String($0.suffix(8)) } ?? "Global"
                )
                Toggle(isOn: $ai.useRAG) {
                    Label("RAG", systemImage: "magnifyingglass.circle")
                        .labelStyle(.titleAndIcon)
                        .font(.caption)
                }
                .toggleStyle(.switch)
                .controlSize(.mini)
                Spacer()
            }
        }
        .padding(AF.Space.m)
    }

    @ViewBuilder private var modelPickerRow: some View {
        Menu {
            ForEach(ai.models) { m in
                Button {
                    ai.selectedModelID = m.id
                } label: {
                    HStack {
                        Text(m.name)
                        if m.id == ai.selectedModelID {
                            Spacer()
                            Image(systemName: "checkmark")
                        }
                    }
                }
            }
            if ai.models.isEmpty {
                Text("No models").foregroundStyle(.secondary)
            }
        } label: {
            HStack(spacing: 8) {
                Image(systemName: "cpu")
                Text(ai.selectedModel?.name ?? "Choose model")
                    .lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Image(systemName: "chevron.down").font(.caption).foregroundStyle(.secondary)
            }
            .padding(.horizontal, 10).padding(.vertical, 7)
            .background(
                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                    .fill(Color.black.opacity(0.25))
            )
            .overlay(
                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                    .strokeBorder(.white.opacity(0.08), lineWidth: 1)
            )
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
    }

    @ViewBuilder private func contextChip(icon: String, text: String) -> some View {
        HStack(spacing: 5) {
            Image(systemName: icon).font(.caption2)
            Text(text).font(.caption.weight(.medium))
        }
        .padding(.horizontal, 8).padding(.vertical, 4)
        .background(Capsule().fill(Color.white.opacity(0.06)))
        .overlay(Capsule().strokeBorder(.white.opacity(0.08), lineWidth: 1))
        .foregroundStyle(.secondary)
    }

    // MARK: - Transcript

    @ViewBuilder private var transcript: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: AF.Space.m) {
                    if ai.messages.isEmpty {
                        emptyState
                    } else {
                        ForEach(ai.messages) { m in messageBubble(m).id(m.id) }
                        if !ai.lastSources.isEmpty {
                            citationStrip(ai.lastSources)
                        }
                        if ai.isLoading {
                            HStack(spacing: 8) {
                                ProgressView().controlSize(.small)
                                Text("Thinking…").font(.caption).foregroundStyle(.secondary)
                            }
                            .padding(.horizontal, AF.Space.m)
                        }
                        if let err = ai.lastError {
                            Text(err).font(.caption).foregroundStyle(.red)
                                .padding(.horizontal, AF.Space.m)
                        }
                    }
                }
                .padding(AF.Space.m)
            }
            .onChange(of: ai.messages.count) { _, _ in
                if let last = ai.messages.last {
                    withAnimation(.easeOut(duration: 0.2)) {
                        proxy.scrollTo(last.id, anchor: .bottom)
                    }
                }
            }
        }
    }

    @ViewBuilder private var emptyState: some View {
        VStack(alignment: .leading, spacing: AF.Space.m) {
            Text("Suggestions")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.tertiary)
                .textCase(.uppercase)

            ForEach(AIController.QuickAction.allCases) { a in
                Button {
                    ai.run(a, api: api)
                } label: {
                    HStack(spacing: 10) {
                        Image(systemName: a.icon)
                            .frame(width: 22)
                            .foregroundStyle(AF.Palette.tint(.purple))
                        Text(a.title)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        Image(systemName: "arrow.up.right.circle")
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                    }
                    .padding(.horizontal, 10).padding(.vertical, 9)
                    .background(
                        RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                            .fill(Color.white.opacity(0.04))
                    )
                    .overlay(
                        RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                            .strokeBorder(.white.opacity(0.06), lineWidth: 1)
                    )
                }
                .buttonStyle(.plain)
                .disabled(ai.isLoading)
            }
        }
    }

    @ViewBuilder private func messageBubble(_ m: APIClient.ChatMessage) -> some View {
        let isUser = m.role == "user"
        HStack {
            if isUser { Spacer(minLength: 24) }
            VStack(alignment: .leading, spacing: 4) {
                Text(m.role.capitalized)
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.tertiary)
                    .textCase(.uppercase)
                Text(m.content)
                    .font(.callout)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .padding(10)
            .background(
                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                    .fill(isUser ? AF.Palette.tint(.blue).opacity(0.18) : Color.white.opacity(0.05))
            )
            .overlay(
                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                    .strokeBorder(.white.opacity(0.06), lineWidth: 1)
            )
            if !isUser { Spacer(minLength: 24) }
        }
    }

    @ViewBuilder private func citationStrip(_ sources: [String]) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Sources")
                .font(.caption2.weight(.semibold))
                .foregroundStyle(.tertiary)
                .textCase(.uppercase)
            FlowLayout(spacing: 6) {
                ForEach(Array(sources.enumerated()), id: \.offset) { _, name in
                    Button {
                        router.open(.document(filename: name, caseID: caseID))
                    } label: {
                        HStack(spacing: 4) {
                            Image(systemName: "doc.text")
                            Text(name).lineLimit(1)
                        }
                        .font(.caption)
                        .padding(.horizontal, 8).padding(.vertical, 4)
                        .background(Capsule().fill(AF.Palette.tint(.blue).opacity(0.18)))
                        .overlay(Capsule().strokeBorder(AF.Palette.tint(.blue).opacity(0.4), lineWidth: 1))
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    // MARK: - Composer

    @ViewBuilder private var composer: some View {
        VStack(spacing: 6) {
            Divider().opacity(0.2)
            HStack(alignment: .bottom, spacing: 8) {
                TextField("Ask about this case…", text: $input, axis: .vertical)
                    .textFieldStyle(.plain)
                    .lineLimit(1...5)
                    .focused($focused)
                    .padding(10)
                    .background(
                        RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                            .fill(Color.black.opacity(0.25))
                    )
                    .overlay(
                        RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                            .strokeBorder(.white.opacity(0.08), lineWidth: 1)
                    )
                    .onSubmit { submit() }
                Button {
                    submit()
                } label: {
                    Image(systemName: "paperplane.fill")
                        .font(.system(size: 14, weight: .semibold))
                        .padding(10)
                }
                .buttonStyle(.afPrimary)
                .keyboardShortcut(.return, modifiers: [.command])
                .disabled(input.trimmingCharacters(in: .whitespaces).isEmpty || ai.isLoading)
            }
            .padding(.horizontal, AF.Space.m)
            .padding(.bottom, AF.Space.m)
            .padding(.top, 4)
        }
    }

    private func submit() {
        let t = input.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !t.isEmpty else { return }
        ai.send(t, api: api)
        input = ""
    }
}

// MARK: - FlowLayout (chip wrapping)

struct FlowLayout: Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let maxW = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, lineH: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x + s.width > maxW {
                x = 0; y += lineH + spacing; lineH = 0
            }
            x += s.width + spacing
            lineH = max(lineH, s.height)
        }
        return CGSize(width: maxW.isFinite ? maxW : x, height: y + lineH)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x: CGFloat = bounds.minX, y: CGFloat = bounds.minY, lineH: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x + s.width > bounds.maxX {
                x = bounds.minX; y += lineH + spacing; lineH = 0
            }
            v.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(s))
            x += s.width + spacing
            lineH = max(lineH, s.height)
        }
    }
}
