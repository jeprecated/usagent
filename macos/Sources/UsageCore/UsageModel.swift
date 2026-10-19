import Foundation

// A port of UsageStore.qml's pure functions for the compact bar and the detail
// popup. Every derived value is computed from one snapshot of the payloads, the
// settings and `now` (epoch milliseconds), so the same inputs give the same text.

public enum Severity: Int, Comparable {
  case ok, warning, critical, error

  public var name: String {
    switch self {
    case .ok: return "ok"
    case .warning: return "warning"
    case .critical: return "critical"
    case .error: return "error"
    }
  }

  public static func < (lhs: Severity, rhs: Severity) -> Bool { lhs.rawValue < rhs.rawValue }
}

public enum PartRole: String {
  case label, value, time
}

public struct BarPart {
  public let text: String
  public let severity: Severity
  public let role: PartRole
}

public struct BarSegment {
  public let provider: String
  public let kind: String
  public let parts: [BarPart]
  public var text: String { parts.map(\.text).joined(separator: " ") }
  public var severity: Severity { parts.map(\.severity).max() ?? .ok }
}

/// One account's quotas. CLIProxy pools several upstream accounts under one
/// provider; every other provider is a single non-pooled account.
public struct Account {
  public let key: String
  public let provider: String
  public let kind: String
  public let pooled: Bool
  public var items: [QuotaItem]
  public var meters: [QuotaItem] = []
  public var credits: [QuotaItem] = []
  public var worstItem: QuotaItem?
  public var title = ""
  public var name = ""
  public var label = ""
  public var shortLabel = ""
}

public struct ProviderGroup {
  public let provider: String
  public let label: String
  public let compactLabel: String
  public let metadataText: String
  public let items: [QuotaItem]
  public let accounts: [Account]
  public let worstItem: QuotaItem?
}

public struct UsageModel {
  public let settings: Settings
  public let now: Double
  public let error: String
  public let overview: Overview
  public let expiringUsage: ExpiringUsage
  public let resetCredits: ResetCredits

  public let visibleQuotaItems: [QuotaItem]
  let providerMetadataById: [String: ProviderMeta]
  let quotaItemById: [String: QuotaItem]

  public private(set) var providerGroups: [ProviderGroup] = []
  public private(set) var compactBarSegments: [BarSegment] = []
  public private(set) var visibleExpiringOpportunities: [Opportunity] = []
  public private(set) var visibleResetCredits: [ResetCredit] = []
  public private(set) var severity: Severity = .ok

  public init(
    overview: Overview,
    expiringUsage: ExpiringUsage = ExpiringUsage(),
    resetCredits: ResetCredits = ResetCredits(),
    settings: Settings = Settings(),
    now: Double,
    error: String = ""
  ) {
    self.settings = settings
    self.now = now
    self.error = error
    self.overview = overview
    self.expiringUsage = expiringUsage
    self.resetCredits = resetCredits

    visibleQuotaItems = overview.quotaItems.filter { $0.visible != false }
    var metadata: [String: ProviderMeta] = [:]
    for provider in overview.providers {
      guard let id = provider.id, id.trimmingCharacters(in: .whitespacesAndNewlines) != "" else { continue }
      metadata[id] = provider
    }
    providerMetadataById = metadata
    var byId: [String: QuotaItem] = [:]
    for item in visibleQuotaItems {
      guard let id = item.id, id != "" else { continue }
      byId[id] = item
    }
    quotaItemById = byId

    providerGroups = groupedProviderData()
    compactBarSegments = providerGroups.flatMap { $0.accounts.compactMap(buildAccountSegment) }
    visibleExpiringOpportunities = filteredExpiringOpportunities()
    visibleResetCredits = filteredResetCredits()
    severity = overallSeverity()
  }

  public var compactBarText: String {
    compactBarSegments.isEmpty ? "AI: —" : "AI: " + compactBarSegments.map(\.text).joined(separator: " · ")
  }

  public var isEmpty: Bool {
    providerGroups.isEmpty && visibleExpiringOpportunities.isEmpty && visibleResetCredits.isEmpty
  }

