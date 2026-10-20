import Combine
import Foundation
import UsageCore

func nowMs() -> Double { Date().timeIntervalSince1970 * 1000 }

/// Polls usagent and publishes a fresh `UsageModel` whenever the data or the
/// clock changes. Settings are re-read on every poll. With `notifyResets` it
/// posts a notification when a used meter's window resets.
@MainActor
final class Store: ObservableObject {
  @Published private(set) var model: UsageModel

  private var settings = Settings.load()
  private var overview = Overview()
  private var expiringUsage = ExpiringUsage()
  private var resetCredits = ResetCredits()
  private var error = ""
  private var fetching = false
  private var lastAuxiliaryFetchAt: Double = 0
  private let client = UsageClient(timeout: 5)
  private var resetWatch = ResetWatch()
  private let notifier = Notifier()

  init() {
    model = UsageModel(overview: Overview(), settings: settings, now: nowMs())
  }

  /// Polls every 10 s and ticks the countdowns every second. The store lives
  /// as long as the app, so the loops never stop.
  func start() {
    Task {
      while true {
        refresh()
        try? await Task.sleep(nanoseconds: 10_000_000_000)
      }
    }
    Task {
      while true {
        try? await Task.sleep(nanoseconds: 1_000_000_000)
        rebuild()
      }
    }
  }

  /// Fetches `/v1/usage` unless a fetch is already running. On failure the last
  /// values stay and the error is published.
  func refresh() {
    guard !fetching else { return }
    fetching = true
    settings = Settings.load()
    if settings.notifyResets { notifier.requestAuthorization() }
    let settings = settings
    Task {
      do {
        if let overview = try await client.fetch(Overview.self, from: settings.usageUrl) {
          self.overview = overview
          error = ""
        }
        maybeFetchAuxiliary(settings)
      } catch {
        self.error = UsageClient.usageErrorText(error)
      }
      fetching = false
      rebuild()
    }
  }

  func rebuild() {
    model = UsageModel(
      overview: overview,
      expiringUsage: expiringUsage,
      resetCredits: resetCredits,
      settings: settings,
      now: nowMs(),
      error: error
    )
    let resets = resetWatch.update(model)
    if settings.notifyResets, !resets.isEmpty { notifier.post(resets) }
  }

  /// UsageStore's rule: after a successful usage fetch, fetch the auxiliary
  /// endpoints at most every `auxFetchIntervalMs`. Their failures are not shown.
  private func maybeFetchAuxiliary(_ settings: Settings) {
    let now = nowMs()
    guard now - lastAuxiliaryFetchAt >= settings.auxIntervalMs else { return }
    lastAuxiliaryFetchAt = now
    if settings.showExpiringUsage, settings.expiringEndpoint != "" {
      Task {
        if let value = try? await client.fetch(ExpiringUsage.self, from: settings.expiringEndpoint) {
          expiringUsage = value
          rebuild()
        }
      }
    }
    if settings.showChatGPTResetCredits, settings.resetCreditsEndpoint != "" {
      Task {
        if let value = try? await client.fetch(ResetCredits.self, from: settings.resetCreditsEndpoint) {
          resetCredits = value
          rebuild()
        }
      }
    }
  }
}
