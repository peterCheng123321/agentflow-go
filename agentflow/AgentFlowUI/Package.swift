// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "AgentFlowUI",
    platforms: [.macOS("26.0")],
    products: [
        .executable(name: "AgentFlowUI", targets: ["AgentFlowUI"])
    ],
    targets: [
        .executableTarget(
            name: "AgentFlowUI",
            path: "Sources/AgentFlowUI",
            swiftSettings: [
                .unsafeFlags(["-framework", "AppKit"])
            ]
        ),
        .testTarget(
            name: "AgentFlowTests",
            dependencies: ["AgentFlowUI"],
            path: "Tests/AgentFlowTests"
        )
    ]
)
