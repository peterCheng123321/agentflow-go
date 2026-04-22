import SwiftUI

enum NavItem: String, CaseIterable, Identifiable {
    case cases    = "Cases"
    case agent    = "Agent"
    case jobs     = "Jobs"
    case settings = "Settings"

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .cases:    return "folder.fill"
        case .agent:    return "sparkles"
        case .jobs:     return "square.stack.3d.up"
        case .settings: return "gear"
        }
    }
}

struct ContentView: View {
    @EnvironmentObject var api: APIClient
    @EnvironmentObject var backend: BackendManager
    @State private var selection: NavItem = .cases
    @State private var selectedCaseID: String? = nil

    var body: some View {
        HStack(spacing: 0) {
            // Column 1 — dark sidebar
            SidebarView(selection: $selection)
                .frame(width: 220)

            Divider()

            // Column 2 — case list (only for Cases tab)
            if selection == .cases {
                CaseListColumn(selectedCaseID: $selectedCaseID)
                    .frame(width: 300)
                Divider()
            }

            // Column 3 — detail / content area
            Group {
                switch selection {
                case .cases:
                    if let id = selectedCaseID {
                        CaseDetailView(caseID: id)
                            .id(id)
                    } else {
                        EmptyStateView(
                            icon: "folder.open",
                            title: "Select a Case",
                            subtitle: "Pick a case from the list to view details, documents, and AI analysis."
                        )
                    }
                case .agent:
                    AgentView()
                case .jobs:
                    JobsView()
                case .settings:
                    SettingsView()
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(AF.Color.detailBg)
        }
        .frame(minWidth: 900, minHeight: 600)
        .alert("Backend Error", isPresented: .constant(backend.startError != nil)) {
            Button("OK") { backend.startError = nil }
        } message: {
            Text(backend.startError ?? "")
        }
    }
}

// MARK: - Sidebar

struct SidebarView: View {
    @Binding var selection: NavItem
    @EnvironmentObject var api: APIClient

    var body: some View {
        VStack(spacing: 0) {
            // App header
            HStack(spacing: AF.Spacing.sm) {
                ZStack {
                    RoundedRectangle(cornerRadius: 8, style: .continuous)
                        .fill(AF.Color.accent)
                        .frame(width: 30, height: 30)
                    Image(systemName: "scalemass.fill")
                        .font(.system(size: 14, weight: .semibold))
                        .foregroundStyle(.white)
                }
                VStack(alignment: .leading, spacing: 1) {
                    Text("AgentFlow")
                        .font(.system(size: 14, weight: .bold))
                        .foregroundStyle(AF.Color.sidebarText)
                    Text("Legal AI Platform")
                        .font(.system(size: 10))
                        .foregroundStyle(AF.Color.sidebarTextSub)
                }
                Spacer()
            }
            .padding(.horizontal, AF.Spacing.md)
            .padding(.top, AF.Spacing.lg)
            .padding(.bottom, AF.Spacing.md)

            Rectangle()
                .fill(AF.Color.sidebarDivider)
                .frame(height: 1)
                .padding(.horizontal, AF.Spacing.md)

            // Nav items
            VStack(spacing: 2) {
                ForEach(NavItem.allCases) { item in
                    SidebarRow(item: item, isSelected: selection == item)
                        .onTapGesture {
                            withAnimation(.spring(duration: 0.2)) { selection = item }
                        }
                }
            }
            .padding(.horizontal, AF.Spacing.sm)
            .padding(.top, AF.Spacing.sm)

            Spacer()

            // Bottom status
            VStack(spacing: AF.Spacing.sm) {
                Rectangle()
                    .fill(AF.Color.sidebarDivider)
                    .frame(height: 1)

                HStack(spacing: AF.Spacing.sm) {
                    ConnectionDot(connected: api.connected)
                    Spacer()
                    if let rag = api.ragSummary {
                        HStack(spacing: 4) {
                            Image(systemName: "doc.text.magnifyingglass")
                                .font(.system(size: 10))
                                .foregroundStyle(AF.Color.sidebarTextSub)
                            Text("\(rag.documentCount) docs")
                                .font(.system(size: 10, weight: .medium))
                                .foregroundStyle(AF.Color.sidebarTextSub)
                        }
                    }
                }
                .padding(.horizontal, AF.Spacing.md)
                .padding(.bottom, AF.Spacing.md)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(AF.Color.sidebarBg)
    }
}

struct SidebarRow: View {
    var item: NavItem
    var isSelected: Bool

    var body: some View {
        HStack(spacing: 0) {
            RoundedRectangle(cornerRadius: 1.5, style: .continuous)
                .fill(isSelected ? AF.Color.accent : Color.clear)
                .frame(width: 3)

            HStack(spacing: AF.Spacing.sm) {
                Image(systemName: item.icon)
                    .font(.system(size: 13, weight: .medium))
                    .frame(width: 18)
                    .foregroundStyle(isSelected ? AF.Color.accent : AF.Color.sidebarTextSub)
                Text(item.rawValue)
                    .font(.system(size: 13, weight: isSelected ? .semibold : .regular))
                    .foregroundStyle(isSelected ? AF.Color.sidebarText : AF.Color.sidebarTextSub)
                Spacer()
            }
            .padding(.leading, AF.Spacing.sm)
            .padding(.trailing, AF.Spacing.sm)
            .padding(.vertical, 9)
            .background(
                isSelected ? AF.Color.sidebarSelected : Color.clear,
                in: RoundedRectangle(cornerRadius: AF.Radius.chip, style: .continuous)
            )
        }
        .contentShape(Rectangle())
        .accessibilityLabel(item.rawValue)
        .accessibilityAddTraits(isSelected ? [.isSelected] : [])
    }
}

// MARK: - Jobs View

struct JobsView: View {
    @EnvironmentObject var api: APIClient

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text("Background Jobs")
                    .font(.system(size: 15, weight: .bold))
                Spacer()
                Text("\(api.jobs.count) active")
                    .font(.system(size: 12))
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, AF.Spacing.md)
            .padding(.top, AF.Spacing.md)

            ZStack {
                if api.jobs.isEmpty {
                    EmptyStateView(icon: "square.stack.3d.up", title: "No Active Jobs", subtitle: "Upload documents or run the agent to see background tasks here.")
                } else {
                    ScrollView {
                        LazyVStack(spacing: AF.Spacing.sm) {
                            ForEach(api.jobs.sorted(by: { $0.createdAt > $1.createdAt })) { job in
                                JobRow(job: job)
                            }
                        }
                        .padding(AF.Spacing.md)
                    }
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
    }
}

struct JobRow: View {
    var job: AFJob

    private var statusColor: Color {
        switch job.status {
        case "completed":  return AF.Color.accentGreen
        case "failed":     return AF.Color.accentRed
        case "processing": return AF.Color.accent
        default:           return .secondary
        }
    }

    var body: some View {
        HStack(spacing: AF.Spacing.md) {
            ProgressRing(progress: Double(job.progress) / 100.0, size: 36, color: statusColor)

            VStack(alignment: .leading, spacing: 3) {
                Text(job.type.replacingOccurrences(of: "_", with: " ").capitalized)
                    .font(.system(size: 13, weight: .semibold))
                Text(job.error.isEmpty ? job.status : "Failed: \(job.error)")
                    .font(.system(size: 11))
                    .foregroundStyle(job.error.isEmpty ? .secondary : AF.Color.accentRed)
                    .lineLimit(1)
            }

            Spacer()

            Text("\(job.progress)%")
                .font(.system(size: 12, weight: .medium, design: .monospaced))
                .foregroundStyle(statusColor)
        }
        .glassCard(padding: AF.Spacing.md)
        .accessibilityLabel("\(job.type) job, \(job.status), \(job.progress) percent complete")
    }
}

// MARK: - Settings View

struct SettingsView: View {
    @EnvironmentObject var api: APIClient

    var body: some View {
        ScrollView {
            VStack(spacing: AF.Spacing.md) {
                Text("Settings")
                    .font(.system(size: 15, weight: .bold))
                    .frame(maxWidth: .infinity, alignment: .leading)
                VStack(alignment: .leading, spacing: AF.Spacing.sm) {
                    SectionHeader(title: "Connection")
                    HStack {
                        Text("Backend URL")
                            .font(.system(size: 13))
                            .foregroundStyle(.secondary)
                        Spacer()
                        Text(api.baseURL)
                            .font(.system(size: 12, design: .monospaced))
                            .foregroundStyle(.primary)
                    }
                    HStack {
                        Text("Status")
                            .font(.system(size: 13))
                            .foregroundStyle(.secondary)
                        Spacer()
                        ConnectionDot(connected: api.connected)
                    }
                }
                .glassCard()

                if let rag = api.ragSummary {
                    VStack(alignment: .leading, spacing: AF.Spacing.sm) {
                        SectionHeader(title: "Knowledge Base")
                        InfoRow(label: "Documents", value: "\(rag.documentCount)")
                        InfoRow(label: "Chunks", value: "\(rag.totalChunks)")
                        InfoRow(label: "Backend", value: rag.backendMode.uppercased())
                    }
                    .glassCard()
                }

                VStack(alignment: .leading, spacing: AF.Spacing.sm) {
                    SectionHeader(title: "System")
                    InfoRow(label: "Platform", value: "macOS \(ProcessInfo.processInfo.operatingSystemVersionString)")
                    InfoRow(label: "App Version", value: Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "dev")
                }
                .glassCard()
            }
            .padding(AF.Spacing.md)
        }
    }
}

struct InfoRow: View {
    var label: String
    var value: String

    var body: some View {
        HStack {
            Text(label).font(.system(size: 13)).foregroundStyle(.secondary)
            Spacer()
            Text(value).font(.system(size: 13, weight: .medium)).foregroundStyle(.primary)
        }
    }
}
