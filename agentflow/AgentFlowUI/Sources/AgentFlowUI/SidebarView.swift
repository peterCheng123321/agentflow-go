import SwiftUI

struct SidebarView: View {
    @EnvironmentObject var api: APIClient
    @EnvironmentObject var backend: BackendManager
    @Binding var cases: [Case]
    @Binding var selection: String?
    @Binding var search: String
    let onRefresh: () async -> Void
    let onNew: () -> Void

    var filtered: [Case] {
        let q = search.trimmingCharacters(in: .whitespaces).lowercased()
        guard !q.isEmpty else { return cases }
        return cases.filter {
            $0.client_name.lowercased().contains(q) ||
            $0.matter_type.lowercased().contains(q) ||
            $0.case_id.lowercased().contains(q) ||
            $0.state.lowercased().contains(q)
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            // Brand
            HStack(spacing: 10) {
                ZStack {
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .fill(LinearGradient(colors: [
                            Color(red: 0.35, green: 0.5, blue: 1.0),
                            Color(red: 0.72, green: 0.38, blue: 1.0)
                        ], startPoint: .topLeading, endPoint: .bottomTrailing))
                        .frame(width: 32, height: 32)
                    Image(systemName: "scale.3d")
                        .font(.system(size: 14, weight: .bold))
                        .foregroundStyle(.white)
                }
                VStack(alignment: .leading, spacing: 1) {
                    Text("AgentFlow").font(.headline)
                    Text(backendLabel)
                        .font(.caption2.weight(.medium))
                        .foregroundStyle(backendColor)
                }
                Spacer()
            }
            .padding(.horizontal, AF.Space.m)
            .padding(.top, AF.Space.m)
            .padding(.bottom, AF.Space.s)

            // Search
            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass")
                    .foregroundStyle(.secondary)
                TextField("Search cases…", text: $search)
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

            // Section header
            HStack {
                SectionHeader(title: "Active cases")
                Spacer()
                Text("\(filtered.count)")
                    .font(.caption2.monospacedDigit())
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, AF.Space.m)
            .padding(.top, AF.Space.m)
            .padding(.bottom, 6)

            // List
            ScrollView {
                LazyVStack(spacing: 4) {
                    if filtered.isEmpty {
                        Text(search.isEmpty ? "No cases yet" : "No matches")
                            .font(.callout)
                            .foregroundStyle(.secondary)
                            .padding(.vertical, 20)
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

            // Footer
            VStack(spacing: AF.Space.s) {
                Button(action: onNew) {
                    HStack {
                        Image(systemName: "plus.circle.fill")
                        Text("New case").frame(maxWidth: .infinity)
                    }
                }
                .buttonStyle(.afPrimary)

                Button {
                    Task { await onRefresh() }
                } label: {
                    HStack {
                        Image(systemName: "arrow.clockwise")
                        Text("Refresh").frame(maxWidth: .infinity)
                    }
                }
                .buttonStyle(.afGhost)
            }
            .padding(AF.Space.m)
        }
        .frame(width: 250)
        .background(
            Rectangle()
                .fill(.ultraThinMaterial)
                .overlay(
                    Rectangle()
                        .fill(LinearGradient(colors: [
                            .black.opacity(0.35), .black.opacity(0.15)
                        ], startPoint: .top, endPoint: .bottom))
                )
        )
        .overlay(alignment: .trailing) {
            Rectangle().fill(Color.white.opacity(0.06)).frame(width: 1)
        }
    }

    private var backendLabel: String {
        switch backend.status {
        case .starting: return "Starting…"
        case .running:  return "Connected"
        case .failed:   return "Backend offline"
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
    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Circle()
                .fill(AF.Palette.tint(WorkflowState(rawValue: `case`.state)?.accent ?? .neutral))
                .frame(width: 8, height: 8)
                .padding(.top, 6)
            VStack(alignment: .leading, spacing: 3) {
                Text(`case`.displayName)
                    .font(.callout.weight(.semibold))
                    .lineLimit(1)
                Text(`case`.matter_type)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                HStack(spacing: 6) {
                    Text(WorkflowState(rawValue: `case`.state)?.pretty ?? `case`.state)
                        .font(.caption2.weight(.medium))
                        .foregroundStyle(.secondary)
                    if `case`.docCount > 0 {
                        Label("\(`case`.docCount)", systemImage: "doc")
                            .font(.caption2)
                            .foregroundStyle(.tertiary)
                    }
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 9)
        .background(
            RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                .fill(selected ? Color.accentColor.opacity(0.18) : Color.clear)
        )
        .overlay(
            RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                .strokeBorder(selected ? Color.accentColor.opacity(0.35) : .clear, lineWidth: 1)
        )
        .contentShape(Rectangle())
    }
}
