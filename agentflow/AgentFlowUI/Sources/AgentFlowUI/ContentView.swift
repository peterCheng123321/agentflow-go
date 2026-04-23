import SwiftUI

struct ContentView: View {
    @EnvironmentObject var api: APIClient
    @EnvironmentObject var backend: BackendManager
    @EnvironmentObject var ai: AIController
    @EnvironmentObject var router: AppRouter

    @State private var cases: [Case] = []
    @State private var selection: String?
    @State private var search: String = ""
    @State private var refreshTick = 0

    var body: some View {
        HStack(spacing: 0) {
            SidebarView(
                cases: $cases,
                selection: $selection,
                search: $search,
                onRefresh: { await loadCases() },
                onNew: { router.open(.newCase) }
            )
            .overlay(alignment: .bottom) {
                HStack(spacing: 8) {
                    Button { router.open(.settings) } label: {
                        Image(systemName: "gear")
                    }
                    .buttonStyle(.afGhost)
                    .help("Settings")

                    Button { router.toggleInspector() } label: {
                        Image(systemName: router.inspectorOpen ? "sidebar.trailing" : "sparkles")
                            .foregroundStyle(router.inspectorOpen ? .primary : AF.Palette.tint(.purple))
                    }
                    .buttonStyle(.afGhost)
                    .help(router.inspectorOpen ? "Hide AI (⌘⌥I)" : "Show AI (⌘⌥I)")
                    .keyboardShortcut("i", modifiers: [.command, .option])

                    Spacer()
                    Circle()
                        .fill(statusColor)
                        .frame(width: 7, height: 7)
                    Text(statusLabel)
                        .font(.caption2).foregroundStyle(.secondary)
                }
                .padding(.horizontal, AF.Space.m)
                .padding(.vertical, AF.Space.s)
            }

            Divider().opacity(0.2)

            Group {
                if let sel = selection, let c = cases.first(where: { $0.case_id == sel }) {
                    CaseDetailView(caseID: c.case_id, onChanged: { await loadCases() })
                        .id(c.case_id)
                } else {
                    EmptyStateView(
                        icon: "folder",
                        title: "Select a case",
                        subtitle: "Pick a case from the sidebar, or click + to create one."
                    )
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)

            if router.inspectorOpen {
                Divider().opacity(0.2)
                AIInspector(caseID: selection)
                    .frame(width: 340)
            }
        }
        .background(AmbientBackground())
        .task { await loadCases() }
        .onChange(of: selection) { _, new in ai.bind(toCase: new) }
        .onReceive(Timer.publish(every: 6, on: .main, in: .common).autoconnect()) { _ in
            Task { await loadCases() }
        }
        // Single hoisted sheet — any child calls `router.open(...)`.
        .sheet(item: $router.sheet) { sheet in
            switch sheet {
            case .document(let name, let caseID):
                DocumentViewer(filename: name, caseID: caseID,
                               onDeleted: { await loadCases() })
                    .environmentObject(api)
            case .settings:
                SettingsSheet()
                    .environmentObject(backend)
            case .newCase:
                NewCaseSheet { await loadCases() }
                    .environmentObject(api)
            case .modelPicker:
                ModelPickerSheet()
                    .environmentObject(api)
                    .environmentObject(ai)
            }
        }
    }

    private var statusLabel: String {
        switch backend.status {
        case .starting: return "Starting…"
        case .running:  return "Online"
        case .failed:   return "Offline"
        }
    }
    private var statusColor: Color {
        switch backend.status {
        case .starting: return .orange
        case .running:  return .green
        case .failed:   return .red
        }
    }

    private func loadCases() async {
        do {
            cases = try await api.listCases()
            if selection == nil, let first = cases.first { selection = first.case_id }
        } catch {
            // swallow — backend may still be starting
        }
    }
}
