import Foundation

extension UsageModel {
  /// A plain-text rendering of the popup, in the same order as PopupView, for
  /// `UsageBar --dump`.
  public var popupText: String {
    var lines = ["AI usage quotas"]
    if error != "" { lines.append("error: " + error) }
    lines.append(compactBarText)
    lines.append("severity: " + severity.name)

    for group in providerGroups {
      lines.append("")
      lines.append(group.metadataText == "" ? group.label : group.label + "  " + group.metadataText)
      for account in group.accounts {
        let worst = quotaRuntimeSeverity(account.worstItem)
        var heading = "  [" + account.kind + "]"
        if account.pooled || group.accounts.count > 1 { heading += " " + [account.title, account.name].filter { $0 != "" }.joined(separator: " ") }
        if worst != .ok { heading += " (" + worst.name + ")" }
        lines.append(heading)
        for meter in account.meters {
          let label = pad(meterLabel(meter, account), 16)
          let reset = pad(meterResetText(meter), 20)
          lines.append("    " + label + " " + reset + " " + meterValueText(meter) + " " + bar(meterPercent(meter)))
          let errorText = quotaErrorText(meter)
          if errorText != "" { lines.append("      error: " + errorText) }
        }
        if !account.credits.isEmpty {
          lines.append("    " + account.credits.map { "(" + creditChipText($0) + ")" }.joined(separator: " "))
        }
      }
    }

    if !visibleExpiringOpportunities.isEmpty {
      lines.append("")
      lines.append("Use soon")
      for opportunity in visibleExpiringOpportunities {
        lines.append("  " + expiringOpportunityText(opportunity))
        lines.append("    " + expiringOpportunityDetailText(opportunity))
      }
    }

    if !visibleResetCredits.isEmpty {
      lines.append("")
      lines.append(resetCreditsSummaryText)
      for credit in visibleResetCredits { lines.append("  " + resetCreditText(credit)) }
    }

    if isEmpty {
      lines.append("")
      lines.append(error != "" ? error : "No quota data")
    }
    return lines.joined(separator: "\n")
  }

  private func pad(_ text: String, _ width: Int) -> String {
    text + String(repeating: " ", count: max(0, width - text.count))
  }

  private func bar(_ percent: Double) -> String {
    let filled = Int(jsRound(percent / 10))
    return "[" + String(repeating: "#", count: filled) + String(repeating: ".", count: 10 - filled) + "]"
  }
}