  // MARK: Gating

  func providerEnabled(_ provider: String?) -> Bool {
    guard let provider else { return false }
    return settings.providers.contains(provider)
  }

  func windowEnabled(_ windowKey: String) -> Bool {
    if windowKey == "fableWeekly" { return settings.showFableWeekly && settings.windows.contains(windowKey) }
    return settings.windows.contains(windowKey) && !settings.excludeWindows.contains(windowKey)
  }

  // MARK: State and severity

  public func quotaRuntimeState(_ item: QuotaItem?) -> String {
    guard let item else { return "unknown" }
    if item.state == "error" || item.error?.code == "credentials-missing" { return "error" }
    if let staleAt = item.refresh?.staleAt, staleAt <= now { return "stale" }
    if let state = item.state, state != "" { return state }
    return "fresh"
  }

  public func quotaRuntimeSeverity(_ item: QuotaItem?) -> Severity {
    let state = quotaRuntimeState(item)
    if state == "error" || item?.severity == "error" { return .error }
    if item?.severity == "critical" { return .critical }
    if item?.severity == "warning" || item?.severity == "warn" { return .warning }
    if state == "stale" { return .warning }
    return .ok
  }

  func overallSeverity() -> Severity {
    var result = Severity.ok
    for item in visibleQuotaItems {
      guard providerEnabled(item.provider) else { continue }
      let windowKey = semanticWindow(item)
      if !windowEnabled(windowKey), !(windowKey == "unknown" && (item.error != nil || quotaRuntimeState(item) == "error")) { continue }
      let severity = quotaRuntimeSeverity(item)
      if severity == .error { return .error }
      if severity == .critical { result = .critical } else if severity == .warning, result == .ok { result = .warning }
    }
    return result
  }

  // MARK: Windows

  static func normalizedWindowToken(_ value: String?) -> String {
    replacing((value ?? "").lowercased(), "[^a-z0-9]", "")
  }

  static func isResetCreditsWindow(_ item: QuotaItem?) -> Bool {
    let window = item?.window
    return [window?.id, window?.label, window?.kind].contains { value in
      let token = normalizedWindowToken(value)
      return token == "resetcredits" || token == "resets" || token == "credit" || token == "credits"
    }
  }

  public func semanticWindow(_ item: QuotaItem?) -> String {
    let label = (item?.label ?? "").lowercased()
    let id = (item?.id ?? "").lowercased()
    let windowId = item?.window?.id ?? ""
    let windowText = [item?.window?.label, item?.window?.id, item?.window?.kind].map { ($0 ?? "").lowercased() }.joined(separator: " ")
    let text = label + " " + id + " " + windowText
    let weekly = text.contains("week") || text.contains("7d") || text.contains("7 d")

    if windowId == "spendingCredits" { return "spendingCredits" }
    if Self.isResetCreditsWindow(item) { return "resetCredits" }
    if item?.provider == "z-ai", text.contains("web") || text.contains("search") { return "web" }
    if id.contains("fable") || windowId == "fableWeekly", weekly { return "fableWeekly" }
    if windowId == "session" || windowId == "weekly" || windowId == "monthly" { return windowId }
    if text.contains("month") { return "monthly" }
    if weekly { return "weekly" }
    if text.contains("session") || text.contains("5h") || text.contains("5 h") || text.contains("hour") { return "session" }
    return "unknown"
  }

  public func windowLabel(_ windowKey: String) -> String {
    switch windowKey {
    case "session": return "S"
    case "weekly": return "W"
    case "monthly": return "M"
    case "fableWeekly": return "F"
    case "resetCredits": return "R"
    default: return windowKey
    }
  }

  public func windowName(_ windowKey: String) -> String {
    switch windowKey {
    case "session": return "Session"
    case "weekly": return "Weekly"
    case "fableWeekly": return "Fable weekly"
    case "monthly": return "Monthly"
    case "resetCredits": return "Resets"
    case "spendingCredits": return "Credits"
    default: return "Quota"
    }
  }

