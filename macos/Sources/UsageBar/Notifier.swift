import Foundation
import UsageCore
import UserNotifications

/// Posts reset notifications. UNUserNotificationCenter needs the app bundle, so
/// an unbundled binary (for example `.build/debug/UsageBar`) posts nothing.
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
  private lazy var center: UNUserNotificationCenter? = {
    guard Bundle.main.bundleIdentifier == Settings.domain else { return nil }
    let center = UNUserNotificationCenter.current()
    center.delegate = self
    return center
  }()
  private var requested = false

  /// Asks once per launch; macOS prompts only the first time.
  func requestAuthorization() {
    guard !requested, let center else { return }
    requested = true
    center.requestAuthorization(options: [.alert, .sound]) { _, _ in }
  }

  func post(_ resets: [MeterReset]) {
    guard let center else { return }
    let content = UNMutableNotificationContent()
    content.title = "Quota reset"
    content.body = resetNotificationBody(resets)
    content.sound = .default
    center.add(UNNotificationRequest(identifier: UUID().uuidString, content: content, trigger: nil))
  }

  // Without this, macOS hides the banner while the app is active, for example
  // while the popover is open.
  func userNotificationCenter(
    _ center: UNUserNotificationCenter,
    willPresent notification: UNNotification,
    withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
  ) {
    completionHandler([.banner, .sound])
  }
}
