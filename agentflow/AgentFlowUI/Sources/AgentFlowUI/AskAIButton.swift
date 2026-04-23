import SwiftUI

/// Primary AI affordance. Tap → open inspector with the case bound.
/// Chevron menu → quick actions + model switcher.
struct AskAIButton: View {
    @EnvironmentObject var api: APIClient
    @EnvironmentObject var ai: AIController
    @EnvironmentObject var router: AppRouter
    let caseID: String?

    var body: some View {
        Menu {
            Section("Ask AI about this case") {
                ForEach(AIController.QuickAction.allCases) { a in
                    Button {
                        ai.bind(toCase: caseID)
                        if let id = caseID { router.focusResearch(forCase: id) }
                        ai.run(a, api: api)
                    } label: {
                        Label(a.title, systemImage: a.icon)
                    }
                }
            }
            Section("Model") {
                ForEach(ai.models) { m in
                    Button {
                        ai.selectedModelID = m.id
                    } label: {
                        if m.id == ai.selectedModelID {
                            Label(m.name, systemImage: "checkmark")
                        } else {
                            Text(m.name)
                        }
                    }
                }
            }
        } label: {
            HStack(spacing: 6) {
                Image(systemName: "sparkles")
                Text("Ask AI")
            }
        } primaryAction: {
            ai.bind(toCase: caseID)
            if let id = caseID { router.focusResearch(forCase: id) }
        }
        .menuStyle(.borderlessButton)
        .buttonStyle(.afPrimary)
        .keyboardShortcut("i", modifiers: [.command])
        .task { await ai.loadModelsIfNeeded(api: api) }
    }
}
