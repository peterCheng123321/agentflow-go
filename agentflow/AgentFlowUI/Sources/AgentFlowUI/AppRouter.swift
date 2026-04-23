import Foundation
import SwiftUI

/// Single source of truth for app-level sheet presentation.
///
/// SwiftUI's `.sheet(item:)` is unreliable when attached inside nested
/// `ScrollView` / `GlassCard` parents: presentations silently drop. We fix that
/// by hoisting every modal to the App root and letting children fire intents.
///
/// Children mutate `router.sheet = .doc(name, caseID)`; the App root owns the
/// `.sheet(item: $router.sheet)` and renders the correct view.
@MainActor
final class AppRouter: ObservableObject {
    @Published var sheet: Sheet?
    /// When set, the case hub for this `case_id` selects the Research tab (Ask AI / quick actions).
    @Published var pendingCaseResearchFocus: String?

    @Published var inspectorOpen: Bool = false

    enum Sheet: Identifiable, Hashable {
        case document(filename: String, caseID: String?)
        case settings
        case newCase
        case modelPicker

        var id: String {
            switch self {
            case .document(let n, let c): return "doc:\(c ?? "-"):\(n)"
            case .settings:                return "settings"
            case .newCase:                 return "newCase"
            case .modelPicker:             return "modelPicker"
            }
        }
    }

    func open(_ s: Sheet) { sheet = s }
    func close() { sheet = nil }

    func focusResearch(forCase caseID: String) {
        pendingCaseResearchFocus = caseID
    }

    func toggleInspector() { inspectorOpen.toggle() }
}
