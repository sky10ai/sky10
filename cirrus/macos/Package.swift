// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "cirrus",
    platforms: [.macOS(.v14)],
    targets: [
        .target(
            name: "cirrus",
            path: "cirrus",
            exclude: ["Resources", "App.swift", "Services/NotificationManager.swift", "Services/FileProviderManager.swift"]
        ),
        .testTarget(
            name: "cirrus-tests",
            dependencies: ["cirrus"],
            path: "cirrus-tests"
        ),
    ]
)
