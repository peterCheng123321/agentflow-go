import SwiftUI

// MARK: - Design tokens
//
// Centralised tokens for spacing, radius, and colour. Views should consume
// these rather than hard-coding values — this keeps the app aligned with the
// macOS 26 HIG 8pt grid and lets the theme evolve in one place.

enum AF {

    // 8pt grid spacing scale (plus 4pt for very tight gaps).
    enum Space {
        static let xxs: CGFloat = 4
        static let xs:  CGFloat = 8
        static let s:   CGFloat = 12
        static let m:   CGFloat = 16
        static let l:   CGFloat = 24
        static let xl:  CGFloat = 32
        static let xxl: CGFloat = 48
    }

    // Corner radii. Matches Apple's own concentric-corner guidance.
    enum Radius {
        static let s:  CGFloat = 6
        static let m:  CGFloat = 10
        static let l:  CGFloat = 14
        static let xl: CGFloat = 20
    }

    // System-semantic colours. No hard-coded RGB for chrome surfaces —
    // everything here tracks light/dark mode and accessibility contrast.
    enum Palette {
        static let accent         = Color.accentColor
        static let background     = Color(nsColor: .windowBackgroundColor)
        static let surface        = Color(nsColor: .controlBackgroundColor)
        static let separator      = Color(nsColor: .separatorColor)
        static let textPrimary    = Color(nsColor: .labelColor)
        static let textSecondary  = Color(nsColor: .secondaryLabelColor)

        /// Semantic colour for a workflow state. Delegates to `tint(_:)` via
        /// the state's accent mapping so there's a single source of truth
        /// for workflow colour decisions.
        static func state(_ state: WorkflowState) -> Color {
            tint(state.accent)
        }

        /// Legacy accent-token lookup kept so callers outside this file
        /// (SidebarView, CaseDetailView, etc.) keep compiling. New code
        /// should prefer `state(_:)` or the semantic tokens above.
        static func tint(_ accent: AFAccent) -> Color {
            switch accent {
            case .neutral: return Color(nsColor: .secondaryLabelColor)
            case .blue:    return Color(nsColor: .systemBlue)
            case .purple:  return Color(nsColor: .systemPurple)
            case .amber:   return Color(nsColor: .systemOrange)
            case .green:   return Color(nsColor: .systemGreen)
            case .gray:    return Color(nsColor: .systemGray)
            }
        }
    }
}

// MARK: - Liquid Glass helpers
//
// Thin wrappers around the native `.glassEffect()` modifier so every surface
// picks up the same radius + tint treatment. Do NOT reintroduce hand-rolled
// `.ultraThinMaterial + overlay + shadow` stacks — that's what these replace.

extension View {
    /// Wraps content in a Liquid Glass card: padding is the caller's
    /// responsibility, the modifier only applies the surface treatment.
    func afGlassCard(radius: CGFloat = AF.Radius.l) -> some View {
        self.glassEffect(
            .regular,
            in: RoundedRectangle(cornerRadius: radius, style: .continuous)
        )
    }

    /// Subtler Liquid Glass panel for inline/secondary surfaces (toolbars,
    /// chips, side rails). Uses the smaller `.m` radius by default.
    func afGlassPanel() -> some View {
        self.glassEffect(
            .regular,
            in: RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
        )
    }
}

// MARK: - Glass card (legacy container)

/// Kept for source compatibility — existing call sites use `GlassCard { ... }`.
/// Internally routes through `afGlassCard` so the visual treatment matches
/// the rest of the system.
struct GlassCard<Content: View>: View {
    var padding: CGFloat = AF.Space.m
    var radius: CGFloat = AF.Radius.l
    @ViewBuilder var content: () -> Content

    var body: some View {
        content()
            .padding(padding)
            .afGlassCard(radius: radius)
    }
}

// MARK: - Section header

/// Sentence-case section label. No ALL CAPS, no letter-spacing — just a
/// quiet secondary caption, per HIG.
struct SectionHeader: View {
    let title: String
    var subtitle: String? = nil

    var body: some View {
        VStack(alignment: .leading, spacing: AF.Space.xxs) {
            Text(title)
                .font(.caption.weight(.medium))
                .foregroundStyle(.secondary)
            if let subtitle {
                Text(subtitle)
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
        }
    }
}

// MARK: - State pill

struct StatePill: View {
    let state: String

    var body: some View {
        let ws = WorkflowState(rawValue: state)
        let tint = ws.map(AF.Palette.state) ?? AF.Palette.textSecondary
        Text(ws?.pretty ?? state)
            .font(.caption.weight(.semibold))
            .padding(.horizontal, AF.Space.s)
            .padding(.vertical, AF.Space.xxs)
            .foregroundStyle(tint)
            .background(
                Capsule(style: .continuous).fill(tint.opacity(0.15))
            )
    }
}

// MARK: - Button styles

/// Prominent call-to-action filled with the system accent colour.
struct AFPrimaryButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.callout.weight(.semibold))
            .padding(.horizontal, AF.Space.m)
            .padding(.vertical, AF.Space.xs)
            .foregroundStyle(Color.white)
            .background(
                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                    .fill(Color.accentColor)
            )
            .opacity(configuration.isPressed ? 0.8 : 1)
    }
}

/// Secondary action rendered on a Liquid Glass panel.
struct AFGhostButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.callout.weight(.medium))
            .padding(.horizontal, AF.Space.s)
            .padding(.vertical, AF.Space.xs)
            .foregroundStyle(.primary)
            .afGlassPanel()
            .opacity(configuration.isPressed ? 0.75 : 1)
    }
}

extension ButtonStyle where Self == AFPrimaryButtonStyle {
    static var afPrimary: AFPrimaryButtonStyle { .init() }
}
extension ButtonStyle where Self == AFGhostButtonStyle {
    static var afGhost: AFGhostButtonStyle { .init() }
}

// MARK: - Ambient background

/// Window background. Uses the system window colour so light/dark mode,
/// increased contrast, and tinted appearances all behave correctly.
struct AmbientBackground: View {
    var body: some View {
        AF.Palette.background
            .ignoresSafeArea()
    }
}

// MARK: - Meta item

struct MetaItem: View {
    let icon: String
    let label: String
    let value: String

    var body: some View {
        HStack(spacing: AF.Space.xs) {
            Image(systemName: icon)
                .font(.caption)
                .foregroundStyle(.secondary)
            VStack(alignment: .leading, spacing: 2) {
                Text(label)
                    .font(.caption.weight(.medium))
                    .foregroundStyle(.secondary)
                Text(value)
                    .font(.callout)
            }
        }
    }
}

// MARK: - Empty state

struct EmptyStateView: View {
    let icon: String
    let title: String
    let subtitle: String

    var body: some View {
        VStack(spacing: AF.Space.s) {
            Image(systemName: icon)
                .font(.system(size: 40, weight: .regular))
                .foregroundStyle(.secondary)
                .padding(.bottom, AF.Space.xs)
            Text(title)
                .font(.headline)
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
