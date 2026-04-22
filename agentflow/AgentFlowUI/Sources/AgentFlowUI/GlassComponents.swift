import SwiftUI

// MARK: - Design tokens

enum AF {
    enum Color {
        static let accent        = SwiftUI.Color(red: 0.337, green: 0.337, blue: 0.898) // #5656E6
        static let accentGreen   = SwiftUI.Color(red: 0.188, green: 0.820, blue: 0.345)
        static let accentAmber   = SwiftUI.Color(red: 1.0,   green: 0.623, blue: 0.039)
        static let accentRed     = SwiftUI.Color(red: 1.0,   green: 0.271, blue: 0.227)
        static let textPrimary   = SwiftUI.Color.primary
        static let textSecondary = SwiftUI.Color.secondary
        static let glassStroke   = SwiftUI.Color.white.opacity(0.12)
        static let glassShadow   = SwiftUI.Color.black.opacity(0.06)

        // Sidebar palette
        static let sidebarBg         = SwiftUI.Color(red: 0.11, green: 0.11, blue: 0.18)
        static let sidebarText       = SwiftUI.Color.white.opacity(0.85)
        static let sidebarTextSub    = SwiftUI.Color.white.opacity(0.45)
        static let sidebarSelected   = SwiftUI.Color(red: 0.35, green: 0.35, blue: 0.90).opacity(0.25)
        static let sidebarDivider    = SwiftUI.Color.white.opacity(0.08)

        // Column backgrounds
        static let contentBg = SwiftUI.Color(NSColor.controlBackgroundColor)
        static let detailBg  = SwiftUI.Color(NSColor.windowBackgroundColor)
    }
    enum Radius {
        static let card:   CGFloat = 10
        static let button: CGFloat = 8
        static let chip:   CGFloat = 6
    }
    enum Spacing {
        static let xs: CGFloat = 4
        static let sm: CGFloat = 8
        static let md: CGFloat = 14
        static let lg: CGFloat = 20
        static let xl: CGFloat = 28
    }
}

// MARK: - Card modifier (Liquid Glass)

struct CardModifier: ViewModifier {
    var cornerRadius: CGFloat = AF.Radius.card
    var padding: CGFloat = AF.Spacing.md

    func body(content: Content) -> some View {
        content
            .padding(padding)
            .glassEffect(in: RoundedRectangle(cornerRadius: cornerRadius, style: .continuous))
    }
}

extension View {
    func glassCard(radius: CGFloat = AF.Radius.card, padding: CGFloat = AF.Spacing.md) -> some View {
        modifier(CardModifier(cornerRadius: radius, padding: padding))
    }
}

// MARK: - Primary action button

struct AFButton: View {
    var title: String
    var icon: String? = nil
    var style: Style = .primary
    var isLoading: Bool = false
    var action: () -> Void

    enum Style { case primary, secondary, destructive, ghost }

    private var fgColor: Color {
        switch style {
        case .primary, .destructive: return .white
        case .secondary, .ghost:     return AF.Color.accent
        }
    }

    var body: some View {
        Button(action: action) {
            HStack(spacing: title.isEmpty ? 0 : 5) {
                if isLoading {
                    ProgressView()
                        .scaleEffect(0.7)
                        .tint(style == .primary ? .white : AF.Color.accent)
                } else if let icon {
                    Image(systemName: icon)
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundStyle(fgColor)
                }
                if !title.isEmpty {
                    Text(title)
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundStyle(fgColor)
                }
            }
            .padding(.horizontal, title.isEmpty ? AF.Spacing.sm : AF.Spacing.md)
            .padding(.vertical, AF.Spacing.sm)
            .glassEffect(
                glassEffectForStyle(),
                in: RoundedRectangle(cornerRadius: AF.Radius.button, style: .continuous)
            )
        }
        .buttonStyle(.plain)
        .disabled(isLoading)
    }

    private func glassEffectForStyle() -> Glass {
        switch style {
        case .primary:             return Glass.regular.interactive().tint(AF.Color.accent.opacity(0.3))
        case .destructive:         return Glass.regular.interactive().tint(AF.Color.accentRed.opacity(0.3))
        case .secondary, .ghost:   return Glass.regular.interactive()
        }
    }
}

// MARK: - State badge

struct StateBadge: View {
    var state: String
    var compact: Bool = false

