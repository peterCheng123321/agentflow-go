import SwiftUI

enum MatterListFilter: String, CaseIterable, Identifiable {
    case all = "All matters"
    case needsAction = "Needs you"
    var id: String { rawValue }
}

struct ContentView: View {
    @EnvironmentObject var api: APIClient
    @EnvironmentObject var backend: BackendManager
    @EnvironmentObject var ai: AIController
    @EnvironmentObject var router: AppRouter

    @State private var cases: [Case] = []
    @State private var selection: String?
    @State private var search: String = ""
    @State private var matterFilter: MatterListFilter = .all

    var body: some View {
        NavigationSplitView {
            SidebarView(
                cases: $cases,
                selection: $selection,
                search: $search,
                matterFilter: $matterFilter,
                onRefresh: { await loadCases() },
                onNew: { router.open(.newCase) },
                onOpenResearch: {
                    if let s = selection {
                        router.focusResearch(forCase: s)
                    }
                }
            )
            .navigationSplitViewColumnWidth(min: 260, ideal: 288, max: 360)
        } detail: {
            Group {
                if let sel = selection, let c = cases.first(where: { $0.case_id == sel }) {
                    CaseHubView(caseID: c.case_id, onChanged: { await loadCases() })
                        .id(c.case_id)
                        .navigationTitle(c.displayName)
                        .navigationSubtitle(c.matter_type)
                } else {
                    EmptyStateView(
                        icon: "briefcase",
                        title: "No matter selected",
                        subtitle: "Choose a client file from the list, or create a new matter to begin."
                    )
                    .navigationTitle("AgentFlow")
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
        .task { await loadCases() }
        .onChange(of: selection) { _, new in
            ai.bind(toCase: new)
        }
        .onReceive(Timer.publish(every: 8, on: .main, in: .common).autoconnect()) { _ in
            Task { await loadCases() }
        }
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

    private func loadCases() async {
        do {
            cases = try await api.listCases()
            if selection == nil, let first = cases.first { selection = first.case_id }
            if let sel = selection, !cases.contains(where: { $0.case_id == sel }) {
                selection = cases.first?.case_id
            }
        } catch {
            // Backend may still be starting.
        }
    }
}
