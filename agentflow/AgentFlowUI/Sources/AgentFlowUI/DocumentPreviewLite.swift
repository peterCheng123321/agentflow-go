import AppKit
import Quartz

// MARK: - Public API

/// A small AppKit bridge that surfaces the system Quick Look panel
/// (the same overlay Finder shows when you press space on a file).
///
/// Usage:
///   QuickLookCenter.show([URL(fileURLWithPath: "/etc/hosts")])
///   QuickLookCenter.hide()
///
/// Files must already exist on disk. For remote files (e.g. ones served
/// by the local Go backend), download them to a temporary directory
/// first and pass the resulting file URLs.
@MainActor
enum QuickLookCenter {
    /// Show the macOS QuickLook panel for the given file URLs. Pass an array
    /// so multi-selection previews navigate via arrow keys. Files must
    /// already be on disk (download first if remote).
    static func show(_ urls: [URL]) {
        guard !urls.isEmpty else { return }
        QuickLookCoordinator.shared.present(urls: urls)
    }

    /// Hide the panel if it's open.
    static func hide() {
        QuickLookCoordinator.shared.dismiss()
    }
}

// MARK: - Coordinator (internal singleton)

/// Backs `QLPreviewPanel.shared()` with a stable data source/delegate.
/// Quick Look's shared panel demands a long-lived owner — that's us.
@MainActor
private final class QuickLookCoordinator: NSObject {
    static let shared = QuickLookCoordinator()

    private var urls: [URL] = []

    private override init() {
        super.init()
    }

    func present(urls: [URL]) {
        self.urls = urls
        guard let panel = QLPreviewPanel.shared() else { return }
        // Hand control to us before calling makeKey — Quick Look queries the
        // current responder chain for a controller and would otherwise reject
        // the call when no SwiftUI view has volunteered.
        panel.dataSource = self
        panel.delegate = self
        panel.reloadData()
        panel.makeKeyAndOrderFront(nil)
    }

    func dismiss() {
        guard QLPreviewPanel.sharedPreviewPanelExists(),
              let panel = QLPreviewPanel.shared() else { return }
        panel.orderOut(nil)
    }
}

// MARK: - QLPreviewPanelDataSource

extension QuickLookCoordinator: @MainActor QLPreviewPanelDataSource {
    func numberOfPreviewItems(in panel: QLPreviewPanel!) -> Int {
        urls.count
    }

    func previewPanel(_ panel: QLPreviewPanel!, previewItemAt index: Int) -> QLPreviewItem! {
        guard urls.indices.contains(index) else { return nil }
        return urls[index] as NSURL
    }
}

// MARK: - QLPreviewPanelDelegate

extension QuickLookCoordinator: @MainActor QLPreviewPanelDelegate {
    // Default delegate behavior is fine; explicit conformance keeps the
    // contract obvious and lets future hooks (e.g. transitions, key handling)
    // land here without changing the public API.
}

// No preview — system overlay panel.
