import AppKit
import UsageCore

/// The entry point. With `--dump` it fetches once, prints the compact bar text
/// and a plain-text popup, and exits (non-zero if the usage fetch fails).
/// Otherwise it runs as an accessory menu bar app; Quit exits 0.
@main
enum UsageBarApp {
  @MainActor static func main() {
    if CommandLine.arguments.contains("--dump") {
      Task { exit(await dump()) }
      dispatchMain()
    }

    let app = NSApplication.shared
    let delegate = AppDelegate()
    app.delegate = delegate
    app.setActivationPolicy(.accessory)
    app.run()
  }

  static func dump() async -> Int32 {
    let settings = Settings.load()
    let client = UsageClient(timeout: 5)
    let overview: Overview
    do {
      guard let value = try await client.fetch(Overview.self, from: settings.usageUrl) else {
        FileHandle.standardError.write(Data("usage payload is not a JSON object: \(settings.usageUrl)\n".utf8))
        return 1
      }
      overview = value
    } catch {
      FileHandle.standardError.write(Data((UsageClient.usageErrorText(error) + ": \(settings.usageUrl)\n").utf8))
      return 1
    }

    async let expiring = settings.showExpiringUsage && settings.expiringEndpoint != ""
      ? try? client.fetch(ExpiringUsage.self, from: settings.expiringEndpoint) : nil
    async let credits = settings.showChatGPTResetCredits && settings.resetCreditsEndpoint != ""
      ? try? client.fetch(ResetCredits.self, from: settings.resetCreditsEndpoint) : nil
    let model = UsageModel(
      overview: overview,
      expiringUsage: await expiring ?? ExpiringUsage(),
      resetCredits: await credits ?? ResetCredits(),
      settings: settings,
      now: nowMs()
    )
    print(model.compactBarText)
    print()
    print(model.popupText)
    return 0
  }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
  private var store: Store?
  private var status: StatusController?

  func applicationDidFinishLaunching(_ notification: Notification) {
    let store = Store()
    status = StatusController(store: store)
    store.start()
    self.store = store
  }
}
