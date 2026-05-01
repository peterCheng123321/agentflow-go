import SwiftUI

struct SidebarView: View {
    @EnvironmentObject var api: APIClient
    @EnvironmentObject var backend: BackendManager
    @EnvironmentObject var router: AppRouter
    @Binding var cases: [Case]
    @Binding var selection: String?
    @Binding var search: String
    @Binding var matterFilter: MatterListFilter
    let onRefresh: () async -> Void
    let onNew: () -> Void
    let onOpenResearch: () -> Void

    private var pool: [Case] {
        switch matterFilter {
        case .all:         return cases
        case .needsAction: return cases.filter(\.needsLawyerAttention)
        }
    }

    private var filtered: [Case] {
        let q = search.trimmingCharacters(in: .whitespaces).lowercased()
        let base = pool.sorted { a, b in
            if a.needsLawyerAttention != b.needsLawyerAttention {
                return a.needsLawyerAttention && !b.needsLawyerAttention
            }
            return a.updated_at > b.updated_at
        }
        guard !q.isEmpty else { return base }
        return base.filter {
            $0.client_name.lowercased().contains(q) ||
            $0.matter_type.lowercased().contains(q) ||
            $0.case_id.lowercased().contains(q) ||
            $0.state.lowercased().contains(q)
        }
    }

    var body: some View {
        List(selection: $selection) {
            if filtered.isEmpty {
                Text(emptyListHint)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .frame(maxWidth: .infinity, alignment: .center)
                    .padding(.vertical, AF.Space.m)
                    .listRowSeparator(.hidden)
            } else {
                ForEach(filtered) { c in
                    SidebarRow(case: c)
                        .tag(Optional(c.case_id))
                }
            }
        }
        .listStyle(.sidebar)
        .navigationTitle("Matters")
        .searchable(text: $search, placement: .sidebar, prompt: "Search matters")
        .toolbar {
            ToolbarItem(placement: .automatic) {
                Menu {
                    Picker("Filter", selection: $matterFilter) {
                        ForEach(MatterListFilter.allCases) { f in
                            Text(f.rawValue).tag(f)
                        }
                    }
                } label: {
                    Label(matterFilter.rawValue, systemImage: "line.3.horizontal.decrease.circle")
                }
                .help("Filter matters")
            }
            ToolbarItem(placement: .automatic) {
                Button(action: onNew) {
                    Label("New matter", systemImage: "plus")
                }
                .help("New matter")
                .keyboardShortcut("n", modifiers: [.command])
            }
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            footerBar
        }
    }

    private var footerBar: some View {
        HStack(spacing: AF.Space.s) {
            Circle()
                .fill(backendColor)
                .frame(width: 8, height: 8)
            Text(backendLabel)
                .font(.caption)
                .foregroundStyle(.secondary)
            Spacer()
            Button { Task { await onRefresh() } } label: {
                Image(systemName: "arrow.clockwise")
            }
            .buttonStyle(.borderless)
            .help("Refresh")

            Button(action: onOpenResearch) {
                Image(systemName: "sparkles")
            }
            .buttonStyle(.borderless)
            .help("Open research for selected matter")
            .keyboardShortcut("l", modifiers: [.command, .option])
            .disabled(selection == nil)

            Button { router.open(.settings) } label: {
                Image(systemName: "gearshape")
            }
            .buttonStyle(.borderless)
            .help("Settings")
        }
        .padding(.horizontal, AF.Space.m)
        .padding(.vertical, AF.Space.xs)
        .background(.bar)
    }

    private var emptyListHint: String {
        if matterFilter == .needsAction {
            return search.isEmpty ? "Nothing needs your sign-off right now." : "No matches in this queue."
        }
        return search.isEmpty ? "No matters yet — add one from the toolbar." : "No matches."
    }

    private var backendLabel: String {
        switch backend.status {
        case .starting: return "Engine starting…"
        case .running:  return "Engine ready"
        case .failed:   return "Engine offline"
        }
    }
    private var backendColor: Color {
        switch backend.status {
        case .starting: return .orange
        case .running:  return .green
        case .failed:   return .red
        }
    }
}

struct SidebarRow: View {
    let `case`: Case

    private var accent: Color {
        AF.Palette.tint(WorkflowState(rawValue: `case`.state)?.accent ?? .neutral)
    }

    var body: some View {
        HStack(alignment: .center, spacing: AF.Space.s) {
            RoundedRectangle(cornerRadius: 2, style: .continuous)
                .fill(accent)
                .frame(width: 3, height: 32)

            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(`case`.displayName)
                        .font(.callout.weight(.semibold))
                        .foregroundStyle(.primary)
                        .lineLimit(1)
                    if `case`.needsLawyerAttention {
                        Image(systemName: "exclamationmark.circle.fill")
                            .font(.caption2)
                            .foregroundStyle(.orange)
                            .help("Needs approval or review")
                    }
                }
                Text(`case`.matter_type)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                HStack(spacing: 6) {
                    Text(WorkflowState(rawValue: `case`.state)?.pretty ?? `case`.state)
                        .font(.caption2.weight(.medium))
                        .foregroundStyle(accent)
                    if `case`.docCount > 0 {
                        Label("\(`case`.docCount)", systemImage: "doc")
                            .font(.caption2)
                            .foregroundStyle(.secondary)
                    }
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.vertical, 4)
        .contentShape(Rectangle())
    }
}
