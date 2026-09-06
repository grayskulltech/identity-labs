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
        .library(name: "IdentityAssistant", targets: ["IdentityAssistant"]),
    ],
    dependencies: [
        .package(
            url: "https://github.com/anthropics/ClaudeForFoundationModels.git",
            from: "0.1.0"
        ),
    ],
    targets: [
        .target(
            name: "IdentityAssistant",
            dependencies: [
                .product(name: "ClaudeForFoundationModels", package: "ClaudeForFoundationModels"),
            ]
        ),
        .testTarget(
            name: "IdentityAssistantTests",
            dependencies: ["IdentityAssistant"]
        ),
    ]
)
