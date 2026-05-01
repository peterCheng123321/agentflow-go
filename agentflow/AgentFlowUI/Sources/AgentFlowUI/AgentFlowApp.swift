import SwiftUI

@main
struct AgentFlowApp: App {
    @StateObject private var backend = BackendManager()
    @StateObject private var api = APIClient(port: 8080)
    @StateObject private var ai = AIController()
    @StateObject private var router = AppRouter()

    var body: some Scene {
        WindowGroup("AgentFlow") {
            ContentView()
                .environmentObject(backend)
                .environmentObject(api)
                .environmentObject(ai)
                .environmentObject(router)
                .frame(minWidth: 960, idealWidth: 1280, minHeight: 720, idealHeight: 820)
                .task { backend.start() }
        }
        .commands {
            CommandGroup(replacing: .newItem) { }
        }
    }
}
