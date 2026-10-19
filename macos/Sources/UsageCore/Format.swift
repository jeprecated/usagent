import Foundation

// Formatting helpers ported from UsageStore. They follow JavaScript semantics:
// Math.round rounds .5 toward +∞, and numbers print without a trailing ".0".

/// JavaScript's Math.round.
func jsRound(_ value: Double) -> Double { (value + 0.5).rounded(.down) }

/// JavaScript's String(number) for the finite values these helpers produce.
func jsString(_ value: Double) -> String {
  if value == value.rounded(), abs(value) < 1e15 { return String(Int64(value)) }
  return "\(value)"
}

func clamp(_ value: Double, _ low: Double, _ high: Double) -> Double { max(low, min(high, value)) }

public func formatPercentValue(_ value: Double?) -> String {
  guard let value else { return "—" }
  return jsString(clamp(jsRound(value), 0, 100)) + "%"
}

func normalizedUnit(_ unit: String?) -> String {
  (unit ?? "").trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
}

func isPercentUnit(_ unit: String?) -> Bool {
  let normalized = normalizedUnit(unit)
  return normalized == "percent" || normalized == "%"
}

public func formatCompactNumber(_ value: Double?) -> String {
  guard let value else { return "—" }
  let magnitude = abs(value)
  if magnitude >= 1_000_000 { return jsString(jsRound(value / 100_000) / 10) + "M" }
  if magnitude >= 1000 { return jsString(jsRound(value / 100) / 10) + "k" }
  return jsString(magnitude >= 10 ? jsRound(value) : jsRound(value * 10) / 10)
}

public func formatCompactQuotaValue(_ value: Double?, _ unit: String?) -> String {
  guard let value else { return "—" }
  if isPercentUnit(unit) { return formatPercentValue(value) }
  if normalizedUnit(unit) == "usd" { return "$" + formatCompactNumber(value) }
  let text = formatCompactNumber(value)
  if let unit, unit != "" { return text + " " + unit }
  return text
}

func formatQuotaValue(_ value: Double?, _ unit: String?) -> String {
  guard let value else { return "—" }
  if unit == "usd" || unit == "USD" { return "$" + String(format: "%.2f", jsRound(value * 100) / 100) }
  if unit == "percent" || unit == "%" { return formatPercentValue(value) }
  let rounded = abs(value) >= 10 ? jsRound(value) : jsRound(value * 10) / 10
  if let unit, unit != "" { return jsString(rounded) + " " + unit }
  return jsString(rounded)
}

/// "45m", "5h30m", "30h", "6d20h".
public func formatCompactDuration(_ ms: Double?) -> String {
  guard let ms else { return "—" }
  let minutes = max(0, (ms / 60000).rounded(.up))
  if minutes < 60 { return jsString(minutes) + "m" }
  let hours = (minutes / 60).rounded(.down)
  let remainingMinutes = minutes.truncatingRemainder(dividingBy: 60)
  if hours < 48 { return jsString(hours) + "h" + (hours < 6 && remainingMinutes > 0 ? jsString(remainingMinutes) + "m" : "") }
  let days = (hours / 24).rounded(.down)
  let remainingHours = hours.truncatingRemainder(dividingBy: 24)
  return jsString(days) + "d" + (remainingHours > 0 ? jsString(remainingHours) + "h" : "")
}

/// The bar's coarse countdown: "21m", "30h", "6d".
public func formatTimeLeft(_ ms: Double?) -> String {
  guard let ms else { return "" }
  let minutes = max(0, (ms / 60000).rounded(.up))
  if minutes < 60 { return jsString(minutes) + "m" }
  let hours = (minutes / 60).rounded(.down)
  if hours < 48 { return jsString(hours) + "h" }
  return jsString((hours / 24).rounded(.down)) + "d"
}

/// The popup's countdown: "6d 20h", "3h 2m", "45m", "12s".
public func formatDuration(_ ms: Double?) -> String {
  guard let ms else { return "—" }
  var seconds = max(0, (ms / 1000).rounded(.down))
  let days = (seconds / 86400).rounded(.down)
  seconds = seconds.truncatingRemainder(dividingBy: 86400)
  let hours = (seconds / 3600).rounded(.down)
  seconds = seconds.truncatingRemainder(dividingBy: 3600)
  let minutes = (seconds / 60).rounded(.down)
  if days > 0 { return jsString(days) + "d " + jsString(hours) + "h" }
  if hours > 0 { return jsString(hours) + "h " + jsString(minutes) + "m" }
  if minutes > 0 { return jsString(minutes) + "m" }
  return jsString(seconds) + "s"
}

let maxSafeInteger: Double = 9_007_199_254_740_991

/// Date.parse for the RFC 3339 timestamps usagent emits (Go may print up to nine
/// fractional digits; Date.parse keeps milliseconds). Unparseable or empty text
/// sorts last.
public func timestampFromText(_ value: String?) -> Double {
  guard let value, value != "" else { return maxSafeInteger }
  var whole = value
  var fractionMs: Double = 0
  if let parts = matches(value, #"^([^.]+)\.(\d+)(.*)$"#) {
    whole = parts[1] + parts[3]
    fractionMs = Double((parts[2] + "00").prefix(3)) ?? 0
  }
  let formatter = ISO8601DateFormatter()
  formatter.formatOptions = [.withInternetDateTime]
  guard let date = formatter.date(from: whole) else { return maxSafeInteger }
  return date.timeIntervalSince1970 * 1000 + fractionMs
}

func replacing(_ text: String, _ pattern: String, _ template: String, caseInsensitive: Bool = false) -> String {
  guard let regex = try? NSRegularExpression(pattern: pattern, options: caseInsensitive ? [.caseInsensitive] : []) else { return text }
  return regex.stringByReplacingMatches(in: text, range: NSRange(text.startIndex..., in: text), withTemplate: template)
}

func matches(_ text: String, _ pattern: String) -> [String]? {
  guard let regex = try? NSRegularExpression(pattern: pattern),
    let match = regex.firstMatch(in: text, range: NSRange(text.startIndex..., in: text))
  else { return nil }
  return (0..<match.numberOfRanges).map { index in
    Range(match.range(at: index), in: text).map { String(text[$0]) } ?? ""
  }
}

/// Redacts bearer tokens, API keys and token query parameters, then caps the
/// text at 180 characters.
public func sanitizeErrorText(_ value: String?) -> String {
  var text = value ?? ""
  text = replacing(text, #"Bearer\s+[A-Za-z0-9._~+/=:-]+"#, "Bearer …", caseInsensitive: true)
  text = replacing(text, #"sk-[A-Za-z0-9_-]{12,}"#, "sk-…")
  text = replacing(text, #"(api[_-]?key=)[^\s&]+"#, "$1…", caseInsensitive: true)
  text = replacing(text, #"(token=)[^\s&]+"#, "$1…", caseInsensitive: true)
  if text.count > 180 { text = String(text.prefix(177)) + "…" }
  return text
}
