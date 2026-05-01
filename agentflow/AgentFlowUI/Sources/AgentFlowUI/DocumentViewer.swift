import SwiftUI
import PDFKit
import AppKit
import UniformTypeIdentifiers

// MARK: - Document viewer sheet

struct DocumentViewer: View {
    @EnvironmentObject var api: APIClient
    @Environment(\.dismiss) private var dismiss
    let filename: String
    let caseID: String?
    var onDeleted: () async -> Void

    enum Mode { case pdf, image, text, unsupported, loading, error(String) }
    @State private var mode: Mode = .loading
    @State private var data: Data?
    @State private var textContent: String = ""
    @State private var editable: Bool = false
    @State private var dirty: Bool = false
    @State private var saving: Bool = false
    @State private var toast: String?
    @State private var confirmDelete = false

    var body: some View {
        VStack(spacing: 0) {
            // Toolbar
            HStack(spacing: 8) {
                Image(systemName: icon)
                    .foregroundStyle(AF.Palette.tint(.blue))
                Text(filename).font(.headline).lineLimit(1)
                Spacer()
                if isTextLike {
                    Toggle(isOn: $editable) {
                        Label("Edit", systemImage: editable ? "pencil.circle.fill" : "pencil.circle")
                            .labelStyle(.titleAndIcon)
                    }
                    .toggleStyle(.button)
                    .controlSize(.regular)
                    .disabled(saving)
                }
                if editable && isTextLike {
                    Button {
                        save()
                    } label: {
                        if saving { ProgressView().controlSize(.small) }
                        else { Label("Save", systemImage: "square.and.arrow.down") }
                    }
                    .buttonStyle(.afPrimary)
                    .disabled(!dirty || saving)
                }
                Button {
                    Task { await reveal() }
                } label: {
                    Label("Reveal", systemImage: "folder")
                }
                .buttonStyle(.afGhost)

                Button(role: .destructive) {
                    confirmDelete = true
                } label: {
                    Label("Delete", systemImage: "trash")
                }
                .buttonStyle(.afGhost)
                .disabled(caseID == nil)
                .confirmationDialog(
                    "Remove \(filename)?",
                    isPresented: $confirmDelete,
                    titleVisibility: .visible
                ) {
                    Button("Remove", role: .destructive) { Task { await delete() } }
                    Button("Cancel", role: .cancel) {}
                } message: {
                    Text("This removes the document from this case.")
                }

                Button {
                    dismiss()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                }
                .buttonStyle(.plain)
            }
            .padding(AF.Space.m)
            .background(.ultraThinMaterial)
            .overlay(Rectangle().fill(Color.white.opacity(0.06)).frame(height: 1), alignment: .bottom)

            // Body
            ZStack {
                AmbientBackground().opacity(0.5)
                content
            }
            .frame(minWidth: 820, minHeight: 560)
        }
        .background(
            RoundedRectangle(cornerRadius: AF.Radius.xl, style: .continuous)
                .fill(Color.black.opacity(0.85))
        )
        .task(id: filename) { await load() }
        .overlay(alignment: .top) {
            if let t = toast {
                Text(t)
                    .padding(.horizontal, 14).padding(.vertical, 9)
                    .background(Capsule().fill(.ultraThinMaterial))
                    .padding(.top, AF.Space.m)
                    .transition(.move(edge: .top).combined(with: .opacity))
            }
        }
        .animation(.spring(duration: 0.3), value: toast)
    }

