import Foundation
import UsageCore

enum GoldenChecks {
  // tst_bar_widget.qml: test_compact_pill_shows_headline_window_and_lower_fable
  static func testCompactBarShowsHeadlineWindowAndLowerFable() throws {
    let model = UsageModel(
      overview: overview(usageFixture()),
      settings: settings {
        $0.providers = ["claude-code", "chatgpt", "z-ai"]
        $0.windows = ["session", "weekly", "fableWeekly", "monthly", "resetCredits"]
      },
      now: now
    )
    try expectEqual(model.compactBarText, "AI: C S 79% F73% · GPT 65% · Z S 49%")
  }

  // tst_bar_widget.qml: test_compact_pill_splits_cliproxy_accounts
  static func testCompactBarSplitsCliproxyAccounts() throws {
    let model = UsageModel(
      overview: overview(cliproxyFixture()),
      settings: settings { $0.providers = ["cliproxy", "cursor"] },
      now: now
    )
    try expectEqual(
      model.compactBarText,
      "AI: C·c 86% 6d · C·j 56% F40% 4d S 12%! 21m · X·j1 80% 5d · X·j2 15%! 30h · CUR M 81% 19d"
    )
    let segments = model.compactBarSegments
    try expectEqual(segments[0].kind, "claude")
    try expectEqual(segments[0].parts[0].role, .label)
    try expectEqual(segments[1].severity, .warning)
    try expectEqual(segments[2].kind, "codex")
    try expectEqual(segments[1].parts.map(\.role), [.label, .value, .value, .time, .value])
  }

  // tst_bar_widget.qml: test_cliproxy_accounts_group_meters_and_credits
  static func testCliproxyAccountsGroupMetersAndCredits() throws {
    let model = UsageModel(
      overview: overview(cliproxyFixture()),
      settings: settings { $0.providers = ["cliproxy"] },
      now: now
    )
    try expectEqual(model.providerGroups.count, 1)
    let accounts = model.providerGroups[0].accounts
    try expectEqual(accounts.count, 4)
    try expectEqual(accounts[0].title, "Claude")
    try expectEqual(accounts[0].name, "c***@jordan.example")
    try expectEqual(accounts[0].meters.count, 3)
    try expectEqual(model.meterLabel(accounts[0].meters[0], accounts[0]), "5-hour")
    try expectEqual(model.meterLabel(accounts[0].meters[2], accounts[0]), "Fable weekly")
    try expectEqual(accounts[0].credits.count, 1)
    try expectEqual(model.creditChipText(accounts[0].credits[0]), "Resets 1 · expires in 6d 20h")
    try expectEqual(model.creditChipText(accounts[2].credits[0]), "Resets 3 · next expires in 1d 6h")
    try expectEqual(model.creditChipText(accounts[3].credits[0]), "Credits 62.5k")
  }
}

enum ModelChecks {
  static func testEmptyOverview() throws {
    let model = UsageModel(overview: Overview(), now: now, error: "usage endpoint unavailable (503)")
    try expectEqual(model.compactBarText, "AI: —")
    try expect(model.isEmpty)
    try expect(model.popupText.hasSuffix("usage endpoint unavailable (503)"))
  }

  static func testShortLabelsAndMeterLabels() throws {
    let model = UsageModel(overview: overview(cliproxyFixture()), settings: settings { $0.providers = ["cliproxy"] }, now: now)
    let accounts = model.providerGroups[0].accounts
    try expectEqual(accounts.map(\.shortLabel), ["C·c", "C·j", "X·j1", "X·j2"])
    try expect(accounts.allSatisfy(\.pooled))
    try expectEqual(accounts[0].meters.map { model.meterLabel($0, accounts[0]) }, ["5-hour", "Weekly", "Fable weekly"])
    try expectEqual(model.meterResetText(accounts[1].meters[0]), "resets in 21m")
    try expectEqual(model.meterResetText(accounts[1].meters[1]), "resets in 4d 6h")
    try expectEqual(model.meterValueText(accounts[1].meters[0]), "12%")
    try expectEqual(model.severity, .warning)
  }

