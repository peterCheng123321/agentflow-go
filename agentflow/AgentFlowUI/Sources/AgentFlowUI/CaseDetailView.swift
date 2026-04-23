import SwiftUI

struct CaseDetailView: View {
    @EnvironmentObject var api: APIClient
    @EnvironmentObject var router: AppRouter
    let caseID: String
    let onChanged: () async -> Void

    @State private var detail: Case?
    @State private var loading = false
    @State private var errorMsg: String?
    @State private var newNote = ""
    @State private var busy = false
    @State private var toast: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: AF.Space.l) {
                header
                workflow
                notes
                documents
            }
            .padding(AF.Space.l)
            .frame(maxWidth: 960, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .task(id: caseID) { await load() }
        .overlay(alignment: .top) {
            if let t = toast {
                Text(t)
                    .padding(.horizontal, 14).padding(.vertical, 9)
                    .background(
                        Capsule().fill(.ultraThinMaterial)
                    )
                    .overlay(Capsule().strokeBorder(.white.opacity(0.1)))
                    .padding(.top, AF.Space.m)
                    .transition(.move(edge: .top).combined(with: .opacity))
            }
        }
        .animation(.spring(duration: 0.3), value: toast)
    }

    // MARK: - Header card

    @ViewBuilder private var header: some View {
        GlassCard(radius: AF.Radius.xl) {
            VStack(alignment: .leading, spacing: AF.Space.s) {
                HStack(alignment: .top) {
                    VStack(alignment: .leading, spacing: 4) {
                        Text(detail?.displayName ?? "Loading…")
                            .font(.largeTitle.weight(.bold))
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
                    HStack(spacing: AF.Space.l) {
                        MetaItem(icon: "number", label: "Case ID", value: d.case_id)
                        MetaItem(icon: "antenna.radiowaves.left.and.right",
                                 label: "Source", value: d.source_channel)
                        MetaItem(icon: "clock",
                                 label: "Updated",
                                 value: d.updated_at.formatted(date: .abbreviated, time: .shortened))
                    }
                    if !d.initial_msg.isEmpty {
                        Text(d.initial_msg)
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .padding(.top, 4)
                    }
                    HStack(spacing: AF.Space.s) {
                        AskAIButton(caseID: caseID)

                        Button {
                            act { try await api.advance(caseID) }
                        } label: {
                            Label("Advance state", systemImage: "arrow.right.circle.fill")
                        }
                        .buttonStyle(.afGhost)
                        .disabled(busy)

                        if needsApproval(d.state) {
                            Button {
                                act { try await api.approveHITL(caseID, state: d.state, approved: true, reason: "Approved via UI") }
                            } label: {
                                Label("Approve HITL", systemImage: "checkmark.seal.fill")
                            }
                            .buttonStyle(.afGhost)
                            .disabled(busy)
                        }

                        Spacer()

                        Button(role: .destructive) {
                            act { try await api.deleteCase(caseID); await onChanged() }
                        } label: {
                            Label("Delete", systemImage: "trash")
                        }
                        .buttonStyle(.afGhost)
                        .disabled(busy)
                    }
                    .padding(.top, AF.Space.s)
                } else if let e = errorMsg {
                    Text(e).foregroundStyle(.red).font(.callout)
                }
            }
        }
    }

    private func needsApproval(_ state: String) -> Bool {
        // HITL gates defined in engine.go: CaseEvaluation, FeeCollection, ClientReview, Filing
        ["CASE_EVALUATION", "FEE_COLLECTION", "CLIENT_REVIEW", "FILING"].contains(state)
    }

    // MARK: - Workflow timeline

    @ViewBuilder private var workflow: some View {
        GlassCard {
            VStack(alignment: .leading, spacing: AF.Space.m) {
                SectionHeader(title: "Workflow", subtitle: "Progression through the legal case pipeline")
                let current = detail?.state ?? ""
                let curIdx = WorkflowState.ordered(from: current)
                HStack(spacing: 0) {
                    ForEach(Array(WorkflowState.allCases.enumerated()), id: \.offset) { (idx, s) in
                        let done = idx <= curIdx && !current.isEmpty
                        VStack(spacing: 6) {
                            ZStack {
                                Circle()
                                    .strokeBorder(done ? AF.Palette.tint(s.accent) : Color.white.opacity(0.12),
                                                  lineWidth: 1.5)
                                    .background(
                                        Circle().fill(done
                                            ? AF.Palette.tint(s.accent).opacity(0.18)
                                            : Color.clear)
                                    )
                                    .frame(width: 22, height: 22)
                                if done {
                                    Circle().fill(AF.Palette.tint(s.accent)).frame(width: 8, height: 8)
                                }
                            }
                            Text(s.pretty)
                                .font(.caption2)
                                .foregroundStyle(done ? .primary : .secondary)
                                .lineLimit(2)
                                .multilineTextAlignment(.center)
                                .frame(maxWidth: 64)
                        }
                        if idx < WorkflowState.allCases.count - 1 {
                            Rectangle()
                                .fill(idx < curIdx ? AF.Palette.tint(s.accent).opacity(0.6) : Color.white.opacity(0.08))
                                .frame(height: 1.5)
                                .padding(.top, -18)
                        }
                    }
                }
            }
        }
    }

    // MARK: - Notes

    @ViewBuilder private var notes: some View {
        GlassCard {
            VStack(alignment: .leading, spacing: AF.Space.m) {
                HStack {
                    SectionHeader(title: "Notes", subtitle: "Internal annotations visible to the team")
                    Spacer()
                    Text("\(detail?.noteCount ?? 0)")
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
                HStack(spacing: AF.Space.s) {
                    TextField("Add a note…", text: $newNote, axis: .vertical)
                        .textFieldStyle(.plain)
                        .lineLimit(1...3)
                        .padding(10)
                        .background(
                            RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                                .fill(Color.black.opacity(0.22))
                        )
                    Button {
                        let text = newNote.trimmingCharacters(in: .whitespacesAndNewlines)
                        guard !text.isEmpty else { return }
                        act {
                            try await api.addNote(caseID, text: text)
                            newNote = ""
                        }
                    } label: {
                        Label("Add", systemImage: "plus").labelStyle(.titleAndIcon)
                    }
                    .buttonStyle(.afPrimary)
                    .disabled(busy || newNote.trimmingCharacters(in: .whitespaces).isEmpty)
                }

                if let d = detail, let arr = d.notes, !arr.isEmpty {
                    VStack(alignment: .leading, spacing: AF.Space.s) {
                        ForEach(arr.indices, id: \.self) { i in
                            let n = arr[arr.count - 1 - i]
                            HStack(alignment: .top, spacing: 10) {
                                Image(systemName: "note.text")
                                    .foregroundStyle(.secondary)
                                    .padding(.top, 2)
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(n.text).font(.callout)
                                    Text(n.timestamp.formatted(date: .abbreviated, time: .shortened))
                                        .font(.caption2)
                                        .foregroundStyle(.tertiary)
                                }
                                Spacer(minLength: 0)
                            }
                            .padding(10)
                            .background(
                                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                                    .fill(Color.white.opacity(0.04))
                            )
                        }
                    }
                } else {
                    Text("No notes yet.")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.vertical, 4)
                }
            }
        }
    }

    // MARK: - Documents

    @ViewBuilder private var documents: some View {
        GlassCard {
            VStack(alignment: .leading, spacing: AF.Space.m) {
                HStack {
                    SectionHeader(title: "Documents", subtitle: "Files attached to this case")
                    Spacer()
                    Text("\(detail?.docCount ?? 0)")
                        .font(.caption.monospacedDigit())
                        .foregroundStyle(.secondary)
                }
                UploadZone(caseID: caseID, onDone: { await load(); await onChanged() })

                if let docs = detail?.uploaded_documents, !docs.isEmpty {
                    VStack(alignment: .leading, spacing: 6) {
                        ForEach(docs, id: \.self) { name in
                            HStack(spacing: 10) {
                                Image(systemName: iconFor(name))
                                    .foregroundStyle(AF.Palette.tint(.blue))
                                Button {
                                    router.open(.document(filename: name, caseID: caseID))
                                } label: {
                                    Text(name).font(.callout).lineLimit(1)
                                        .frame(maxWidth: .infinity, alignment: .leading)
                                }
                                .buttonStyle(.plain)
                                Text(fileExt(name))
                                    .font(.caption2.weight(.semibold))
                                    .padding(.horizontal, 7)
                                    .padding(.vertical, 3)
                                    .background(Capsule().fill(.ultraThinMaterial))
                                    .foregroundStyle(.secondary)
                                Button {
                                    Task {
                                        try? await api.removeDocument(caseID: caseID, filename: name)
                                        await load(); await onChanged()
                                    }
                                } label: {
                                    Image(systemName: "trash")
                                }
                                .buttonStyle(.afGhost)
                            }
                            .padding(.horizontal, 10).padding(.vertical, 8)
                            .background(
                                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                                    .fill(Color.white.opacity(0.04))
                            )
                        }
                    }
                } else {
                    Text("No documents attached yet — drop files above.")
                        .font(.callout).foregroundStyle(.secondary)
                }
            }
        }
    }

    private func iconFor(_ name: String) -> String {
        let ext = fileExt(name).lowercased()
        switch ext {
        case "pdf": return "doc.richtext.fill"
        case "png", "jpg", "jpeg", "heic", "gif": return "photo.fill"
        case "docx", "doc": return "doc.text.fill"
        case "zip": return "archivebox.fill"
        case "txt", "md": return "text.alignleft"
        default: return "doc.fill"
        }
    }
    private func fileExt(_ s: String) -> String {
        (s as NSString).pathExtension
    }

    // MARK: - Data

    private func load() async {
        loading = true; defer { loading = false }
        do {
            detail = try await api.getCase(caseID)
            errorMsg = nil
        } catch {
            errorMsg = error.localizedDescription
        }
    }

    private func act(_ op: @escaping () async throws -> Void) {
        Task {
            busy = true; defer { busy = false }
            do {
                try await op()
                await load()
                await onChanged()
                show("Updated")
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

struct MetaItem: View {
    let icon: String
    let label: String
    let value: String
    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: icon)
                .font(.caption)
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 1) {
                Text(label).font(.caption2).foregroundStyle(.tertiary).textCase(.uppercase)
                Text(value).font(.callout.weight(.medium))
            }
        }
    }
}
