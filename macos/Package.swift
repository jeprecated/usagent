// swift-tools-version:5.9
import PackageDescription

let package = Package(
  name: "UsageBar",
  platforms: [.macOS(.v13)],
  products: [
    .executable(name: "UsageBar", targets: ["UsageBar"]),
  ],
  targets: [
    .target(name: "UsageCore"),
    .executableTarget(name: "UsageBar", dependencies: ["UsageCore"]),
    // The checks run under XCTest (`swift test`) and, because nixpkgs' SwiftPM
    // cannot run XCTest on Darwin, as the UsageCoreChecks executable in Nix.
    .target(name: "UsageCoreCheckCases", dependencies: ["UsageCore"]),
    .executableTarget(name: "UsageCoreChecks", dependencies: ["UsageCoreCheckCases"]),
    .testTarget(name: "UsageCoreTests", dependencies: ["UsageCoreCheckCases"]),
  ]
)