  static func testStaleAndErrorItems() throws {
    let json: [String: Any] = [
      "quotaItems": [
        [
          "id": "claude-weekly", "provider": "claude-code", "label": "Claude weekly", "window": ["id": "weekly"],
          "unit": "percent", "remaining": 40, "state": "fresh", "severity": "ok",
          "refresh": ["staleAt": now - 1, "nextRefreshAt": now + 90_000],
          "error": ["code": "rate_limited", "message": "429 retry later", "recoverable": true],
        ],
        [
          "id": "chatgpt-weekly", "provider": "chatgpt", "label": "ChatGPT weekly", "window": ["id": "weekly"],
          "state": "error", "error": ["message": "Bearer abc.def-123 rejected"],
        ],
      ],
    ]
    let model = UsageModel(overview: overview(json), now: now)
    try expectEqual(model.compactBarText, "AI: C 40%! · GPT err")
    try expectEqual(model.severity, .error)
    let claude = model.providerGroups[0].accounts[0].meters[0]
    try expectEqual(model.quotaRuntimeState(claude), "stale")
    try expectEqual(model.quotaErrorText(claude), "", "a recoverable rate limit on a stale item is refresh-only")
    try expectEqual(model.providerGroups[0].metadataText, "refreshes in 1m · stale now")
    let chatgpt = model.providerGroups[1].accounts[0].meters[0]
    try expectEqual(model.meterValueText(chatgpt), "error")
    try expectEqual(model.quotaErrorText(chatgpt), "Bearer … rejected")
  }

  static func testWindowGatingAndModelDedupe() throws {
    let json: [String: Any] = [
      "quotaItems": [
        ["id": "z-ai-web", "provider": "z-ai", "label": "z.ai web search", "unit": "percent", "remaining": 10],
        ["id": "z-ai-model-session", "provider": "z-ai", "label": "GLM model 5h", "unit": "percent", "remaining": 20],
        ["id": "z-ai-session", "provider": "z-ai", "label": "z.ai 5h", "unit": "percent", "remaining": 60],
        ["id": "hidden", "provider": "z-ai", "label": "hidden weekly", "unit": "percent", "remaining": 1, "visible": false],
      ],
    ]
    let model = UsageModel(overview: overview(json), now: now)
    try expectEqual(model.providerGroups[0].items.map(\.id), ["z-ai-session"])
    try expectEqual(model.compactBarText, "AI: Z S 60%")
  }

  static func testExpiringOpportunitiesAndResetCredits() throws {
    let expiring = decode(ExpiringUsage.self, [
      "opportunities": [
        [
          "provider": "chatgpt", "itemId": "chatgpt-weekly", "estimatedWastedAmount": 35, "unit": "percent",
          "timeRemainingMs": 5 * hour + 30 * minute, "confidence": "high", "reasons": ["weekly window resets soon"],
        ],
        ["provider": "cursor", "itemId": "cursor-models", "percentRemaining": 81],
        ["provider": "z-ai", "percentRemaining": 49],
      ],
    ])
    let credits = decode(ResetCredits.self, [
      "credits": [
        ["title": "Later credit", "expiresAt": "2027-01-31T00:00:00Z"],
        ["title": "Redeemed", "status": "redeemed", "expiresAt": "2027-01-01T00:00:00Z"],
        ["status": "available", "expiresAt": ISO8601DateFormatter().string(from: Date(timeIntervalSince1970: (now + 2 * day) / 1000))],
      ],
    ])
    let model = UsageModel(
      overview: overview(usageFixture()), expiringUsage: expiring, resetCredits: credits, now: now
    )
    try expectEqual(model.visibleExpiringOpportunities.count, 2)
    try expectEqual(model.expiringOpportunityText(model.visibleExpiringOpportunities[0]), "GPT W 35% may expire in 5h30m · high")
    try expectEqual(model.expiringOpportunityDetailText(model.visibleExpiringOpportunities[0]), "weekly window resets soon")
    try expectEqual(model.expiringOpportunityText(model.visibleExpiringOpportunities[1]), "Z quota may expire in soon")
    try expectEqual(model.expiringOpportunityDetailText(model.visibleExpiringOpportunities[1]), "49% remaining")
    try expectEqual(model.visibleResetCredits.map { model.resetCreditText($0) }.first, "reset credit · expires in 2d")
    try expectEqual(model.visibleResetCredits.count, 2)
    try expectEqual(model.resetCreditsSummaryText, "ChatGPT reset credits · 2 available")
  }

  static func testPopupTextListsAccountsAndMeters() throws {
    let model = UsageModel(overview: overview(cliproxyFixture()), settings: settings { $0.providers = ["cliproxy", "cursor"] }, now: now)
    let text = model.popupText
    try expect(text.contains("CLIProxy  fresh"))
    try expect(text.contains("[claude] Claude c***@jordan.example"))
    try expect(text.contains("(Resets 1 · expires in 6d 20h)"))
    try expect(text.contains("Models"))
  }
}

