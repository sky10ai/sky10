// swift-tools-version: 5.10
import PackageDescription

let package = Package(
    name: "skyshare",
    platforms: [.macOS(.v14)],
    targets: [
        .target(
            name: "skyshare",
            path: "skyshare",
            exclude: ["Resources", "App_main.swift.bak"]
        ),
        .testTarget(
            name: "skyshare-tests",
            dependencies: ["skyshare"],
            path: "skyshare-tests"
        ),
    ]
)