  func windowOrder(_ windowKey: String) -> Double {
    let order = ["session", "weekly", "fableWeekly", "monthly", "resetCredits", "spendingCredits"]
    return order.firstIndex(of: windowKey).map(Double.init) ?? 3.5
  }

  static func isCreditWindow(_ windowKey: String) -> Bool {
    windowKey == "resetCredits" || windowKey == "spendingCredits"
  }

  // MARK: Labels

  public func providerCompactLabel(_ provider: String?) -> String {
    switch provider {
    case "claude-code": return "C"
    case "chatgpt": return "GPT"
    case "openai": return "OAI"
    case "z-ai": return "Z"
    default:
      let text = provider ?? ""
      return String((text == "" ? "?" : text).prefix(3)).uppercased()
    }
  }

  public func providerLongLabel(_ provider: String?) -> String {
    if let provider, let label = providerMetadataById[provider]?.label?.trimmingCharacters(in: .whitespacesAndNewlines), label != "" {
      return label
    }
    switch provider {
    case "claude-code": return "Claude"
    case "chatgpt": return "ChatGPT"
    case "openai": return "OpenAI"
    case "z-ai": return "z.ai"
    default:
      let text = provider ?? ""
      return text == "" ? "provider" : text
    }
  }

  static func kindCompactLabel(_ kind: String) -> String {
    let labels = ["claude": "C", "codex": "X", "antigravity": "AG", "devin": "DV", "kimi": "K", "meta": "M", "xai": "G"]
    if let label = labels[kind] { return label }
    return String((kind == "" ? "?" : kind).prefix(2)).uppercased()
  }

  // MARK: Values

  public func remainingPercentValue(_ item: QuotaItem?) -> Double? {
    guard let item else { return nil }
    var used = item.percentUsed
    var remainingPercent: Double?
    if item.unit == "percent" || item.unit == "%" {
      if let remaining = item.remaining { remainingPercent = remaining } else if let used { remainingPercent = 100 - used }
    } else if let used {
      remainingPercent = 100 - used
    } else if let itemUsed = item.used, let limit = item.limit, limit > 0 {
      used = clamp(itemUsed / limit * 100, 0, 100)
      remainingPercent = 100 - used!
    }
    return remainingPercent ?? used.map { 100 - $0 }
  }

  static func preferRemainingUnit(_ item: QuotaItem?) -> Bool {
    guard let item, item.remaining != nil, !isPercentUnit(item.unit) else { return false }
    return ["credit", "credits", "usd", "reset", "resets"].contains(normalizedUnit(item.unit))
  }

  static func shouldShowRemainingValue(_ item: QuotaItem?, _ percent: Double?) -> Bool {
    guard let item, item.remaining != nil, !isPercentUnit(item.unit) else { return false }
    return percent == nil || preferRemainingUnit(item)
  }

  func resetAt(_ item: QuotaItem?) -> Double? {
    item?.reset?.resetAt ?? item?.window?.resetAt
  }

  func timeLeftText(_ item: QuotaItem?) -> String {
    guard let reset = resetAt(item), reset > now else { return "" }
    return formatTimeLeft(reset - now)
  }

  // MARK: Items and grouping

  public func isModelQuotaItem(_ item: QuotaItem?) -> Bool {
    guard let item else { return false }
    let text = [item.id, item.label, item.shortLabel, item.window?.id, item.window?.label]
      .map { ($0 ?? "").lowercased() }
      .joined(separator: " ")
    return matches(text, "(^|[^a-z0-9])model([^a-z0-9]|$)") != nil
  }

  func providerItems(_ provider: String) -> [QuotaItem] {
    let items = visibleQuotaItems.filter { item in
      guard item.provider == provider else { return false }
      let windowKey = semanticWindow(item)
      if windowEnabled(windowKey) { return true }
      return windowKey == "unknown" && (item.error != nil || quotaRuntimeState(item) == "error")
    }
    return dedupeProviderItems(items)
  }