    @ViewBuilder private var content: some View {
        switch mode {
        case .loading:
            ProgressView("Loading \(filename)…")
                .controlSize(.large)
                .frame(maxWidth: .infinity, maxHeight: .infinity)

        case .error(let msg):
            EmptyStateView(icon: "exclamationmark.triangle",
                           title: "Could not open",
                           subtitle: msg)

        case .pdf:
            if let d = data, let doc = PDFDocument(data: d) {
                PDFKitView(document: doc)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                EmptyStateView(icon: "doc.richtext",
                               title: "PDF unavailable",
                               subtitle: "The file could not be parsed as a PDF.")
            }

        case .image:
            if let d = data, let img = NSImage(data: d) {
                ScrollView([.horizontal, .vertical]) {
                    Image(nsImage: img)
                        .resizable()
                        .aspectRatio(contentMode: .fit)
                        .padding(AF.Space.m)
                }
            } else {
                EmptyStateView(icon: "photo",
                               title: "Image unavailable",
                               subtitle: "The file could not be rendered.")
            }

        case .text:
            if editable {
                TextEditor(text: $textContent)
                    .font(.system(.body, design: .monospaced))
                    .scrollContentBackground(.hidden)
                    .padding(AF.Space.m)
                    .background(Color.black.opacity(0.3))
                    .onChange(of: textContent) { _, _ in dirty = true }
            } else {
                ScrollView {
                    Text(textContent.isEmpty ? "(empty file)" : textContent)
                        .font(.system(.body, design: .monospaced))
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(AF.Space.m)
                }
            }

        case .unsupported:
            VStack(spacing: AF.Space.m) {
                EmptyStateView(icon: "questionmark.square.dashed",
                               title: "Preview unavailable",
                               subtitle: "This file type can't be previewed here. Reveal it in Finder to open with another app.")
                Button {
                    Task { await reveal() }
                } label: {
                    Label("Reveal in Finder", systemImage: "folder")
                }
                .buttonStyle(.afPrimary)
            }
        }
    }

    // MARK: - Helpers

    private var ext: String { (filename as NSString).pathExtension.lowercased() }
    private var isTextLike: Bool {
        ["txt", "md", "json", "xml", "yml", "yaml", "log", "csv", "tsv", "go", "js", "ts", "swift", "py", "html", "css"].contains(ext)
    }
    private var icon: String {
        switch ext {
        case "pdf": return "doc.richtext.fill"
        case "png", "jpg", "jpeg", "heic", "gif", "webp": return "photo.fill"
        case "docx", "doc": return "doc.text.fill"
        case "zip": return "archivebox.fill"
        case "txt", "md", "csv", "log": return "text.alignleft"
        default: return "doc.fill"
        }
    }

    // MARK: - I/O

    private func load() async {
        mode = .loading
        do {
            let bytes = try await api.documentBytes(filename: filename)
            data = bytes

            switch ext {
            case "pdf": mode = .pdf
            case "png", "jpg", "jpeg", "heic", "gif", "webp": mode = .image
            case "txt", "md", "json", "xml", "yml", "yaml", "log", "csv", "tsv",
                 "go", "js", "ts", "swift", "py", "html", "css":
                textContent = String(data: bytes, encoding: .utf8) ?? ""
                dirty = false
                mode = .text
            default:
                mode = .unsupported
            }
        } catch {
            mode = .error(error.localizedDescription)
        }
    }

    private func save() {
        guard isTextLike else { return }
        saving = true
        Task {
            defer { saving = false }
            let tmp = FileManager.default.temporaryDirectory.appendingPathComponent(filename)
            do {
                try textContent.data(using: .utf8)?.write(to: tmp, options: .atomic)
                _ = try await api.uploadFile(url: tmp, caseID: caseID)
                try? FileManager.default.removeItem(at: tmp)
                dirty = false
                show("Saved")
                await onDeleted() // trigger refresh upstream
            } catch {
                show("Save failed: \(error.localizedDescription)")
            }
        }
    }

    private func delete() async {
        guard let cid = caseID else { return }
        do {
            try await api.removeDocument(caseID: cid, filename: filename)
            await onDeleted()
            dismiss()
        } catch {
            show("Delete failed: \(error.localizedDescription)")
        }
    }

    private func reveal() async {
        // Save to Downloads so the user can open in any app
        guard let d = data else { return }
        let downloads = FileManager.default.urls(for: .downloadsDirectory, in: .userDomainMask)[0]
        let dst = downloads.appendingPathComponent(filename)
        do {
            try d.write(to: dst, options: .atomic)
            NSWorkspace.shared.activateFileViewerSelecting([dst])
            show("Saved to Downloads")
        } catch {
            show("Reveal failed: \(error.localizedDescription)")
        }
    }

    private func show(_ text: String) {
        toast = text
        Task {
            try? await Task.sleep(nanoseconds: 2_000_000_000)
            if toast == text { toast = nil }
        }
    }
}

// MARK: - PDFKit bridge

struct PDFKitView: NSViewRepresentable {
    let document: PDFDocument

    func makeNSView(context: Context) -> PDFView {
        let v = PDFView()
        v.document = document
        v.autoScales = true
        v.displayMode = .singlePageContinuous
        v.displayDirection = .vertical
        v.backgroundColor = .clear
        return v
    }

    func updateNSView(_ nsView: PDFView, context: Context) {
        if nsView.document !== document { nsView.document = document }
    }
}