enum DecodingChecks {
  static func testWrongTypesReadAsMissing() throws {
    let data = Data(#"{"quotaItems":[{"id":"a","provider":"chatgpt","remaining":"50","percentUsed":25,"window":"weekly"},null,3],"providers":"x"}"#.utf8)
    let overview = try unwrap(try decodePayload(Overview.self, from: data))
    try expectEqual(overview.quotaItems.count, 1)
    try expectNil(overview.quotaItems[0].remaining)
    try expectEqual(overview.quotaItems[0].percentUsed, 25)
    try expectNil(overview.quotaItems[0].window)
    try expect(overview.providers.isEmpty)
  }

  static func testNonObjectPayloadIsIgnoredAndInvalidJSONThrows() throws {
    try expectNil(try decodePayload(Overview.self, from: Data("[1,2]".utf8)))
    try expectThrows(try decodePayload(Overview.self, from: Data("{".utf8)))
  }
}

enum FormatChecks {
  static func testPercentAndNumbers() throws {
    try expectEqual(formatPercentValue(62.5), "63%")
    try expectEqual(formatPercentValue(-0.4), "0%")
    try expectEqual(formatPercentValue(140), "100%")
    try expectEqual(formatPercentValue(nil), "—")
    try expectEqual(formatCompactNumber(62_500), "62.5k")
    try expectEqual(formatCompactNumber(1_250_000), "1.3M")
    try expectEqual(formatCompactNumber(12.5), "13")
    try expectEqual(formatCompactNumber(2.25), "2.3")
    try expectEqual(formatCompactNumber(-2.5), "-2.5")
    try expectEqual(formatCompactQuotaValue(12.3, "usd"), "$12")
    try expectEqual(formatCompactQuotaValue(3, "credits"), "3 credits")
    try expectEqual(formatCompactQuotaValue(40, " Percent "), "40%")
  }

  static func testDurations() throws {
    try expectEqual(formatCompactDuration(0), "0m")
    try expectEqual(formatCompactDuration(59 * minute + 1), "1h")
    try expectEqual(formatCompactDuration(5 * hour + 30 * minute), "5h30m")
    try expectEqual(formatCompactDuration(6 * hour + 30 * minute), "6h")
    try expectEqual(formatCompactDuration(6 * day + 20 * hour), "6d20h")
    try expectEqual(formatTimeLeft(21 * minute), "21m")
    try expectEqual(formatTimeLeft(47 * hour + 1), "47h")
    try expectEqual(formatTimeLeft(48 * hour), "2d")
    try expectEqual(formatTimeLeft(30 * hour), "30h")
    try expectEqual(formatDuration(6 * day + 20 * hour + 30000), "6d 20h")
    try expectEqual(formatDuration(3 * hour + 2 * minute), "3h 2m")
    try expectEqual(formatDuration(12_999), "12s")
  }

  static func testTimestampsAndSanitizing() throws {
    try expectEqual(timestampFromText("2026-10-19T12:00:00Z"), 1_792_411_200_000)
    try expectEqual(timestampFromText("2026-10-19T12:00:00.123456789Z"), 1_792_411_200_123)
    try expectEqual(timestampFromText("2026-10-19T14:00:00.5+02:00"), 1_792_411_200_500)
    try expectEqual(timestampFromText("soon"), 9_007_199_254_740_991)
    try expectEqual(timestampFromText(nil), 9_007_199_254_740_991)
    try expectEqual(sanitizeErrorText("bad sk-abcdefghijklmnop key"), "bad sk-… key")
    try expectEqual(sanitizeErrorText("GET /x?api_key=s3cret&token=t0k"), "GET /x?api_key=…&token=…")
    try expectEqual(sanitizeErrorText(String(repeating: "x", count: 200)).count, 178)
  }

