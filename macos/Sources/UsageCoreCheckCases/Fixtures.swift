import Foundation
import UsageCore

// Fixtures ported from mono-agent tests/qml/usagent/tst_bar_widget.qml. Times are
// relative to a fixed `now` instead of Date.now().

let now: Double = 1_800_000_000_000
let minute: Double = 60_000
let hour: Double = 3_600_000
let day: Double = 24 * hour

func decode<T: Decodable>(_ type: T.Type, _ json: Any) -> T {
  let data = try! JSONSerialization.data(withJSONObject: json)
  return try! decodePayload(T.self, from: data)!
}

func overview(_ json: [String: Any]) -> Overview { decode(Overview.self, json) }

func usageFixture() -> [String: Any] {
  func item(_ id: String, _ provider: String, _ label: String, _ windowId: String, _ windowKind: String, _ used: Double) -> [String: Any] {
    [
      "id": id, "provider": provider, "label": label,
      "window": ["id": windowId, "kind": windowKind],
      "limit": 100, "used": used, "remaining": 100 - used, "percentUsed": used,
      "unit": "percent", "state": "fresh", "severity": "ok", "visible": true,
    ]
  }
  return [
    "providers": [
      ["id": "claude-code", "label": "Claude Code", "state": "fresh"],
      ["id": "chatgpt", "label": "ChatGPT", "state": "fresh"],
      ["id": "z-ai", "label": "z.ai", "state": "fresh"],
    ],
    "quotaItems": [
      item("claude-session", "claude-code", "Claude session", "session", "session", 21),
      item("claude-fable-weekly", "claude-code", "Fable weekly", "fableWeekly", "weekly", 27),
      item("chatgpt-weekly", "chatgpt", "ChatGPT weekly", "weekly", "weekly", 35),
      item("z-ai-session", "z-ai", "z.ai session", "session", "session", 51),
    ],
  ]
}

func cliproxyItem(
  _ account: String, _ kind: String, _ suffix: String, _ label: String, _ windowId: String,
  _ remaining: Double, _ resetInMs: Double? = nil, _ extra: [String: Any] = [:]
) -> [String: Any] {
  let windowLabels = ["session": "S", "weekly": "W", "fableWeekly": "F", "resetCredits": "Resets", "spendingCredits": "Credits"]
  var item: [String: Any] = [
    "id": "cliproxy-\(kind)-\(account)-\(suffix)",
    "provider": "cliproxy",
    "label": label,
    "window": ["id": windowId, "label": windowLabels[windowId]!, "kind": windowId],
    "unit": "percent",
    "limit": 100,
    "used": 100 - remaining,
    "remaining": remaining,
    "percentUsed": 100 - remaining,
    "state": "fresh",
    "severity": remaining <= 0 ? "critical" : (remaining <= 20 ? "warning" : "ok"),
    "visible": true,
  ]
  if let resetInMs { item["reset"] = ["resetAt": now + resetInMs] }
  item.merge(extra) { $1 }
  return item
}

func cliproxyFixture() -> [String: Any] {
  [
    "providers": [
      ["id": "cliproxy", "label": "CLIProxy", "state": "fresh"],
      ["id": "cursor", "label": "Cursor", "state": "fresh"],
    ],
    "quotaItems": [
      cliproxyItem("e29a", "claude", "oauth-session", "Claude c***@jordan.example 5h", "session", 100),
      cliproxyItem("e29a", "claude", "oauth-weekly-all", "Claude c***@jordan.example weekly", "weekly", 86, 6 * day + 8 * hour),
      cliproxyItem("e29a", "claude", "oauth-fable-weekly", "Claude c***@jordan.example Fable weekly", "fableWeekly", 100, 6 * day + 8 * hour),
      cliproxyItem("e29a", "claude", "rate-limit-reset-credits", "Claude c***@jordan.example resets", "resetCredits", 1, nil,
                   ["unit": "credits", "limit": 1, "used": 0, "percentUsed": 0, "expiresAt": now + 6 * day + 20 * hour + 30000]),
      cliproxyItem("6c7c", "claude", "oauth-session", "Claude j***@self.example 5h", "session", 12, 21 * minute),
      cliproxyItem("6c7c", "claude", "oauth-weekly-all", "Claude j***@self.example weekly", "weekly", 56, 4 * day + 6 * hour),
      cliproxyItem("6c7c", "claude", "oauth-fable-weekly", "Claude j***@self.example Fable weekly", "fableWeekly", 40, 4 * day + 6 * hour),
      cliproxyItem("8ca3", "codex", "primary", "Codex j***@self.example 5h", "session", 100),
      cliproxyItem("8ca3", "codex", "secondary", "Codex j***@self.example weekly", "weekly", 80, 5 * day + 10 * hour),
      cliproxyItem("8ca3", "codex", "rate-limit-reset-credits", "Codex j***@self.example resets", "resetCredits", 3, nil,
                   ["unit": "credits", "limit": 3, "used": 0, "percentUsed": 0, "expiresAt": now + 30 * hour + 30000]),
      cliproxyItem("baad", "codex", "primary", "Codex j***@gmail.example weekly", "weekly", 15, 30 * hour),
      cliproxyItem("baad", "codex", "spending-credits", "Codex j***@gmail.example spending credits", "spendingCredits", 62500, nil,
                   ["unit": "credits"]),
      [
        "id": "cursor-models", "provider": "cursor", "label": "Cursor Models",
        "window": ["id": "monthly", "label": "M", "kind": "monthly"],
        "unit": "percent", "remaining": 81, "percentUsed": 19, "state": "fresh", "severity": "ok", "visible": true,
        "reset": ["resetAt": now + 19 * day + 7 * hour],
      ] as [String: Any],
    ],
  ]
}

func settings(_ configure: (inout Settings) -> Void) -> Settings {
  var settings = Settings()
  configure(&settings)
  return settings
}
