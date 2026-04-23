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

// MARK: - Model picker as full sheet (alternative to inline menu)

struct ModelPickerSheet: View {
    @EnvironmentObject var api: APIClient
    @EnvironmentObject var ai: AIController
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: AF.Space.m) {
            HStack {
                Label("Select model", systemImage: "cpu").font(.headline)
                Spacer()
                Button { dismiss() } label: {
                    Image(systemName: "xmark.circle.fill")
                }
                .buttonStyle(.plain)
            }

            ScrollView {
                VStack(spacing: AF.Space.s) {
                    ForEach(ai.models) { m in
                        Button {
                            ai.selectedModelID = m.id
                            dismiss()
                        } label: {
                            HStack(alignment: .top, spacing: 12) {
                                Image(systemName: m.id == ai.selectedModelID ? "largecircle.fill.circle" : "circle")
                                    .foregroundStyle(m.id == ai.selectedModelID ? AF.Palette.tint(.blue) : .secondary)
                                    .padding(.top, 2)
                                VStack(alignment: .leading, spacing: 3) {
                                    Text(m.name).font(.callout.weight(.semibold))
                                    if let d = m.description, !d.isEmpty {
                                        Text(d).font(.caption).foregroundStyle(.secondary).lineLimit(3)
                                    }
                                    if let b = m.backend {
                                        Text(b.uppercased()).font(.caption2.weight(.semibold)).foregroundStyle(.tertiary)
                                    }
                                }
                                Spacer(minLength: 0)
                            }
                            .padding(10)
                            .background(
                                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                                    .fill(m.id == ai.selectedModelID
                                          ? AF.Palette.tint(.blue).opacity(0.12)
                                          : Color.white.opacity(0.04))
                            )
                        }
                        .buttonStyle(.plain)
                    }
                }
            }
        }
        .padding(AF.Space.l)
        .frame(minWidth: 520, minHeight: 420)
        .task { await ai.loadModelsIfNeeded(api: api) }
    }
}