    private var color: Color {
        switch state.lowercased() {
        case let s where s.contains("archive"): return .gray
        case let s where s.contains("eval"):    return AF.Color.accentAmber
        case let s where s.contains("doc"):     return AF.Color.accent
        case let s where s.contains("fee"):     return AF.Color.accentGreen
        case let s where s.contains("close"):   return .gray
        case let s where s.contains("capture"): return AF.Color.accent.opacity(0.7)
        default:                                 return AF.Color.accent.opacity(0.7)
        }
    }

    private var label: String {
        if compact {
            return (state.split(separator: "_").last.map(String.init) ?? state).capitalized
        }
        return state.replacingOccurrences(of: "_", with: " ").capitalized
    }

    var body: some View {
        Text(label)
            .font(.system(size: 10, weight: .semibold, design: .rounded))
            .foregroundStyle(color)
            .lineLimit(1)
            .padding(.horizontal, 7)
            .padding(.vertical, 3)
            .glassEffect(
                Glass.regular.tint(color.opacity(0.2)),
                in: Capsule()
            )
            .fixedSize(horizontal: true, vertical: false)
    }
}

// MARK: - Section header

struct SectionHeader<Trailing: View>: View {
    var title: String
    var trailing: Trailing

    init(title: String, @ViewBuilder trailing: () -> Trailing) {
        self.title = title
        self.trailing = trailing()
    }

    var body: some View {
        HStack(alignment: .center) {
            Text(title.uppercased())
                .font(.system(size: 11, weight: .semibold, design: .rounded))
                .foregroundStyle(.secondary)
                .tracking(0.8)
            Spacer()
            trailing
        }
    }
}

extension SectionHeader where Trailing == EmptyView {
    init(title: String) {
        self.title = title
        self.trailing = EmptyView()
    }
}

// MARK: - Progress ring

struct ProgressRing: View {
    var progress: Double
    var size: CGFloat = 28
    var color: Color = AF.Color.accent

    var body: some View {
        ZStack {
            Circle()
                .stroke(color.opacity(0.2), lineWidth: 3)
            Circle()
                .trim(from: 0, to: progress)
                .stroke(color, style: StrokeStyle(lineWidth: 3, lineCap: .round))
                .rotationEffect(.degrees(-90))
                .animation(.easeInOut(duration: 0.3), value: progress)
            if progress >= 1 {
                Image(systemName: "checkmark")
                    .font(.system(size: 9, weight: .bold))
                    .foregroundStyle(color)
            }
        }
        .frame(width: size, height: size)
    }
}

// MARK: - Empty state

struct EmptyStateView: View {
    var icon: String
    var title: String
    var subtitle: String

    var body: some View {
        VStack(spacing: AF.Spacing.md) {
            Image(systemName: icon)
                .font(.system(size: 40, weight: .ultraLight))
                .foregroundStyle(AF.Color.accent.opacity(0.5))
                .padding(AF.Spacing.lg)
                .glassEffect(in: Circle())
            VStack(spacing: 4) {
                Text(title)
                    .font(.headline)
                    .foregroundStyle(.primary)
                Text(subtitle)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(AF.Spacing.xl)
    }
}

// MARK: - Typing indicator

struct TypingIndicator: View {
    @State private var phase = 0

    var body: some View {
        HStack(spacing: 4) {
            ForEach(0..<3, id: \.self) { i in
                Circle()
                    .fill(AF.Color.accent.opacity(0.8))
                    .frame(width: 7, height: 7)
                    .scaleEffect(phase == i ? 1.3 : 0.8)
                    .animation(.easeInOut(duration: 0.4).repeatForever().delay(Double(i) * 0.15), value: phase)
            }
        }
        .onAppear { phase = 0; withAnimation { phase = 2 } }
    }
}

// MARK: - Gradient accent line

struct AccentLine: View {
    var body: some View {
        Rectangle()
            .fill(LinearGradient(
                colors: [AF.Color.accent, AF.Color.accent.opacity(0)],
                startPoint: .leading, endPoint: .trailing
            ))
            .frame(height: 1)
    }
}

// MARK: - Connection dot

struct ConnectionDot: View {
    var connected: Bool

    var body: some View {
        HStack(spacing: 5) {
            Circle()
                .fill(connected ? AF.Color.accentGreen : AF.Color.accentRed)
                .frame(width: 7, height: 7)
                .shadow(color: (connected ? AF.Color.accentGreen : AF.Color.accentRed).opacity(0.6), radius: 4)
            Text(connected ? "Live" : "Offline")
                .font(.system(size: 11, weight: .medium))
                .foregroundStyle(connected ? AF.Color.accentGreen : AF.Color.accentRed)
        }
    }
}
