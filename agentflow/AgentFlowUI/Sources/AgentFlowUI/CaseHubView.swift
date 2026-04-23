import SwiftUI

/// Case-centric workspace: next-step guidance, vertical pipeline, and tabbed work surfaces.
struct CaseHubView: View {
    @EnvironmentObject var api: APIClient
    @EnvironmentObject var router: AppRouter
    @EnvironmentObject var ai: AIController

    let caseID: String
    let onChanged: () async -> Void

    enum HubTab: String, CaseIterable, Identifiable {
        case summary = "Summary"
        case evidence = "Evidence"
        case activity = "Activity"
        case research = "Research"
        var id: String { rawValue }
        var icon: String {
            switch self {
            case .summary: return "rectangle.split.3x1"
            case .evidence: return "doc.viewfinder"
            case .activity: return "text.bubble"
            case .research: return "sparkles"
            }
        }
    }

    @State private var detail: Case?
    @State private var errorMsg: String?
    @State private var newNote = ""
    @State private var busy = false
    @State private var toast: String?
    @State private var tab: HubTab = .summary
    @State private var rejectReason = ""
    @State private var showRejectField = false
    @State private var confirmAdvance = false
    @State private var confirmDelete = false
    @State private var pendingRemoveDoc: String?

    var body: some View {
        VStack(spacing: 0) {
            hero
            Divider().opacity(0.12)
            HStack(alignment: .top, spacing: 0) {
                stepperRail
                    .frame(width: 200)
                Divider().opacity(0.12)
                tabbedContent
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .background(Color.black.opacity(0.12))
        .task(id: caseID) { await load() }
        .onChange(of: router.pendingCaseResearchFocus) { _, id in
            guard id == caseID else { return }
            tab = .research
            router.pendingCaseResearchFocus = nil
        }
        .overlay(alignment: .top) {
            if let t = toast {
                Text(t)
                    .padding(.horizontal, 14).padding(.vertical, 9)
                    .background(Capsule().fill(.thinMaterial))
                    .overlay(Capsule().strokeBorder(.white.opacity(0.12)))
                    .padding(.top, AF.Space.m)
                    .transition(.move(edge: .top).combined(with: .opacity))
            }
        }
        .animation(.spring(duration: 0.3), value: toast)
    }

    // MARK: - Hero (next step)

    @ViewBuilder private var hero: some View {
        VStack(alignment: .leading, spacing: AF.Space.m) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 6) {
                    Text(detail?.displayName ?? "Loading…")
                        .font(.title.weight(.bold))
                    Text(detail?.matter_type ?? " ")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                if let d = detail {
                    StatePill(state: d.state)
                }
            }

            if let d = detail {
                GlassCard(padding: AF.Space.m, radius: AF.Radius.l) {
                    VStack(alignment: .leading, spacing: 10) {
                        Label("Your next step", systemImage: "flag.checkered")
                            .font(.caption.weight(.bold))
                            .foregroundStyle(.white.opacity(0.55))
                        Text(WorkflowGuidance.headline(for: d.state))
                            .font(.title3.weight(.semibold))
                        Text(WorkflowGuidance.detail(for: d.state))
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)

                        if showRejectField {
                            TextField("Reason for rejection…", text: $rejectReason, axis: .vertical)
                                .textFieldStyle(.plain)
                                .lineLimit(2...4)
                                .padding(10)
                                .background(
                                    RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                                        .fill(Color.black.opacity(0.25))
                                )
                        }

                        HStack(spacing: AF.Space.s) {
                            Button {
                                tab = .research
                                ai.bind(toCase: caseID)
                            } label: {
                                Label("Open research", systemImage: "sparkles")
                            }
                            .buttonStyle(.afPrimary)

                            Button {
                                confirmAdvance = true
                            } label: {
                                Label("Advance stage", systemImage: "arrow.right.circle.fill")
                            }
                            .buttonStyle(.afGhost)
                            .disabled(busy)
                            .confirmationDialog(
                                "Advance to \(nextStatePretty(from: d.state))?",
                                isPresented: $confirmAdvance,
                                titleVisibility: .visible
                            ) {
                                Button("Advance", role: .destructive) {
                                    act { try await api.advance(caseID) }
                                }
                                Button("Cancel", role: .cancel) {}
                            } message: {
                                Text("You can't undo this without reverting manually.")
                            }

                            if d.needsLawyerAttention {
                                Button {
                                    act { try await api.approveHITL(caseID, state: d.state, approved: true, reason: "") }
                                } label: {
                                    Label("Approve", systemImage: "checkmark.seal.fill")
                                }
                                .buttonStyle(.afGhost)
                                .tint(.green)
                                .disabled(busy)

                                Button {
                                    if !showRejectField {
                                        showRejectField = true
                                    } else {
                                        let r = rejectReason.trimmingCharacters(in: .whitespacesAndNewlines)
                                        act { try await api.approveHITL(caseID, state: d.state, approved: false, reason: r) }
                                        showRejectField = false
                                        rejectReason = ""
                                    }
                                } label: {
                                    Label(showRejectField ? "Submit rejection" : "Reject…", systemImage: "xmark.circle")
                                }
                                .buttonStyle(.afGhost)
                                .tint(.orange)
                                .disabled(busy)
                            }

                            Spacer()

                            Button(role: .destructive) {
                                confirmDelete = true
                            } label: {
                                Image(systemName: "trash")
                            }
                            .buttonStyle(.afGhost)
                            .help("Delete matter")
                            .disabled(busy)
                            .confirmationDialog(
                                "Delete case \(d.displayName)?",
                                isPresented: $confirmDelete,
                                titleVisibility: .visible
                            ) {
                                Button("Delete", role: .destructive) {
                                    act { try await api.deleteCase(caseID); await onChanged() }
                                }
                                Button("Cancel", role: .cancel) {}
                            } message: {
                                Text("This permanently removes the case and all linked documents and notes.")
                            }
                        }
                    }
                }
            } else if let e = errorMsg {
                Text(e).foregroundStyle(.red).font(.callout)
            }
        }
        .padding(AF.Space.l)
        .background(.thinMaterial)
    }

    // MARK: - Vertical stepper

    @ViewBuilder private var stepperRail: some View {
        let current = detail?.state ?? ""
        let curIdx = WorkflowState.ordered(from: current)
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                SectionHeader(title: "Pipeline", subtitle: "Where this matter sits")
                    .padding(.bottom, AF.Space.s)
                ForEach(Array(WorkflowState.allCases.enumerated()), id: \.offset) { idx, s in
                    let done = idx <= curIdx && !current.isEmpty
                    let isCurrent = idx == curIdx && !current.isEmpty
                    HStack(alignment: .top, spacing: 10) {
                        VStack(spacing: 0) {
                            ZStack {
                                Circle()
                                    .strokeBorder(
                                        done ? AF.Palette.tint(s.accent) : Color.white.opacity(0.12),
                                        lineWidth: isCurrent ? 2.5 : 1.5
                                    )
                                    .background(
                                        Circle().fill(done ? AF.Palette.tint(s.accent).opacity(0.22) : Color.clear)
                                    )
                                    .frame(width: 22, height: 22)
                                if isCurrent {
                                    Circle().fill(AF.Palette.tint(s.accent)).frame(width: 9, height: 9)
                                } else if done {
                                    Image(systemName: "checkmark")
                                        .font(.system(size: 9, weight: .bold))
                                        .foregroundStyle(AF.Palette.tint(s.accent))
                                }
                            }
                            if idx < WorkflowState.allCases.count - 1 {
                                Rectangle()
                                    .fill(idx < curIdx ? AF.Palette.tint(s.accent).opacity(0.45) : Color.white.opacity(0.08))
                                    .frame(width: 2, height: 18)
                            }
                        }
                        .frame(width: 22)

                        VStack(alignment: .leading, spacing: 2) {
                            Text(s.pretty)
                                .font(.caption.weight(isCurrent ? .semibold : .regular))
                                .foregroundStyle(isCurrent ? AF.Palette.tint(s.accent) : (done ? .primary : .secondary))
                            if isCurrent {
                                Text("You are here")
                                    .font(.caption2)
                                    .foregroundStyle(.tertiary)
                            }
                        }
                        .padding(.bottom, idx < WorkflowState.allCases.count - 1 ? 10 : 0)
                    }
                }
            }
            .padding(AF.Space.m)
        }
        .background(Color.black.opacity(0.15))
    }

    // MARK: - Tabs

    @ViewBuilder private var tabbedContent: some View {
        VStack(spacing: 0) {
            HStack(spacing: 4) {
                ForEach(HubTab.allCases) { t in
                    Button {
                        tab = t
                        if t == .research { ai.bind(toCase: caseID) }
                    } label: {
                        Label(t.rawValue, systemImage: t.icon)
                            .labelStyle(.iconOnly)
                            .font(.system(size: 13, weight: .medium))
                            .frame(width: 36, height: 32)
                            .background(
                                RoundedRectangle(cornerRadius: AF.Radius.s, style: .continuous)
                                    .fill(tab == t ? Color.white.opacity(0.12) : Color.clear)
                            )
                    }
                    .buttonStyle(.plain)
                    .help(t.rawValue)
                }
                Spacer()
                Text(tab.rawValue.uppercased())
                    .font(.caption2.weight(.bold))
                    .tracking(1.2)
                    .foregroundStyle(.white.opacity(0.35))
            }
            .padding(.horizontal, AF.Space.m)
            .padding(.vertical, AF.Space.s)
            Divider().opacity(0.1)

            Group {
                switch tab {
                case .summary:
                    summaryPane
                case .evidence:
                    evidencePane
                case .activity:
                    activityPane
                case .research:
                    AIInspector(caseID: caseID)
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }

    @ViewBuilder private var summaryPane: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: AF.Space.l) {
                if let d = detail {
                    metaGrid(d)
                    if let ev = d.evaluation, !ev.isEmpty {
                        GlassCard {
                            VStack(alignment: .leading, spacing: AF.Space.s) {
                                SectionHeader(title: "AI evaluation", subtitle: "Latest structured review")
                                Text(ev)
                                    .font(.callout)
                                    .textSelection(.enabled)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                            }
                        }
                    }
                    if !d.initial_msg.isEmpty {
                        GlassCard {
                            VStack(alignment: .leading, spacing: AF.Space.s) {
                                SectionHeader(title: "Opening intake", subtitle: "First client message")
                                Text(d.initial_msg)
                                    .font(.callout)
                                    .foregroundStyle(.secondary)
                            }
                        }
                    }
                }
            }
            .padding(AF.Space.l)
        }
    }

    @ViewBuilder private func metaGrid(_ d: Case) -> some View {
        GlassCard {
            VStack(alignment: .leading, spacing: AF.Space.m) {
                SectionHeader(title: "Matter record")
                LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: AF.Space.m) {
                    MetaItem(icon: "number", label: "Matter ID", value: String(d.case_id.suffix(12)))
                    MetaItem(icon: "antenna.radiowaves.left.and.right", label: "Source", value: d.source_channel.isEmpty ? "—" : d.source_channel)
                    MetaItem(icon: "clock", label: "Opened", value: d.created_at.formatted(date: .abbreviated, time: .omitted))
                    MetaItem(icon: "clock.arrow.circlepath", label: "Updated", value: d.updated_at.formatted(date: .abbreviated, time: .shortened))
                }
            }
        }
    }

    @ViewBuilder private var evidencePane: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: AF.Space.l) {
                GlassCard {
                    VStack(alignment: .leading, spacing: AF.Space.m) {
                        SectionHeader(title: "Files & exhibits", subtitle: "Upload builds the firm’s searchable index")
                        UploadZone(caseID: caseID, onDone: { await load(); await onChanged() })
                    }
                }
                if let docs = detail?.uploaded_documents, !docs.isEmpty {
                    GlassCard {
                        VStack(alignment: .leading, spacing: AF.Space.m) {
                            SectionHeader(title: "On file")
                            documentList(docs)
                        }
                    }
                } else {
                    Text("No documents yet — add pleadings, scans, or correspondence in the card above.")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, AF.Space.l)
                }
            }
            .padding(AF.Space.l)
        }
    }

    @ViewBuilder private func documentList(_ docs: [String]) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            ForEach(docs, id: \.self) { name in
                HStack(spacing: 10) {
                    Image(systemName: iconForDoc(name))
                        .foregroundStyle(AF.Palette.tint(.blue))
                        .frame(width: 20)
                    Button {
                        router.open(.document(filename: name, caseID: caseID))
                    } label: {
                        Text(cleanFilename(name))
                            .font(.callout)
                            .lineLimit(1)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                    .buttonStyle(.plain)
                    Text(fileExt(name))
                        .font(.caption2.weight(.semibold))
                        .padding(.horizontal, 7).padding(.vertical, 3)
                        .background(Capsule().fill(.ultraThinMaterial))
                        .foregroundStyle(.secondary)
                    Button {
                        pendingRemoveDoc = name
                    } label: {
                        Image(systemName: "trash").foregroundStyle(.secondary)
                    }
                    .buttonStyle(.plain)
                }
                .confirmationDialog(
                    "Remove \(pendingRemoveDoc.map(cleanFilename) ?? "")?",
                    isPresented: Binding(
                        get: { pendingRemoveDoc == name },
                        set: { if !$0 { pendingRemoveDoc = nil } }
                    ),
                    titleVisibility: .visible
                ) {
                    Button("Remove", role: .destructive) {
                        let target = name
                        Task {
                            try? await api.removeDocument(caseID: caseID, filename: target)
                            pendingRemoveDoc = nil
                            await load(); await onChanged()
                        }
                    }
                    Button("Cancel", role: .cancel) { pendingRemoveDoc = nil }
                } message: {
                    Text("This removes the document from this case.")
                }
                .padding(.horizontal, 10).padding(.vertical, 8)
                .background(
                    RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                        .fill(Color.white.opacity(0.05))
                )
            }
        }
    }

    @ViewBuilder private var activityPane: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: AF.Space.l) {
                GlassCard {
                    VStack(alignment: .leading, spacing: AF.Space.m) {
                        SectionHeader(title: "Notes", subtitle: "Visible to everyone on this matter")
                        HStack(spacing: AF.Space.s) {
                            TextField("Add a note…", text: $newNote, axis: .vertical)
                                .textFieldStyle(.plain)
                                .lineLimit(1...3)
                                .padding(10)
                                .background(
                                    RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                                        .fill(Color.black.opacity(0.20))
                                )
                            Button {
                                let text = newNote.trimmingCharacters(in: .whitespacesAndNewlines)
                                guard !text.isEmpty else { return }
                                act {
                                    try await api.addNote(caseID, text: text)
                                    newNote = ""
                                }
                            } label: {
                                Label("Add", systemImage: "plus")
                            }
                            .buttonStyle(.afPrimary)
                            .disabled(busy || newNote.trimmingCharacters(in: .whitespaces).isEmpty)
                        }
                    }
                }
                if let d = detail, let arr = d.notes, !arr.isEmpty {
                    VStack(alignment: .leading, spacing: 8) {
                        ForEach(arr.indices, id: \.self) { i in
                            let n = arr[arr.count - 1 - i]
                            let system = isSystemNote(n.text)
                            noteRow(n, system: system)
                        }
                    }
                    .padding(.horizontal, AF.Space.l)
                } else {
                    Text("No notes yet.")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, AF.Space.l)
                }
            }
            .padding(.vertical, AF.Space.l)
        }
    }

    @ViewBuilder private func noteRow(_ n: Note, system: Bool) -> some View {
        HStack(alignment: .top, spacing: 10) {
            Image(systemName: system ? "gear.badge" : "note.text")
                .font(system ? .caption : .callout)
                .foregroundStyle(system ? Color.secondary.opacity(0.5) : AF.Palette.tint(.purple))
                .padding(.top, 2)
            VStack(alignment: .leading, spacing: 2) {
                Text(n.text)
                    .font(system ? .caption : .callout)
                    .foregroundStyle(system ? .secondary : .primary)
                    .italic(system)
                Text(n.timestamp.formatted(date: .abbreviated, time: .shortened))
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
            }
            Spacer(minLength: 0)
        }
        .padding(system ? 8 : 10)
        .background(
            RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                .fill(system ? Color.white.opacity(0.03) : Color.white.opacity(0.06))
        )
    }

    private func isSystemNote(_ text: String) -> Bool {
        text.hasPrefix("Document uploaded:") ||
        text.hasPrefix("Advanced to state:") ||
        text.hasPrefix("HITL") ||
        text.hasPrefix("Approved") ||
        text.hasPrefix("Case created") ||
        text.hasPrefix("Rejected")
    }

    private func iconForDoc(_ name: String) -> String {
        switch fileExt(name).lowercased() {
        case "pdf": return "doc.richtext.fill"
        case "png", "jpg", "jpeg", "heic": return "photo.fill"
        case "docx", "doc": return "doc.text.fill"
        case "zip": return "archivebox.fill"
        case "txt", "md": return "text.alignleft"
        default: return "doc.fill"
        }
    }
    private func nextStatePretty(from current: String) -> String {
        let idx = WorkflowState.ordered(from: current)
        let all = WorkflowState.allCases
        guard idx >= 0, idx + 1 < all.count else { return "next stage" }
        return all[idx + 1].pretty
    }
    private func fileExt(_ s: String) -> String { (s as NSString).pathExtension }
    private func cleanFilename(_ s: String) -> String {
        let ext = fileExt(s)
        guard !ext.isEmpty else { return s }
        let withoutExt = (s as NSString).deletingPathExtension
        if fileExt(withoutExt).lowercased() == ext.lowercased() { return withoutExt }
        return s
    }

    // MARK: - Data

    private func load() async {
        do {
            detail = try await api.getCase(caseID)
            errorMsg = nil
        } catch {
            errorMsg = error.localizedDescription
        }
    }

    private func act(_ op: @escaping () async throws -> Void) {
        Task {
            busy = true
            defer { busy = false }
            do {
                try await op()
                await load()
                await onChanged()
                show("Saved")
            } catch {
                show(error.localizedDescription, error: true)
            }
        }
    }

    private func show(_ text: String, error: Bool = false) {
        toast = text
        Task {
            try? await Task.sleep(nanoseconds: 2_500_000_000)
            if toast == text { toast = nil }
        }
    }
}
