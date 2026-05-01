import SwiftUI

// MARK: - Materials empty state
//
// Replaces the legacy single-line "No documents yet —" copy with a proper
// drop-target surface. The parent (CaseHubView's materials section) tracks
// drag-over state and passes it in via `isDragOver`; this view only knows how
// to render the two visual states and how to fire the "Pick files…" action.
//
// Don't fold this into `EmptyStateView` in GlassTheme.swift — that variant is
// a static informational placeholder, whereas this one needs the dashed
// drop-target border and the highlighted "ready to drop" mode.

struct MaterialsEmptyState: View {
    /// True when the parent has detected an in-flight drag. Triggers the
    /// "ready to drop" highlighted state.
    let isDragOver: Bool
    /// Action to fire when the user clicks the empty-state CTA. Parent should
    /// open the upload picker.
    let onPickFiles: () -> Void

    private var iconColor: Color {
        isDragOver ? AF.Palette.tint(.blue) : AF.Palette.textSecondary
    }

    private var borderColor: Color {
        isDragOver ? AF.Palette.tint(.blue) : AF.Palette.separator
    }

    private var title: String {
        isDragOver
            ? "Release to upload"
            : "Drop pleadings, exhibits, anything."
    }

    var body: some View {
        VStack(spacing: AF.Space.l) {
            Image(systemName: "tray.and.arrow.down")
                .font(.system(size: 56))
                .foregroundStyle(iconColor)
                .animation(.spring(duration: 0.25), value: isDragOver)

            Text(title)
                .font(.title3.weight(.semibold))
                .multilineTextAlignment(.center)

            if !isDragOver {
                Text("We'll OCR them, classify each one (ID, complaint, evidence…), and index them for case-aware AI research.")
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 380)

                Button("Pick files…", action: onPickFiles)
                    .buttonStyle(.bordered)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(AF.Space.l)
        .overlay(
            RoundedRectangle(cornerRadius: AF.Radius.l, style: .continuous)
                .strokeBorder(
                    borderColor,
                    style: StrokeStyle(lineWidth: 1.5, dash: [6, 4])
                )
                .animation(.spring(duration: 0.25), value: isDragOver)
        )
    }
}

#Preview("Default") {
    MaterialsEmptyState(isDragOver: false, onPickFiles: {})
        .padding()
        .frame(width: 600, height: 380)
}

#Preview("Drag over") {
    MaterialsEmptyState(isDragOver: true, onPickFiles: {})
        .padding()
        .frame(width: 600, height: 380)
}