  /// Per semantic window, drops per-model items when the window has another item.
  func dedupeProviderItems(_ items: [QuotaItem]) -> [QuotaItem] {
    var keys: [String] = []
    var byWindow: [String: [QuotaItem]] = [:]
    for item in items {
      let key = semanticWindow(item)
      if byWindow[key] == nil { keys.append(key) }
      byWindow[key, default: []].append(item)
    }
    return keys.flatMap { key -> [QuotaItem] in
      let peers = byWindow[key]!
      let nonModelPeers = peers.filter { !isModelQuotaItem($0) }
      return nonModelPeers.isEmpty ? peers : nonModelPeers
    }
  }

  func groupedProviderData() -> [ProviderGroup] {
    settings.providers.compactMap { provider in
      let items = providerItems(provider)
      if items.isEmpty { return nil }
      return ProviderGroup(
        provider: provider,
        label: providerLongLabel(provider),
        compactLabel: providerCompactLabel(provider),
        metadataText: providerDetailText(provider, items),
        items: items,
        accounts: accountGroups(provider, items),
        worstItem: worstQuotaItem(items)
      )
    }
  }

  /// CLIProxy item ids are "cliproxy-<kind>-<account>-<window>" and labels
  /// "<Kind> <account> <window>".
  static func cliproxyAccountMatch(_ item: QuotaItem) -> (kind: String, account: String)? {
    guard item.provider == "cliproxy", let groups = matches(item.id ?? "", "^cliproxy-([a-z]+)-([a-z0-9]+)-") else { return nil }
    return (groups[1], groups[2])
  }

  static func commonLabelPrefix(_ items: [QuotaItem]) -> String {
    let first = Array(items[0].label ?? "")
    var prefix = first
    for item in items.dropFirst() {
      let label = Array(item.label ?? "")
      var length = 0
      while length < prefix.count, length < label.count, prefix[length] == label[length] { length += 1 }
      prefix = Array(prefix.prefix(length))
    }
    if items.count > 1, prefix != first {
      prefix = Array(prefix.prefix((prefix.lastIndex(of: " ") ?? -1) + 1))
    }
    return String(prefix).trimmingCharacters(in: .whitespacesAndNewlines)
  }

  func accountGroups(_ provider: String, _ items: [QuotaItem]) -> [Account] {
    var accounts: [Account] = []
    var indexByKey: [String: Int] = [:]
    for item in items {
      let match = Self.cliproxyAccountMatch(item)
      let key = match.map { $0.kind + "-" + $0.account } ?? provider
      if let index = indexByKey[key] {
        accounts[index].items.append(item)
      } else {
        indexByKey[key] = accounts.count
        accounts.append(Account(key: key, provider: provider, kind: match?.kind ?? provider, pooled: match != nil, items: [item]))
      }
    }

    var shortLabelCounts: [String: Int] = [:]
    for index in accounts.indices {
      var account = accounts[index]
      // A stable sort, like Array.prototype.sort.
      account.items = account.items.enumerated()
        .sorted { (windowOrder(semanticWindow($0.element)), $0.offset) < (windowOrder(semanticWindow($1.element)), $1.offset) }
        .map(\.element)
      account.meters = account.items.filter { !Self.isCreditWindow(semanticWindow($0)) }
      account.credits = account.items.filter { Self.isCreditWindow(semanticWindow($0)) }
      account.worstItem = worstQuotaItem(account.items)
      if account.pooled {
        let label = Self.commonLabelPrefix(account.items)
        if let space = label.firstIndex(of: " "), space > label.startIndex {
          account.title = String(label[..<space])
          account.name = String(label[label.index(after: space)...])
        } else {
          account.title = label
          account.name = ""
        }
        account.label = label
        account.shortLabel = Self.kindCompactLabel(account.kind) + (account.name != "" ? "·" + account.name.prefix(1).lowercased() : "")
      } else {
        account.title = providerLongLabel(provider)
        account.name = ""
        account.label = account.title
        account.shortLabel = providerCompactLabel(provider)
      }
      shortLabelCounts[account.shortLabel, default: 0] += 1
      accounts[index] = account
    }

    var shortLabelSeen: [String: Int] = [:]
    for index in accounts.indices {
      let shortLabel = accounts[index].shortLabel
      guard shortLabelCounts[shortLabel, default: 0] >= 2 else { continue }
      shortLabelSeen[shortLabel, default: 0] += 1
      accounts[index].shortLabel = shortLabel + String(shortLabelSeen[shortLabel]!)
    }
    return accounts
  }

