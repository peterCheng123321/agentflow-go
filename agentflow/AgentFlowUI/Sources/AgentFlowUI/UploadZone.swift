import SwiftUI
import UniformTypeIdentifiers
import AppKit

/// A drag-and-drop + click-to-pick area that uploads files to the given case.
struct UploadZone: View {
    @EnvironmentObject var api: APIClient
    let caseID: String?
    var onDone: () async -> Void

    @State private var hovering = false
    @State private var jobs: [UploadJob] = []
    @State private var error: String?

    struct UploadJob: Identifiable, Equatable {
        let id = UUID()
        let filename: String
        var progress: Double
        var status: Status
        enum Status: Equatable { case uploading, processing, done, failed(String) }
    }

    var body: some View {
        VStack(spacing: AF.Space.s) {
            Button {
                pickFile()
            } label: {
                VStack(spacing: 10) {
                    ZStack {
                        Circle().fill(.ultraThinMaterial).frame(width: 52, height: 52)
                        Image(systemName: "arrow.up.doc.fill")
                            .font(.system(size: 22, weight: .semibold))
                            .foregroundStyle(AF.Palette.tint(.blue))
                    }
                    VStack(spacing: 2) {
                        Text("Drop files or click to upload")
                            .font(.callout.weight(.semibold))
                        Text("PDF, images, text, DOCX, ZIP — processed automatically")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, AF.Space.l)
                .padding(.horizontal, AF.Space.m)
                .background(
                    RoundedRectangle(cornerRadius: AF.Radius.l, style: .continuous)
                        .fill(.ultraThinMaterial)
                )
                .overlay(
                    RoundedRectangle(cornerRadius: AF.Radius.l, style: .continuous)
                        .strokeBorder(
                            hovering ? AF.Palette.tint(.blue) : .white.opacity(0.08),
                            style: StrokeStyle(lineWidth: hovering ? 2 : 1, dash: hovering ? [] : [4, 4])
                        )
                )
            }
            .buttonStyle(.plain)
            .onDrop(of: [.fileURL], isTargeted: $hovering) { providers in
                handleDrop(providers: providers)
                return true
            }

            if !jobs.isEmpty {
                VStack(spacing: 6) {
                    ForEach(jobs) { j in
                        HStack(spacing: 10) {
                            iconFor(j)
                            Text(j.filename).lineLimit(1).font(.callout)
                            Spacer()
                            switch j.status {
                            case .uploading:
                                ProgressView(value: j.progress).frame(width: 80)
                                Text("Uploading").font(.caption).foregroundStyle(.secondary)
                            case .processing:
                                ProgressView().controlSize(.small)
                                Text("Processing").font(.caption).foregroundStyle(.secondary)
                            case .done:
                                Text("Ingested").font(.caption).foregroundStyle(.green)
                            case .failed(let m):
                                Text(m).font(.caption).foregroundStyle(.red).lineLimit(1)
                            }
                        }
                        .padding(.horizontal, 10).padding(.vertical, 8)
                        .background(
                            RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                                .fill(Color.white.opacity(0.04))
                        )
                    }
                }
            }

            if let e = error {
                Text(e).font(.caption).foregroundStyle(.red)
            }
        }
    }

    @ViewBuilder private func iconFor(_ j: UploadJob) -> some View {
        switch j.status {
        case .uploading, .processing:
            Image(systemName: "arrow.up.circle").foregroundStyle(.secondary)
        case .done:
            Image(systemName: "checkmark.circle.fill").foregroundStyle(.green)
        case .failed:
            Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange)
        }
    }

    // MARK: - Actions

    private func pickFile() {
        let panel = NSOpenPanel()
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = true
        panel.message = "Pick files to upload to this case"
        panel.prompt = "Upload"
        panel.begin { resp in
            if resp == .OK {
                for url in panel.urls {
                    upload(url: url)
                }
            }
        }
    }

    private func handleDrop(providers: [NSItemProvider]) {
        for p in providers {
            p.loadItem(forTypeIdentifier: UTType.fileURL.identifier, options: nil) { item, _ in
                let url: URL?
                if let u = item as? URL {
                    url = u
                } else if let d = item as? Data {
                    url = URL(dataRepresentation: d, relativeTo: nil)
                } else if let s = item as? String {
                    url = URL(string: s)
                } else {
                    url = nil
                }
                if let u = url {
                    DispatchQueue.main.async { upload(url: u) }
                }
            }
        }
    }

    private func upload(url: URL) {
        let name = url.lastPathComponent
        var job = UploadJob(filename: name, progress: 0.0, status: .uploading)
        jobs.append(job)
        let jobID = job.id

        Task {
            do {
                let resp = try await api.uploadFile(url: url, caseID: caseID) { p in
                    Task { @MainActor in
                        if let idx = jobs.firstIndex(where: { $0.id == jobID }) {
                            jobs[idx].progress = p
                        }
                    }
                }
                if let idx = jobs.firstIndex(where: { $0.id == jobID }) {
                    jobs[idx].status = .processing
                }

                if let jid = resp.job_id {
                    // Poll ingestion job
                    let final = try await api.waitForJob(jid)
                    if let idx = jobs.firstIndex(where: { $0.id == jobID }) {
                        if final.status == "completed" {
                            jobs[idx].status = .done
                        } else {
                            jobs[idx].status = .failed(final.error ?? final.status)
                        }
                    }
                } else {
                    if let idx = jobs.firstIndex(where: { $0.id == jobID }) {
                        jobs[idx].status = .done
                    }
                }
                await onDone()
                // Auto-clean done rows after a moment
                Task {
                    try? await Task.sleep(nanoseconds: 3_500_000_000)
                    jobs.removeAll { $0.id == jobID && $0.status == .done }
                }
            } catch {
                if let idx = jobs.firstIndex(where: { $0.id == jobID }) {
                    jobs[idx].status = .failed(error.localizedDescription)
                }
            }
            _ = job
        }
    }
}
