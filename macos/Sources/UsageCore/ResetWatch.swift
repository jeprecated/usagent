import Foundation

/// A meter whose window reset while the app was running.
public struct MeterReset: Equatable {
  public let account: String
  public let meter: String

  public init(account: String, meter: String) {
    self.account = account
    self.meter = meter
  }
}

/// Reports each meter once, when the clock passes its reset time. Only meters that
/// had been used count. A reset that had already passed when the meter was first
/// seen, or a reset time that moved later, is not reported.
public struct ResetWatch {
  struct Pending {
    let resetAt: Double
    let reset: MeterReset
  }

  var pending: [Pending] = []

  public init() {}

  /// Returns the resets that are now due, then remembers the model's upcoming ones.
  public mutating func update(_ model: UsageModel) -> [MeterReset] {
    let due = pending.filter { $0.resetAt <= model.now }.map(\.reset)
    pending = model.upcomingResets()
    return due
  }
}

/// One line per account: "Claude j***@self.example: Weekly, Fable weekly".
public func resetNotificationBody(_ resets: [MeterReset]) -> String {
  var accounts: [String] = []
  var meters: [String: [String]] = [:]
  for reset in resets {
    if meters[reset.account] == nil { accounts.append(reset.account) }
    meters[reset.account, default: []].append(reset.meter)
  }
  return accounts.map { $0 + ": " + meters[$0]!.joined(separator: ", ") }.joined(separator: "\n")
}

extension UsageModel {
  /// The shown meters with usage whose window resets later.
  func upcomingResets() -> [ResetWatch.Pending] {
    providerGroups.flatMap(\.accounts).flatMap { account in
      account.meters.compactMap { item -> ResetWatch.Pending? in
        guard let reset = resetAt(item), reset > now,
          let percent = remainingPercentValue(item), percent < 100
        else { return nil }
        return ResetWatch.Pending(resetAt: reset, reset: MeterReset(account: account.label, meter: meterLabel(item, account)))
      }
    }
  }
}
