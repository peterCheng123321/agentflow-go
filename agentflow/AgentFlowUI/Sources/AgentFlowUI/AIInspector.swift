import SwiftUI

struct AIInspector: View {
    @EnvironmentObject var api: APIClient
    @EnvironmentObject var ai: AIController
    @EnvironmentObject var router: AppRouter
    let caseID: String?
    @State private var input: String = ""
    @FocusState private var focused: Bool

    private let aiGradient = LinearGradient(
        colors: [Color(red: 0.72, green: 0.55, blue: 1.0), Color(red: 0.38, green: 0.65, blue: 1.0)],
        startPoint: .topLeading, endPoint: .bottomTrailing)

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider().opacity(0.15)
            transcript
            composer
        }
        .background(
            Rectangle()
                .fill(.thinMaterial)
                .overlay(LinearGradient(
                    colors: [Color.white.opacity(0.05), Color.clear],
                    startPoint: .top, endPoint: .bottom))
        )
        .task { await ai.loadModelsIfNeeded(api: api) }
        .onChange(of: caseID) { _, new in ai.bind(toCase: new) }
    }

    // MARK: - Header

    @ViewBuilder private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            // Row 1: title + controls
            HStack(spacing: 10) {
                Image(systemName: "sparkles")
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(aiGradient)

                Text("Research desk")
                    .font(.callout.weight(.semibold))

                Spacer()

                // Docs search toggle
                Button { ai.useRAG.toggle() } label: {
                    Image(systemName: ai.useRAG ? "doc.text.fill" : "doc.text")
                        .font(.system(size: 13))
                        .foregroundStyle(ai.useRAG ? AF.Palette.tint(.blue) : Color.secondary)
                }
                .buttonStyle(.plain)
                .help(ai.useRAG ? "Document search on — click to disable" : "Document search off — click to enable")

                modelPicker
            }

            // Row 2: context + clear
            HStack(spacing: 6) {
                HStack(spacing: 5) {
                    Image(systemName: caseID == nil ? "globe" : "folder.fill")
                        .font(.caption2)
                    Text(caseID.map { "Case " + String($0.suffix(8)) } ?? "Global context")
                        .font(.caption.weight(.medium))
                }
                .padding(.horizontal, 8).padding(.vertical, 4)
                .background(Capsule().fill(Color.white.opacity(0.08)))
                .overlay(Capsule().strokeBorder(Color.white.opacity(0.12), lineWidth: 1))
                .foregroundStyle(.secondary)

                Spacer()

                if !ai.messages.isEmpty {
                    Button { ai.reset() } label: {
                        Text("Clear")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    .buttonStyle(.plain)
                }
            }
        }
        .padding(.horizontal, AF.Space.m)
        .padding(.top, AF.Space.m)
        .padding(.bottom, AF.Space.s)
    }

    @ViewBuilder private var modelPicker: some View {
        Menu {
            let grouped = Dictionary(grouping: ai.models) { ($0.backend ?? "other").lowercased() }
            ForEach(grouped.keys.sorted(), id: \.self) { provider in
                Section(providerLabel(provider)) {
                    ForEach(grouped[provider] ?? []) { m in
                        Button {
                            ai.selectedModelID = m.id
                        } label: {
                            if m.id == ai.selectedModelID {
                                Label(m.name, systemImage: "checkmark")
                            } else {
                                Text(m.name)
                            }
                        }
                    }
                }
            }
            if ai.models.isEmpty {
                Text("No models available").foregroundStyle(.secondary)
            }
            Divider()
            Button {
                router.open(.modelPicker)
            } label: {
                Label("Browse all models…", systemImage: "slider.horizontal.3")
            }
        } label: {
            HStack(spacing: 6) {
                Image(systemName: "cpu")
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(AF.Palette.tint(.blue))
                Text(ai.selectedModel?.name ?? "Choose model")
                    .font(.caption.weight(.medium))
                    .lineLimit(1)
                Image(systemName: "chevron.up.chevron.down")
                    .font(.caption2.weight(.semibold))
                    .foregroundStyle(.secondary)
            }
            .foregroundStyle(.primary)
            .padding(.horizontal, AF.Space.s)
            .padding(.vertical, 5)
            .background(Capsule().fill(AF.Palette.surface))
            .overlay(Capsule().strokeBorder(AF.Palette.separator, lineWidth: 1))
        }
        .menuStyle(.borderlessButton)
        .menuIndicator(.hidden)
        .help(ai.selectedModel?.description ?? "Select the model that powers research")
    }

    private func providerLabel(_ raw: String) -> String {
        switch raw {
        case "anthropic": return "Anthropic"
        case "openai":    return "OpenAI"
        case "google":    return "Google"
        case "ollama", "local": return "Local"
        default: return raw.capitalized
        }
    }

    // MARK: - Transcript

    @ViewBuilder private var transcript: some View {
        if ai.messages.isEmpty {
            emptyState
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollViewReader { proxy in
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: AF.Space.m) {
                        ForEach(ai.messages) { m in messageBubble(m).id(m.id) }
                        if !ai.lastSources.isEmpty { citationStrip(ai.lastSources) }
                        if ai.isLoading { thinkingBubble }
                        if let err = ai.lastError { errorBubble(err) }
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
    }

    // MARK: - Empty state

    @ViewBuilder private var emptyState: some View {
        VStack(spacing: AF.Space.l) {
            Spacer()

            ZStack {
                Circle()
                    .fill(LinearGradient(
                        colors: [AF.Palette.tint(.purple).opacity(0.30),
                                 AF.Palette.tint(.blue).opacity(0.18)],
                        startPoint: .topLeading, endPoint: .bottomTrailing))
                    .frame(width: 68, height: 68)
                    .blur(radius: 12)
                Image(systemName: "sparkles")
                    .font(.system(size: 30, weight: .medium))
                    .foregroundStyle(aiGradient)
            }

            VStack(spacing: 6) {
                Text("Ask me anything")
                    .font(.headline)
                Text(caseID != nil
                     ? "I have context on this case — its documents, notes, and workflow state."
                     : "Select a case to give me context, or ask a general question.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 260)
            }

            FlowLayout(spacing: 8) {
                ForEach(AIController.QuickAction.allCases) { a in
                    Button { ai.run(a, api: api) } label: {
                        HStack(spacing: 6) {
                            Image(systemName: a.icon)
                                .font(.system(size: 11, weight: .semibold))
                                .foregroundStyle(aiGradient)
                            Text(a.title)
                                .font(.callout)
                        }
                        .padding(.horizontal, 12).padding(.vertical, 7)
                        .background(Capsule().fill(Color.white.opacity(0.07)))
                        .overlay(Capsule().strokeBorder(Color.white.opacity(0.14), lineWidth: 1))
                    }
                    .buttonStyle(.plain)
                    .disabled(ai.isLoading)
                }
            }

            Spacer()
        }
        .padding(.horizontal, AF.Space.m)
    }

    // MARK: - Message bubble

    @ViewBuilder private func messageBubble(_ m: APIClient.ChatMessage) -> some View {
        let isUser = m.role == "user"
        HStack(alignment: .bottom, spacing: 8) {
            if isUser { Spacer(minLength: 44) }
            if !isUser {
                Circle()
                    .fill(aiGradient)
                    .frame(width: 22, height: 22)
                    .overlay(
                        Image(systemName: "sparkles")
                            .font(.system(size: 10, weight: .bold))
                            .foregroundStyle(.white)
                    )
            }
            Text(m.content)
                .font(.callout)
                .textSelection(.enabled)
                .multilineTextAlignment(isUser ? .trailing : .leading)
                .padding(.horizontal, 14).padding(.vertical, 10)
                .background(
                    RoundedRectangle(cornerRadius: 20, style: .continuous)
                        .fill(isUser
                            ? LinearGradient(
                                colors: [AF.Palette.tint(.blue).opacity(0.40),
                                         AF.Palette.tint(.purple).opacity(0.30)],
                                startPoint: .topLeading, endPoint: .bottomTrailing)
                            : LinearGradient(
                                colors: [Color.white.opacity(0.12), Color.white.opacity(0.07)],
                                startPoint: .topLeading, endPoint: .bottomTrailing)
                        )
                )
                .overlay(
                    RoundedRectangle(cornerRadius: 20, style: .continuous)
                        .strokeBorder(
                            isUser ? AF.Palette.tint(.blue).opacity(0.35) : Color.white.opacity(0.12),
                            lineWidth: 1)
                )
            if !isUser { Spacer(minLength: 44) }
        }
    }

    // MARK: - Thinking bubble

    @ViewBuilder private var thinkingBubble: some View {
        HStack(alignment: .bottom, spacing: 8) {
            Circle()
                .fill(aiGradient)
                .frame(width: 22, height: 22)
                .overlay(
                    Image(systemName: "sparkles")
                        .font(.system(size: 10, weight: .bold))
                        .foregroundStyle(.white)
                )
            HStack(spacing: 6) {
                ProgressView()
                    .controlSize(.small)
                    .tint(AF.Palette.tint(.purple))
                Text("Thinking…")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, 14).padding(.vertical, 10)
            .background(
                RoundedRectangle(cornerRadius: 20, style: .continuous)
                    .fill(Color.white.opacity(0.08))
            )
            Spacer(minLength: 44)
        }
    }

    // MARK: - Error bubble

    @ViewBuilder private func errorBubble(_ message: String) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "exclamationmark.circle.fill")
                .foregroundStyle(.red.opacity(0.85))
            Text(message)
                .font(.callout)
                .foregroundStyle(.red.opacity(0.85))
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(12)
        .background(
            RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                .fill(Color.red.opacity(0.08))
        )
        .overlay(
            RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                .strokeBorder(Color.red.opacity(0.20), lineWidth: 1)
        )
    }

    // MARK: - Citation strip

    @ViewBuilder private func citationStrip(_ sources: [String]) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Sources used")
                .font(.caption2.weight(.bold))
                .tracking(0.8)
                .foregroundStyle(.white.opacity(0.40))
                .textCase(.uppercase)
                .padding(.leading, 2)
            FlowLayout(spacing: 6) {
                ForEach(Array(sources.enumerated()), id: \.offset) { _, name in
                    Button {
                        router.open(.document(filename: name, caseID: caseID))
                    } label: {
                        HStack(spacing: 4) {
                            Image(systemName: "doc.text.fill")
                            Text(name).lineLimit(1)
                        }
                        .font(.caption)
                        .padding(.horizontal, 8).padding(.vertical, 4)
                        .background(Capsule().fill(AF.Palette.tint(.blue).opacity(0.15)))
                        .overlay(Capsule().strokeBorder(AF.Palette.tint(.blue).opacity(0.40), lineWidth: 1))
                        .foregroundStyle(AF.Palette.tint(.blue))
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    // MARK: - Composer

    @ViewBuilder private var composer: some View {
        VStack(spacing: 0) {
            Divider().opacity(0.15)
            HStack(alignment: .bottom, spacing: 8) {
                TextField("Message…", text: $input, axis: .vertical)
                    .textFieldStyle(.plain)
                    .lineLimit(1...5)
                    .focused($focused)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 10)
                    .background(
                        RoundedRectangle(cornerRadius: 22, style: .continuous)
                            .fill(Color.white.opacity(focused ? 0.09 : 0.06))
                    )
                    .overlay(
                        RoundedRectangle(cornerRadius: 22, style: .continuous)
                            .strokeBorder(
                                focused ? AF.Palette.tint(.purple).opacity(0.50) : Color.white.opacity(0.10),
                                lineWidth: 1)
                    )
                    .animation(.easeOut(duration: 0.15), value: focused)

                Button { submit() } label: {
                    Image(systemName: "arrow.up")
                        .font(.system(size: 13, weight: .bold))
                        .padding(10)
                }
                .buttonStyle(.afPrimary)
                .keyboardShortcut(.return, modifiers: [.command])
                .disabled(input.trimmingCharacters(in: .whitespaces).isEmpty || ai.isLoading)
            }
            .padding(.horizontal, AF.Space.m)
            .padding(.vertical, AF.Space.m)
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
            if x + s.width > maxW { x = 0; y += lineH + spacing; lineH = 0 }
            x += s.width + spacing
            lineH = max(lineH, s.height)
        }
        return CGSize(width: maxW.isFinite ? maxW : x, height: y + lineH)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x: CGFloat = bounds.minX, y: CGFloat = bounds.minY, lineH: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(.unspecified)
            if x + s.width > bounds.maxX { x = bounds.minX; y += lineH + spacing; lineH = 0 }
            v.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(s))
            x += s.width + spacing
            lineH = max(lineH, s.height)
        }
    }
}
