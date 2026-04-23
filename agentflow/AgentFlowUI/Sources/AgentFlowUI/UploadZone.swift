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
                HStack(spacing: 12) {
                    Image(systemName: "arrow.up.doc.fill")
                        .font(.system(size: 16, weight: .semibold))
                        .foregroundStyle(hovering ? AF.Palette.tint(.blue) : .secondary)
                    VStack(alignment: .leading, spacing: 1) {
                        Text("Drop files or click to upload")
                            .font(.callout.weight(.medium))
                            .foregroundStyle(hovering ? .primary : .secondary)
                        Text("PDF, images, text, DOCX, ZIP")
                            .font(.caption)
                            .foregroundStyle(.tertiary)
                    }
                    Spacer()
                    Image(systemName: "plus.circle.fill")
                        .font(.system(size: 18))
                        .foregroundStyle(hovering ? AF.Palette.tint(.blue) : .secondary)
                }
                .padding(.horizontal, AF.Space.m)
                .padding(.vertical, AF.Space.s)
                .background(
                    RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                        .fill(hovering ? AF.Palette.tint(.blue).opacity(0.08) : Color.black.opacity(0.15))
                )
                .overlay(
                    RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                        .strokeBorder(
                            hovering ? AF.Palette.tint(.blue).opacity(0.60) : Color.white.opacity(0.10),
                            style: StrokeStyle(lineWidth: 1, dash: hovering ? [] : [5, 4])
                        )
                )
                .animation(.easeOut(duration: 0.15), value: hovering)
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
        let job = UploadJob(filename: name, progress: 0.0, status: .uploading)
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
