import SwiftUI
import AppKit

@main
struct AgentFlowApp: App {
    @StateObject private var backend = BackendManager()
    @StateObject private var api = APIClient(port: 8080)
    @StateObject private var ai = AIController()
    @StateObject private var router = AppRouter()

    init() {
        // Make the window chrome transparent so the ambient background bleeds edge-to-edge.
    }

    var body: some Scene {
        WindowGroup("AgentFlow") {
            ContentView()
                .environmentObject(backend)
                .environmentObject(api)
                .environmentObject(ai)
                .environmentObject(router)
                .frame(minWidth: 960, idealWidth: 1280, minHeight: 720, idealHeight: 820)
                .background(WindowAccessor { window in
                    window.titlebarAppearsTransparent = true
                    window.titleVisibility = .hidden
                    window.isMovableByWindowBackground = false
                    window.styleMask.insert(.fullSizeContentView)
                    window.backgroundColor = NSColor.black
                })
                .task {
                    backend.start()
                }
        }
        .windowStyle(.hiddenTitleBar)
        .commands {
            CommandGroup(replacing: .newItem) { }
        }
    }
}

// Helper to reach the NSWindow for titlebar customization
struct WindowAccessor: NSViewRepresentable {
    let callback: (NSWindow) -> Void
    func makeNSView(context: Context) -> NSView {
        let v = NSView()
        DispatchQueue.main.async {
            if let w = v.window { callback(w) }
        }
        return v
    }
    func updateNSView(_ nsView: NSView, context: Context) {
        DispatchQueue.main.async {
            if let w = nsView.window { callback(w) }
        }
    }
}