  /// The item with the lowest remaining percentage, else the most severe one.
  public func worstQuotaItem(_ items: [QuotaItem]) -> QuotaItem? {
    var worstNumeric: QuotaItem?
    var worstPercent: Double = 101
    for item in items {
      if let percent = remainingPercentValue(item), percent < worstPercent {
        worstPercent = percent
        worstNumeric = item
      }
    }
    if let worstNumeric { return worstNumeric }

    var fallback: QuotaItem?
    var fallbackRank = -1
    for item in items {
      let rank = quotaRuntimeSeverity(item).rawValue
      if rank > fallbackRank {
        fallbackRank = rank
        fallback = item
      }
    }
    return fallback
  }

  func filteredExpiringOpportunities() -> [Opportunity] {
    guard settings.showExpiringUsage else { return [] }
    var result: [Opportunity] = []
    for opportunity in expiringUsage.opportunities {
      guard providerEnabled(opportunity.provider) else { continue }
      if let item = quotaItem(for: opportunity), isModelQuotaItem(item) { continue }
      result.append(opportunity)
      if settings.maxExpiringItems > 0, result.count >= settings.maxExpiringItems { break }
    }
    return result
  }

  func filteredResetCredits() -> [ResetCredit] {
    guard settings.showChatGPTResetCredits, providerEnabled("chatgpt") else { return [] }
    let credits = resetCredits.credits
      .filter { $0.status != "expired" && $0.status != "redeemed" }
      .enumerated()
      .sorted { (timestampFromText($0.element.expiresAt), $0.offset) < (timestampFromText($1.element.expiresAt), $1.offset) }
      .map(\.element)
    return settings.maxResetCredits > 0 ? Array(credits.prefix(settings.maxResetCredits)) : credits
  }

  func quotaItem(for opportunity: Opportunity) -> QuotaItem? {
    guard let itemId = opportunity.itemId, itemId != "" else { return nil }
    return quotaItemById[itemId]
  }

  // MARK: Compact bar

  public func compactItemText(_ item: QuotaItem?) -> String {
    guard let item else { return "" }
    let state = quotaRuntimeState(item)
    if state == "error" { return "err" }
    let percent = remainingPercentValue(item)
    var text: String
    if Self.shouldShowRemainingValue(item, percent) {
      text = formatCompactQuotaValue(item.remaining, item.unit)
    } else if let percent {
      text = formatPercentValue(percent)
    } else if state == "stale" {
      text = "stale"
    } else {
      text = settings.hideUnavailable ? "" : "—"
    }
    if text == "" { return "" }

    let severity = quotaRuntimeSeverity(item)
    if state == "stale" || severity == .critical || severity == .warning, text != "stale" { text += "!" }
    return text
  }

  func accountWindowItems(_ account: Account, _ windowKey: String) -> [QuotaItem] {
    account.meters.filter { semanticWindow($0) == windowKey }
  }

  /// The bar headline is the account's longest window: weekly, else monthly,
  /// else its worst other meter.
  func accountPrimaryItem(_ account: Account) -> QuotaItem? {
    for key in ["weekly", "monthly"] {
      let items = accountWindowItems(account, key)
      if !items.isEmpty { return worstQuotaItem(items) }
    }
    let others = account.meters.filter { semanticWindow($0) != "fableWeekly" }
    return worstQuotaItem(others.isEmpty ? account.meters : others)
  }

