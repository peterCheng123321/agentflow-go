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

    // Motion tokens. Curated curves rather than ad-hoc `.easeInOut`s
    // sprinkled across views — gives the app a single, recognisable feel.
    //
    // - `quick` for state flips and button feedback (~150ms)
    // - `smooth` for general view transitions (~300ms, cubic ease)
    // - `springSnappy` for short interactive nudges (selection, taps)
    // - `springFlow`  for longer cross-view morphs (sheets, sidebar→detail)
    enum Motion {
        static let quick:        Animation = .easeOut(duration: 0.15)
        static let smooth:       Animation = .smooth(duration: 0.32)
        static let springSnappy: Animation = .spring(response: 0.32, dampingFraction: 0.78)
        static let springFlow:   Animation = .spring(response: 0.55, dampingFraction: 0.86)
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

    /// Hover-lift treatment for a Liquid Glass card that responds to the
    /// pointer. Subtle vertical lift + accent-tinted shadow when hovered;
    /// returns to rest with `Motion.smooth`. Use on tappable surfaces
    /// (case rows, doc tiles) where a click target should feel alive.
    func afInteractiveCard() -> some View {
        modifier(InteractiveCardModifier())
    }
}

private struct InteractiveCardModifier: ViewModifier {
    @State private var isHovered = false

    func body(content: Content) -> some View {
        content
            .scaleEffect(isHovered ? 1.012 : 1.0)
            .shadow(
                color: Color.accentColor.opacity(isHovered ? 0.18 : 0),
                radius: isHovered ? 12 : 0,
                y: isHovered ? 4 : 0
            )
            .animation(AF.Motion.smooth, value: isHovered)
            .onHover { isHovered = $0 }
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
/// Press feedback: scales 0.97 + dims, snaps back via `Motion.springSnappy`.
/// Hover: elevates with a softened accent shadow so the click target reads
/// as "live" — works inside Liquid Glass without competing with the
/// material's own depth.
struct AFPrimaryButtonStyle: ButtonStyle {
    @State private var isHovered = false

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
            .shadow(
                color: Color.accentColor.opacity(isHovered ? 0.38 : 0.18),
                radius: isHovered ? 10 : 4,
                y: isHovered ? 4 : 1
            )
            .scaleEffect(configuration.isPressed ? 0.97 : (isHovered ? 1.015 : 1.0))
            .opacity(configuration.isPressed ? 0.85 : 1)
            .animation(AF.Motion.springSnappy, value: configuration.isPressed)
            .animation(AF.Motion.smooth, value: isHovered)
            .onHover { isHovered = $0 }
    }
}

/// Secondary action rendered on a Liquid Glass panel. Lighter feedback
/// than the primary — small scale + opacity dip on press, gentle glow
/// on hover so the surface feels live but doesn't shout.
struct AFGhostButtonStyle: ButtonStyle {
    @State private var isHovered = false

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.callout.weight(.medium))
            .padding(.horizontal, AF.Space.s)
            .padding(.vertical, AF.Space.xs)
            .foregroundStyle(.primary)
            .afGlassPanel()
            .overlay(
                RoundedRectangle(cornerRadius: AF.Radius.m, style: .continuous)
                    .stroke(Color.accentColor.opacity(isHovered ? 0.55 : 0), lineWidth: 1)
            )
            .scaleEffect(configuration.isPressed ? 0.96 : 1.0)
            .opacity(configuration.isPressed ? 0.78 : 1)
            .animation(AF.Motion.springSnappy, value: configuration.isPressed)
            .animation(AF.Motion.smooth, value: isHovered)
            .onHover { isHovered = $0 }
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

    @State private var appeared = false

    var body: some View {
        VStack(spacing: AF.Space.s) {
            Image(systemName: icon)
                .font(.system(size: 40, weight: .regular))
                .foregroundStyle(.secondary)
                .symbolEffect(.bounce.up.byLayer, options: .nonRepeating, value: appeared)
                .padding(.bottom, AF.Space.xs)
                .opacity(appeared ? 1 : 0)
                .offset(y: appeared ? 0 : 8)
            Text(title)
                .font(.headline)
                .opacity(appeared ? 1 : 0)
                .offset(y: appeared ? 0 : 8)
            Text(subtitle)
                .font(.callout)
                .multilineTextAlignment(.center)
                .foregroundStyle(.secondary)
                .frame(maxWidth: 360)
                .opacity(appeared ? 1 : 0)
                .offset(y: appeared ? 0 : 8)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding(AF.Space.l)
        .onAppear {
            // Stagger the three lines via implicit animation. The
            // delay-cascade keeps the entrance feeling intentional rather
            // than a single hard fade.
            withAnimation(AF.Motion.springFlow.delay(0.05))           { appeared = true }
        }
    }
}
