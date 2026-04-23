import SwiftUI

// MARK: - Theme

enum AF {
    enum Radius {
        static let s: CGFloat = 8
        static let m: CGFloat = 12
        static let l: CGFloat = 18
        static let xl: CGFloat = 28
    }
    enum Space {
        static let xs: CGFloat = 6
        static let s: CGFloat = 10
        static let m: CGFloat = 16
        static let l: CGFloat = 24
        static let xl: CGFloat = 36
    }
    enum Palette {
        static func tint(_ a: AFAccent) -> Color {
            switch a {
            case .neutral: return .secondary
            case .blue:    return Color(red: 0.25, green: 0.55, blue: 1.0)
            case .purple:  return Color(red: 0.62, green: 0.35, blue: 0.98)
            case .amber:   return Color(red: 1.0, green: 0.70, blue: 0.22)
            case .green:   return Color(red: 0.20, green: 0.80, blue: 0.55)
            case .gray:    return Color.gray
            }
        }
    }
}

// MARK: - Glass card

struct GlassCard<Content: View>: View {
    var padding: CGFloat = AF.Space.m
    var radius: CGFloat = AF.Radius.l
    @ViewBuilder var content: () -> Content

    var body: some View {
        content()
            .padding(padding)
            .background(
                RoundedRectangle(cornerRadius: radius, style: .continuous)
                    .fill(.ultraThinMaterial)
            )
            .overlay(
                RoundedRectangle(cornerRadius: radius, style: .continuous)
                    .strokeBorder(Color.white.opacity(0.08), lineWidth: 1)
            )
            .shadow(color: .black.opacity(0.18), radius: 18, x: 0, y: 6)
    }
}

// MARK: - Section header

struct SectionHeader: View {
    let title: String
    var subtitle: String? = nil
    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(title)
                .font(.system(size: 11, weight: .semibold))
                .tracking(0.8)
                .foregroundStyle(.secondary)
                .textCase(.uppercase)
            if let s = subtitle {
                Text(s).font(.callout).foregroundStyle(.secondary)
            }
        }
    }
}

// MARK: - State pill

struct StatePill: View {
    let state: String
    var body: some View {
        let ws = WorkflowState(rawValue: state)
        let tint = AF.Palette.tint(ws?.accent ?? .neutral)
        Text(ws?.pretty ?? state)
            .font(.caption.weight(.semibold))
            .padding(.horizontal, 10)
            .padding(.vertical, 5)
            .background(
                Capsule().fill(tint.opacity(0.18))
            )
            .overlay(
                Capsule().strokeBorder(tint.opacity(0.45), lineWidth: 0.8)
            )
            .foregroundStyle(tint)
    }
}

// MARK: - Primary / secondary button style

struct AFPrimaryButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.callout.weight(.semibold))
            .padding(.horizontal, 14)
            .padding(.vertical, 9)
            .background(
                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                    .fill(LinearGradient(
                        colors: [Color.accentColor, Color.accentColor.opacity(0.85)],
                        startPoint: .top, endPoint: .bottom))
            )
            .foregroundStyle(.white)
            .opacity(configuration.isPressed ? 0.75 : 1)
            .scaleEffect(configuration.isPressed ? 0.98 : 1)
            .shadow(color: Color.accentColor.opacity(0.35), radius: 10, y: 3)
    }
}

struct AFGhostButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.callout.weight(.medium))
            .padding(.horizontal, 12)
            .padding(.vertical, 7)
            .background(
                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                    .fill(.ultraThinMaterial)
            )
            .overlay(
                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                    .strokeBorder(.white.opacity(0.08), lineWidth: 1)
            )
            .opacity(configuration.isPressed ? 0.75 : 1)
            .scaleEffect(configuration.isPressed ? 0.98 : 1)
    }
}

extension ButtonStyle where Self == AFPrimaryButtonStyle { static var afPrimary: AFPrimaryButtonStyle { .init() } }
extension ButtonStyle where Self == AFGhostButtonStyle { static var afGhost: AFGhostButtonStyle { .init() } }

// MARK: - Ambient background

struct AmbientBackground: View {
    var body: some View {
        ZStack {
            LinearGradient(colors: [
                Color(red: 0.06, green: 0.07, blue: 0.12),
                Color(red: 0.10, green: 0.10, blue: 0.18)
            ], startPoint: .top, endPoint: .bottom)
            .ignoresSafeArea()

            // Soft orbs
            GeometryReader { geo in
                Circle()
                    .fill(Color(red: 0.30, green: 0.45, blue: 1.0).opacity(0.22))
                    .frame(width: geo.size.width * 0.6, height: geo.size.width * 0.6)
                    .blur(radius: 110)
                    .offset(x: -geo.size.width * 0.15, y: -geo.size.height * 0.15)
                Circle()
                    .fill(Color(red: 0.75, green: 0.35, blue: 1.0).opacity(0.20))
                    .frame(width: geo.size.width * 0.55, height: geo.size.width * 0.55)
                    .blur(radius: 120)
                    .offset(x: geo.size.width * 0.55, y: geo.size.height * 0.50)
            }
            .ignoresSafeArea()
        }
    }
}

// MARK: - Empty state

struct EmptyStateView: View {
    let icon: String
    let title: String
    let subtitle: String
    var body: some View {
        VStack(spacing: AF.Space.m) {
            ZStack {
                Circle()
                    .fill(.ultraThinMaterial)
                    .frame(width: 88, height: 88)
                Image(systemName: icon)
                    .font(.system(size: 34, weight: .light))
                    .foregroundStyle(.secondary)
            }
            Text(title).font(.title3.weight(.semibold))
            Text(subtitle)
                .font(.callout)
                .multilineTextAlignment(.center)
                .foregroundStyle(.secondary)
                .frame(maxWidth: 360)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(AF.Space.l)
    }
}