  static func testPalette() throws {
    try expectEqual(RGBA(hex: "#e60f1117")?.alpha ?? 0, 230.0 / 255, accuracy: 1e-9)
    try expectEqual(RGBA(hex: "#ff0000"), RGBA(hex: "#ffff0000"))
    let palette = Palette(colors: ["chatgpt": "#010203"])
    try expectEqual(palette.kind("codex"), RGBA(hex: "#010203"))
    try expectEqual(palette.kind("claude"), RGBA(hex: "#f5a97f"))
    try expectNil(palette.kind("antigravity"))
  }
}

enum SettingsChecks {
  static func testDefaultsAndUserDefaults() throws {
    try expectEqual(Settings().usageUrl, "http://lattice.tail6e682f.ts.net:8788/v1/usage")
    try expectEqual(Settings().providers, ["claude-code", "chatgpt", "openai", "z-ai"])

    // The registration domain lives in memory only, so the check writes no plist.
    let defaults = try unwrap(UserDefaults(suiteName: "dev.jeprecated.usagent-bar.checks"))
    defaults.register(defaults: [
      "usageUrl": "http://example.ts.net:8788/v1/usage?x=1#y",
      "providers": ["cliproxy", "cursor"],
      "showFableWeekly": false,
      "sessionBarThreshold": 15,
      "auxFetchIntervalMs": 1000,
      "windows": "not a list",
    ])
    let settings = Settings(defaults: defaults)
    try expectEqual(settings.providers, ["cliproxy", "cursor"])
    try expectFalse(settings.showFableWeekly)
    try expectEqual(settings.sessionBarThreshold, 15)
    try expectEqual(settings.auxIntervalMs, 30_000)
    try expectEqual(settings.windows, Settings().windows)
    try expectEqual(settings.resetCreditsEndpoint, "http://example.ts.net:8788/v1/chatgpt/reset-credits")
    try expectEqual(settings.expiringEndpoint, "http://example.ts.net:8788/v1/expiring-usage?within=168h&includeLowConfidence=true")
  }

  static func testDerivedEndpoints() throws {
    var settings = Settings()
    settings.usageUrl = "http://host:8788/"
    try expectEqual(settings.derivedEndpointUrl("/v1/chatgpt/reset-credits"), "http://host:8788/v1/chatgpt/reset-credits")
    settings.usageUrl = ""
    try expectEqual(settings.expiringEndpoint, "")
    settings.expiringUsageUrl = "http://other/expiring?a=1"
    settings.expiringWithin = "7 d"
    try expectEqual(settings.expiringEndpoint, "http://other/expiring?a=1&within=7%20d&includeLowConfidence=true")
  }
}

/// Every check, in source order. Add new checks here.
public let allChecks: [Check] = [
  Check("GoldenChecks.testCompactBarShowsHeadlineWindowAndLowerFable", GoldenChecks.testCompactBarShowsHeadlineWindowAndLowerFable),
  Check("GoldenChecks.testCompactBarSplitsCliproxyAccounts", GoldenChecks.testCompactBarSplitsCliproxyAccounts),
  Check("GoldenChecks.testCliproxyAccountsGroupMetersAndCredits", GoldenChecks.testCliproxyAccountsGroupMetersAndCredits),
  Check("ModelChecks.testEmptyOverview", ModelChecks.testEmptyOverview),
  Check("ModelChecks.testShortLabelsAndMeterLabels", ModelChecks.testShortLabelsAndMeterLabels),
  Check("ModelChecks.testStaleAndErrorItems", ModelChecks.testStaleAndErrorItems),
  Check("ModelChecks.testWindowGatingAndModelDedupe", ModelChecks.testWindowGatingAndModelDedupe),
  Check("ModelChecks.testExpiringOpportunitiesAndResetCredits", ModelChecks.testExpiringOpportunitiesAndResetCredits),
  Check("ModelChecks.testPopupTextListsAccountsAndMeters", ModelChecks.testPopupTextListsAccountsAndMeters),
  Check("DecodingChecks.testWrongTypesReadAsMissing", DecodingChecks.testWrongTypesReadAsMissing),
  Check("DecodingChecks.testNonObjectPayloadIsIgnoredAndInvalidJSONThrows", DecodingChecks.testNonObjectPayloadIsIgnoredAndInvalidJSONThrows),
  Check("FormatChecks.testPercentAndNumbers", FormatChecks.testPercentAndNumbers),
  Check("FormatChecks.testDurations", FormatChecks.testDurations),
  Check("FormatChecks.testTimestampsAndSanitizing", FormatChecks.testTimestampsAndSanitizing),
  Check("FormatChecks.testPalette", FormatChecks.testPalette),
  Check("SettingsChecks.testDefaultsAndUserDefaults", SettingsChecks.testDefaultsAndUserDefaults),
  Check("SettingsChecks.testDerivedEndpoints", SettingsChecks.testDerivedEndpoints),
]
