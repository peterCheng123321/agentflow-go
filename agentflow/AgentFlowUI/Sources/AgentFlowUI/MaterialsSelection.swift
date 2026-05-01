import SwiftUI

// MARK: - MaterialsSelection
//
// Multi-select state for the Materials pane. Holds a set of currently
// selected filenames and exposes minimal mutation primitives the parent
// list (and the bulk-action bar below) drive. Single source of truth so
// both row checkmarks and the action bar see consistent state.

@MainActor
final class MaterialsSelection: ObservableObject {
    @Published private(set) var selected: Set<String> = []

    /// Toggle membership of `filename` in the selection.
    func toggle(_ filename: String) {
        if selected.insert(filename).inserted == false {
            selected.remove(filename)
        }
    }

    /// Whether `filename` is currently selected.
    func contains(_ filename: String) -> Bool {
        selected.contains(filename)
    }

    /// Drop the entire selection.
    func clear() {
        selected.removeAll()
    }

    /// Replace the selection with the given filenames (deduplicated).
    func selectAll(_ filenames: [String]) {
        selected = Set(filenames)
    }
}

// MARK: - MaterialsBulkActionBar
//
// Bottom action bar that appears when the user has one or more files
// selected in the Materials pane. The parent is responsible for mounting
// this view via `.safeAreaInset(edge: .bottom) { ... }`. When the
// selection is empty the view collapses to `EmptyView()` so the host
// pane regains its full height with no visual gap.

struct MaterialsBulkActionBar: View {
    @ObservedObject var selection: MaterialsSelection
    let onDelete: (Set<String>) -> Void
    let onMove: (Set<String>) -> Void
    let onDownload: (Set<String>) -> Void
    let onPreview: (Set<String>) -> Void

    var body: some View {
        if selection.selected.isEmpty {
            EmptyView()
        } else {
            HStack(spacing: AF.Space.s) {
                Text("\(selection.selected.count) selected")
                    .font(.callout.weight(.semibold))

                Spacer()

                Button("Preview") { onPreview(selection.selected) }
                    .buttonStyle(.bordered)

                Button("Download") { onDownload(selection.selected) }
                    .buttonStyle(.bordered)

                Button("Move…") { onMove(selection.selected) }
                    .buttonStyle(.bordered)

                Button(role: .destructive) {
                    onDelete(selection.selected)
                } label: {
                    Text("Delete")
                }
                .buttonStyle(.bordered)
                .tint(.red)

                Button {
                    selection.clear()
                } label: {
                    Image(systemName: "xmark")
                        .imageScale(.small)
                }
                .buttonStyle(.borderless)
                .help("Clear selection")
            }
            .padding(.horizontal, AF.Space.m)
            .padding(.vertical, AF.Space.s)
            .background(.bar)
        }
    }
}

#Preview {
    let s = MaterialsSelection()
    s.selectAll(["a.pdf", "b.pdf", "c.docx"])
    return MaterialsBulkActionBar(
        selection: s,
        onDelete: { _ in },
        onMove: { _ in },
        onDownload: { _ in },
        onPreview: { _ in }
    )
    .frame(width: 600)
}
