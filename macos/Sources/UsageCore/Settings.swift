import Foundation

/// The subset of the Noctalia widget's manifest `defaultSettings` that the compact
/// bar and the popup use, with the same names and defaults. `usageUrl` defaults to
/// Lattice's daemon instead of file mode, which this app does not support. The
/// bar and notification settings at the end are UsageBar's own.
public struct Settings {
  public static let domain = "dev.jeprecated.usagent-bar"
  public static let defaultUsageUrl = "http://lattice.tail6e682f.ts.net:8788/v1/usage"

  public var usageUrl = Settings.defaultUsageUrl
  public var providers = ["claude-code", "chatgpt", "openai", "z-ai"]
  public var windows = ["session", "weekly", "fableWeekly", "monthly", "resetCredits", "spendingCredits"]
  public var excludeWindows = ["web"]
  public var showFableWeekly = true
  public var sessionBarThreshold: Double = 30
  public var hideUnavailable = true
  public var showExpiringUsage = true
  public var expiringUsageUrl = ""
  public var expiringWithin = "168h"
  public var expiringIncludeLowConfidence = true
  public var maxExpiringItems = 4
  public var showChatGPTResetCredits = true
  public var chatGPTResetCreditsUrl = ""
  public var maxResetCredits = 4
  public var auxFetchIntervalMs: Double = 300_000
  public var popoverWidth: Double = 540
  public var popoverMaxHeight: Double = 520
  public var colors: [String: String] = [:]
  /// "accounts", "kinds" or "worst"; see `UsageModel.barSegments`.
  public var barMode = "accounts"
  public var showBarTimes = true
  public var barPrefix = "AI: "
  public var notifyResets = false

  public init() {}

  /// Reads each key that is set in `defaults`, keeping the default for keys that
  /// are missing or have the wrong type. List settings are plist string arrays.
  public init(defaults: UserDefaults) {
    func string(_ key: String) -> String? { defaults.object(forKey: key) as? String }
    func strings(_ key: String) -> [String]? { defaults.object(forKey: key) == nil ? nil : defaults.stringArray(forKey: key) }
    func bool(_ key: String) -> Bool? { (defaults.object(forKey: key) as? NSNumber)?.boolValue }
    func number(_ key: String) -> Double? {
      guard let value = (defaults.object(forKey: key) as? NSNumber)?.doubleValue, value.isFinite else { return nil }
      return value
    }

    if let v = string("usageUrl") { usageUrl = v }
    if let v = strings("providers") { providers = v }
    if let v = strings("windows") { windows = v }
    if let v = strings("excludeWindows") { excludeWindows = v }
    if let v = bool("showFableWeekly") { showFableWeekly = v }
    if let v = number("sessionBarThreshold") { sessionBarThreshold = v }
    if let v = bool("hideUnavailable") { hideUnavailable = v }
    if let v = bool("showExpiringUsage") { showExpiringUsage = v }
    if let v = string("expiringUsageUrl") { expiringUsageUrl = v }
    if let v = string("expiringWithin") { expiringWithin = v }
    if let v = bool("expiringIncludeLowConfidence") { expiringIncludeLowConfidence = v }
    if let v = number("maxExpiringItems") { maxExpiringItems = Int(max(0, min(v, 1000))) }
    if let v = bool("showChatGPTResetCredits") { showChatGPTResetCredits = v }
    if let v = string("chatGPTResetCreditsUrl") { chatGPTResetCreditsUrl = v }
    if let v = number("maxResetCredits") { maxResetCredits = Int(max(0, min(v, 1000))) }
    if let v = number("auxFetchIntervalMs") { auxFetchIntervalMs = v }
    if let v = number("popoverWidth") { popoverWidth = v }
    if let v = number("popoverMaxHeight") { popoverMaxHeight = v }
    if let v = defaults.dictionary(forKey: "colors") { colors = v.compactMapValues { $0 as? String } }
    if let v = string("barMode") { barMode = v }
    if let v = bool("showBarTimes") { showBarTimes = v }
    if let v = string("barPrefix") { barPrefix = v }
    if let v = bool("notifyResets") { notifyResets = v }
  }

  /// The app's own defaults domain. An unbundled binary (for example
  /// `.build/debug/UsageBar`) has no bundle id, so it reads the domain by name.
  public static func load() -> Settings {
    if Bundle.main.bundleIdentifier == domain { return Settings(defaults: .standard) }
    return Settings(defaults: UserDefaults(suiteName: domain) ?? .standard)
  }

  /// Aux endpoints are polled at most this often; UsageStore clamps it to 30 s.
  public var auxIntervalMs: Double { max(30_000, auxFetchIntervalMs) }

  public var expiringEndpoint: String {
    var url = expiringUsageUrl != "" ? expiringUsageUrl : derivedEndpointUrl("/v1/expiring-usage")
    if url == "" { return "" }
    var params: [String] = []
    if expiringWithin != "" { params.append("within=" + encodeURIComponent(expiringWithin)) }
    params.append("includeLowConfidence=" + (expiringIncludeLowConfidence ? "true" : "false"))
    url += (url.contains("?") ? "&" : "?") + params.joined(separator: "&")
    return url
  }

  public var resetCreditsEndpoint: String {
    chatGPTResetCreditsUrl != "" ? chatGPTResetCreditsUrl : derivedEndpointUrl("/v1/chatgpt/reset-credits")
  }

  /// Strips `#…`, `?…` and one trailing `/` from `usageUrl`, then replaces a
  /// trailing `/v1/usage` with `path` or appends `path`.
  public func derivedEndpointUrl(_ path: String) -> String {
    if usageUrl == "" { return "" }
    var url = usageUrl
    if let hash = url.firstIndex(of: "#") { url = String(url[..<hash]) }
    if let query = url.firstIndex(of: "?") { url = String(url[..<query]) }
    if url.hasSuffix("/") { url.removeLast() }
    if url.hasSuffix("/v1/usage") { return String(url.dropLast(9)) + path }
    return url + path
  }
}

/// JavaScript's encodeURIComponent: escapes everything except A–Z a–z 0–9 - _ . ! ~ * ' ( ).
func encodeURIComponent(_ value: String) -> String {
  var allowed = CharacterSet(charactersIn: "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
  allowed.insert(charactersIn: "-_.!~*'()")
  return value.addingPercentEncoding(withAllowedCharacters: allowed) ?? value
}
