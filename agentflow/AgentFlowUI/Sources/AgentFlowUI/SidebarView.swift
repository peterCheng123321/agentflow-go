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
        case .all:
            return cases
        case .needsAction:
            return cases.filter(\.needsLawyerAttention)
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
        VStack(spacing: 0) {
            HStack(spacing: 10) {
                ZStack {
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .fill(LinearGradient(colors: [
                            Color(red: 0.35, green: 0.5, blue: 1.0),
                            Color(red: 0.72, green: 0.38, blue: 1.0)
                        ], startPoint: .topLeading, endPoint: .bottomTrailing))
                        .frame(width: 32, height: 32)
                    Image(systemName: "briefcase.fill")
                        .font(.system(size: 14, weight: .bold))
                        .foregroundStyle(.white)
                }
                VStack(alignment: .leading, spacing: 1) {
                    Text("Matters").font(.headline)
                    Text(backendLabel)
                        .font(.caption2.weight(.medium))
                        .foregroundStyle(backendColor)
                }
                Spacer()
            }
            .padding(.horizontal, AF.Space.m)
            .padding(.top, AF.Space.m)
            .padding(.bottom, AF.Space.s)

            Picker("", selection: $matterFilter) {
                ForEach(MatterListFilter.allCases) { f in
                    Text(f.rawValue).tag(f)
                }
            }
            .pickerStyle(.segmented)
            .padding(.horizontal, AF.Space.m)
            .padding(.bottom, AF.Space.s)

            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(.secondary)
                TextField("Search…", text: $search)
                    .textFieldStyle(.plain)
                    .font(.callout)
            }
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .background(
                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                    .fill(.ultraThinMaterial)
            )
            .overlay(
                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                    .strokeBorder(.white.opacity(0.07), lineWidth: 1)
            )
            .padding(.horizontal, AF.Space.m)

            HStack {
                SectionHeader(title: matterFilter == .needsAction ? "Queue" : "Open matters")
                Spacer()
                Text("\(filtered.count)")
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, AF.Space.m)
            .padding(.top, AF.Space.m)
            .padding(.bottom, 6)

            ScrollView {
                LazyVStack(spacing: 4) {
                    if filtered.isEmpty {
                        Text(emptyListHint)
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .multilineTextAlignment(.center)
                            .padding(.vertical, 24)
                            .padding(.horizontal, AF.Space.m)
                    } else {
                        ForEach(filtered) { c in
                            SidebarRow(case: c, selected: selection == c.case_id)
                                .onTapGesture { selection = c.case_id }
                        }
                    }
                }
                .padding(.horizontal, 10)
            }

            Spacer(minLength: 0)

            VStack(spacing: AF.Space.s) {
                HStack(spacing: 8) {
                    Button { router.open(.settings) } label: {
                        Image(systemName: "gear")
                    }
                    .buttonStyle(.afGhost)
                    .help("Settings")

                    Button { Task { await onRefresh() } } label: {
                        Image(systemName: "arrow.clockwise")
                    }
                    .buttonStyle(.afGhost)
                    .help("Refresh")

                    Spacer()

                    Button(action: onOpenResearch) {
                        Image(systemName: "sparkles")
                            .foregroundStyle(AF.Palette.tint(.purple))
                    }
                    .buttonStyle(.afGhost)
                    .help("Open research for selected matter")
                    .keyboardShortcut("l", modifiers: [.command, .option])
                    .disabled(selection == nil)
                }

                Button(action: onNew) {
                    HStack {
                        Image(systemName: "plus.circle.fill")
                        Text("New matter").frame(maxWidth: .infinity)
                    }
                }
                .buttonStyle(.afPrimary)
            }
            .padding(AF.Space.m)
        }
        .background(
            ZStack {
                Color(red: 0.05, green: 0.03, blue: 0.10)
                Rectangle().fill(.ultraThinMaterial).opacity(0.55)
            }
        )
        .overlay(alignment: .trailing) {
            Rectangle().fill(Color.white.opacity(0.06)).frame(width: 1)
        }
    }

    private var emptyListHint: String {
        if matterFilter == .needsAction {
            return search.isEmpty ? "Nothing needs your sign-off right now." : "No matches in this queue."
        }
        return search.isEmpty ? "No matters yet — tap New matter." : "No matches."
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
    let selected: Bool

    private var accent: Color {
        AF.Palette.tint(WorkflowState(rawValue: `case`.state)?.accent ?? .neutral)
    }

    var body: some View {
        HStack(alignment: .center, spacing: 0) {
            RoundedRectangle(cornerRadius: 2, style: .continuous)
                .fill(accent)
                .frame(width: 3, height: 34)
                .padding(.leading, 8)
                .padding(.trailing, 10)

            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(`case`.displayName)
                        .font(.callout.weight(.semibold))
                        .lineLimit(1)
                    if `case`.needsLawyerAttention {
                        Image(systemName: "exclamationmark.circle.fill")
                            .font(.caption2)
                            .foregroundStyle(Color.orange.opacity(0.9))
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
                        .foregroundStyle(accent.opacity(0.85))
                    if `case`.docCount > 0 {
                        Label("\(`case`.docCount)", systemImage: "doc")
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.vertical, 9)
        .padding(.trailing, 10)
        .background(
            RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                .fill(selected ? Color.white.opacity(0.10) : Color.clear)
        )
        .overlay(
            RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                .strokeBorder(selected ? accent.opacity(0.40) : .clear, lineWidth: 1)
        )
        .contentShape(Rectangle())
    }
}
