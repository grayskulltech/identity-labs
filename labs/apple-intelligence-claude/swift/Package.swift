// swift-tools-version: 6.2
// Requires Xcode 27 on a macOS 27 host. The Foundation Models server-side
// provider API and the ClaudeForFoundationModels package target the OS 27 releases.

import PackageDescription

let package = Package(
    name: "IdentityAssistant",
    platforms: [
        .iOS(.v27),
        .macOS(.v27),
        .visionOS(.v27),
        .watchOS(.v27),
    ],
    products: [
        .library(name: "MCPBridge", targets: ["MCPBridge"]),
        .library(name: "OnDeviceAssistant", targets: ["OnDeviceAssistant"]),
        .library(name: "IdentityAssistant", targets: ["IdentityAssistant"]),
    ],
    dependencies: [
        .package(
            url: "https://github.com/anthropics/ClaudeForFoundationModels.git",
            from: "0.1.0"
        ),
    ],
    targets: [
        // Model-agnostic: turns an MCP gateway's tools into Foundation Models tools.
        .target(name: "MCPBridge"),
        // On-device only. Links no model vendor package, so a keyboard extension can
        // import it without pulling a cloud SDK into a 60 MB process.
        .target(name: "OnDeviceAssistant", dependencies: ["MCPBridge"]),
        .target(
            name: "IdentityAssistant",
            dependencies: [
                "MCPBridge",
                .product(name: "ClaudeForFoundationModels", package: "ClaudeForFoundationModels"),
            ]
        ),
        .testTarget(
            name: "MCPBridgeTests",
            dependencies: ["MCPBridge"]
        ),
        .testTarget(
            name: "OnDeviceAssistantTests",
            dependencies: ["OnDeviceAssistant"]
        ),
        .testTarget(
            name: "IdentityAssistantTests",
            dependencies: ["IdentityAssistant"]
        ),
    ]
)