  func buildAccountSegment(_ account: Account) -> BarSegment? {
    let primary = accountPrimaryItem(account)
    let value = compactItemText(primary)
    if value == "" { return nil }
    let primaryWindow = semanticWindow(primary)
    var parts = [BarPart(text: account.shortLabel, severity: .ok, role: .label)]
    let windowPrefix = primaryWindow != "weekly" && primaryWindow != "unknown" ? windowLabel(primaryWindow) + " " : ""
    parts.append(BarPart(text: windowPrefix + value, severity: quotaRuntimeSeverity(primary), role: .value))

    let primaryPercent = remainingPercentValue(primary)
    if primaryWindow != "fableWeekly", windowEnabled("fableWeekly") {
      let fable = worstQuotaItem(accountWindowItems(account, "fableWeekly"))
      if let fablePercent = remainingPercentValue(fable), primaryPercent == nil || fablePercent < primaryPercent! {
        parts.append(BarPart(text: "F" + compactItemText(fable), severity: quotaRuntimeSeverity(fable), role: .value))
      }
    }

    let timeLeft = timeLeftText(primary)
    if timeLeft != "" { parts.append(BarPart(text: timeLeft, severity: .ok, role: .time)) }

    if primaryWindow != "session" {
      let session = worstQuotaItem(accountWindowItems(account, "session"))
      if let sessionPercent = remainingPercentValue(session), sessionPercent <= settings.sessionBarThreshold {
        let sessionLeft = timeLeftText(session)
        let text = "S " + compactItemText(session) + (sessionLeft != "" ? " " + sessionLeft : "")
        parts.append(BarPart(text: text, severity: quotaRuntimeSeverity(session), role: .value))
      }
    }

    return BarSegment(provider: account.provider, kind: account.kind, parts: parts)
  }

  // MARK: Popup

