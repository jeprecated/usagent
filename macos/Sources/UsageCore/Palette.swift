import Foundation

public struct RGBA: Equatable {
  public var red, green, blue, alpha: Double

  /// Parses "#RRGGBB" or Qt's alpha-first "#AARRGGBB".
  public init?(hex: String) {
    var text = hex.trimmingCharacters(in: .whitespaces)
    if text.hasPrefix("#") { text.removeFirst() }
    guard text.count == 6 || text.count == 8, let value = UInt32(text, radix: 16) else { return nil }
    let hasAlpha = text.count == 8
    alpha = hasAlpha ? Double((value >> 24) & 0xFF) / 255 : 1
    red = Double((value >> 16) & 0xFF) / 255
    green = Double((value >> 8) & 0xFF) / 255
    blue = Double(value & 0xFF) / 255
  }
}

/// The widget's colours: manifest `colors` plus BarWidget's kind colours, each
/// overridable through the `colors` setting.
public struct Palette {
  static let defaults: [String: String] = [
    "background": "#e60f1117",
    "text": "#f1f5f9",
    "border": "#334155",
    "ok": "#8bd5ca",
    "warning": "#eed49f",
    "critical": "#f5a97f",
    "error": "#ed8796",
    "stale": "#cad3f5",
    "claudeCode": "#f5a97f",
    "chatgpt": "#8bd5ca",
    "openai": "#91d7e3",
    "zai": "#c6a0f6",
    "cursor": "#b7bdf8",
    "popupBackground": "#f00b0f14",
    "popupBorder": "#334155",
  ]

  // Pooled CLIProxy accounts use their upstream kind; direct providers their id.
  static let kindKeys: [String: String] = [
    "claude-code": "claudeCode",
    "claude": "claudeCode",
    "chatgpt": "chatgpt",
    "codex": "codex",
    "openai": "openai",
    "z-ai": "zai",
    "cursor": "cursor",
  ]

  let colors: [String: String]

  public init(colors: [String: String] = [:]) {
    self.colors = colors
  }

  public func color(_ name: String) -> RGBA {
    if let hex = colors[name], let color = RGBA(hex: hex) { return color }
    // BarWidget's codex colour falls back to the chatgpt colour.
    if name == "codex" { return color("chatgpt") }
    return RGBA(hex: Self.defaults[name] ?? Self.defaults["text"]!)!
  }

  public var text: RGBA { color("text") }
  public var border: RGBA { color("border") }
  public var warning: RGBA { color("warning") }
  public var error: RGBA { color("error") }
  public var popupBackground: RGBA { color("popupBackground") }
  public var popupBorder: RGBA { color("popupBorder") }

  /// The accent for an account kind; unknown kinds use the text colour.
  public func kind(_ kind: String) -> RGBA? {
    Self.kindKeys[kind].map(color)
  }

  public func severity(_ severity: Severity) -> RGBA {
    switch severity {
    case .error: return color("error")
    case .critical: return color("critical")
    case .warning: return color("warning")
    case .ok: return color("ok")
    }
  }

  /// UsageDetailPopup's colorForQuota: stale warnings use the stale colour.
  public func quota(_ item: QuotaItem?, in model: UsageModel) -> RGBA {
    let severity = model.quotaRuntimeSeverity(item)
    if severity == .warning, model.quotaRuntimeState(item) == "stale" { return color("stale") }
    return self.severity(severity)
  }
}