  static func compactMetadataValue(_ value: String?) -> String {
    let text = (value ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    return text.count > 40 ? String(text.prefix(37)) + "…" : text
  }

  func updatedAgeText(_ timestamp: Double?) -> String {
    guard let timestamp else { return "" }
    let diff = now - timestamp
    return diff < 0 ? "updated just now" : "updated " + formatDuration(diff) + " ago"
  }

  func providerMetadataDetailText(_ provider: String) -> String {
    guard let metadata = providerMetadataById[provider] else { return "" }
    var parts: [String] = []
    let state = Self.compactMetadataValue(metadata.state)
    if state != "" { parts.append(state) }
    let source = Self.compactMetadataValue(metadata.source)
    if source != "" { parts.append("source " + source) }
    let updated = updatedAgeText(metadata.lastUpdatedAt)
    if updated != "" { parts.append(updated) }
    return parts.joined(separator: " · ")
  }

  func quotaRefreshScheduleText(_ item: QuotaItem) -> String {
    guard let refresh = item.refresh else { return "" }
    var parts: [String] = []
    if let nextRefreshAt = refresh.nextRefreshAt {
      let diff = nextRefreshAt - now
      parts.append(diff <= 0 ? "refresh due" : "refreshes in " + formatDuration(diff))
    }
    if let staleAt = refresh.staleAt {
      let diff = staleAt - now
      parts.append(diff <= 0 ? "stale now" : "stale in " + formatDuration(diff))
    }
    return parts.joined(separator: " · ")
  }

  func providerDetailText(_ provider: String, _ items: [QuotaItem]) -> String {
    var parts: [String] = []
    let metadata = providerMetadataDetailText(provider)
    if metadata != "" { parts.append(metadata) }
    if let schedule = items.lazy.map(quotaRefreshScheduleText).first(where: { $0 != "" }) { parts.append(schedule) }
    return parts.joined(separator: " · ")
  }

  public func meterLabel(_ item: QuotaItem, _ account: Account) -> String {
    var text = item.label ?? ""
    let prefix = account.label
    if prefix != "", text.hasPrefix(prefix + " ") {
      text = String(text.dropFirst(prefix.count + 1))
    } else if prefix != "", text == prefix {
      text = ""
    }
    text = text.trimmingCharacters(in: .whitespacesAndNewlines)
    if text == "" { return windowName(semanticWindow(item)) }
    if text == "5h" { return "5-hour" }
    return text.prefix(1).uppercased() + text.dropFirst()
  }

  public func meterPercent(_ item: QuotaItem) -> Double {
    remainingPercentValue(item).map { clamp($0, 0, 100) } ?? 0
  }

  public func meterValueText(_ item: QuotaItem) -> String {
    if quotaRuntimeState(item) == "error" { return "error" }
    return formatPercentValue(remainingPercentValue(item))
  }

  public func meterResetText(_ item: QuotaItem) -> String {
    guard let reset = resetAt(item) else { return "" }
    let diff = reset - now
    return diff <= 0 ? "reset due" : "resets in " + formatDuration(diff)
  }

  public func creditChipText(_ item: QuotaItem) -> String {
    let windowLabelText = item.window?.label ?? ""
    let name = windowLabelText.count > 1 ? windowLabelText : windowName(semanticWindow(item))
    guard let remaining = item.remaining else { return name + " —" }
    if normalizedUnit(item.unit) == "usd" { return name + " " + formatQuotaValue(remaining, "usd") }
    var text = name + " " + formatCompactNumber(remaining)
    if remaining > 0, let expiresAt = item.expiresAt, expiresAt > now {
      text += " · " + (remaining > 1 ? "next expires" : "expires") + " in " + formatDuration(expiresAt - now)
    }
    return text
  }

  public func quotaErrorText(_ item: QuotaItem) -> String {
    guard let error = item.error, !isRefreshOnlyError(item) else { return "" }
    return sanitizeErrorText(nonEmpty(error.message) ?? nonEmpty(error.code) ?? "provider error")
  }

  /// A recoverable rate-limit error on a stale item that still has values only
  /// delays the next refresh, so the popup does not show it.
  func isRefreshOnlyError(_ item: QuotaItem) -> Bool {
    guard let error = item.error, error.recoverable == true, quotaRuntimeState(item) == "stale" else { return false }
    guard item.remaining != nil || item.used != nil || item.percentUsed != nil || item.limit != nil else { return false }
    let text = (nonEmpty(error.message) ?? error.code ?? "").lowercased()
    return text.contains("429") || text.contains("rate limit") || text.contains("rate_limit") || text.contains("retry")
  }

  public func expiringOpportunityText(_ opportunity: Opportunity) -> String {
    let provider = providerCompactLabel(opportunity.provider)
    let item = quotaItem(for: opportunity)
    let windowKey = item != nil ? semanticWindow(item) : "unknown"
    let window = windowKey != "unknown" ? windowLabel(windowKey) + " " : ""
    let amount = opportunity.estimatedWastedAmount != nil
      ? formatCompactQuotaValue(opportunity.estimatedWastedAmount, opportunity.unit) : "quota"
    let time = opportunity.timeRemainingMs != nil ? formatCompactDuration(opportunity.timeRemainingMs) : "soon"
    let confidence = nonEmpty(opportunity.confidence).map { " · " + $0 } ?? ""
    return provider + " " + window + amount + " may expire in " + time + confidence
  }

  public func expiringOpportunityDetailText(_ opportunity: Opportunity) -> String {
    if let reason = opportunity.reasons.first { return reason }
    if let percent = opportunity.percentRemaining { return formatPercentValue(percent) + " remaining" }
    return nonEmpty(opportunity.label) ?? nonEmpty(opportunity.itemId) ?? "use-it-soon"
  }

  public var resetCreditsSummaryText: String {
    let available = resetCredits.availableCount ?? Double(visibleResetCredits.count)
    return available <= 0 ? "ChatGPT reset credits" : "ChatGPT reset credits · " + jsString(available) + " available"
  }

  public func resetCreditText(_ credit: ResetCredit) -> String {
    let title = nonEmpty(credit.title) ?? "reset credit"
    let expires = timestampFromText(credit.expiresAt)
    return title + (expires < maxSafeInteger ? " · expires in " + formatCompactDuration(expires - now) : "")
  }
}

func nonEmpty(_ value: String?) -> String? {
  guard let value, value != "" else { return nil }
  return value
}
