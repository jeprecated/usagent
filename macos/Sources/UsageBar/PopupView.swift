import AppKit
import SwiftUI
import UsageCore

extension Color {
  init(_ rgba: RGBA) {
    self.init(.sRGB, red: rgba.red, green: rgba.green, blue: rgba.blue, opacity: rgba.alpha)
  }
}

/// A port of UsageDetailPopup: header, summary line, per-provider account cards
/// with meters and credit chips, "Use soon" opportunities and reset credits.
struct PopupView: View {
  @ObservedObject var store: Store
  @State private var contentHeight: CGFloat = 0

  var body: some View {
    let model = store.model
    let palette = Palette(colors: model.settings.colors)
    let width = CGFloat(model.settings.popoverWidth)
    let innerWidth = width - 24

    VStack(alignment: .leading, spacing: 8) {
      header
      if model.error != "", !model.isEmpty {
        Text("error: " + model.error)
          .font(.system(size: 11))
          .foregroundColor(Color(palette.error))
          .lineLimit(2)
      }
      Text(model.compactBarText)
        .font(.system(size: 11, weight: .medium))
        .opacity(0.72)
        .lineLimit(1)
        .truncationMode(.tail)

      if model.isEmpty {
        Text(model.error != "" ? model.error : "No quota data")
          .font(.system(size: 12))
          .opacity(0.72)
          .multilineTextAlignment(.center)
          .frame(maxWidth: .infinity, minHeight: 96)
      } else {
        ScrollView(.vertical) {
          content(model, palette, innerWidth)
            .background(GeometryReader { proxy in
              Color.clear.preference(key: ContentHeightKey.self, value: proxy.size.height)
            })
        }
        .frame(height: min(contentHeight, max(76, CGFloat(model.settings.popoverMaxHeight) - 80)))
        .onPreferenceChange(ContentHeightKey.self) { contentHeight = $0 }
      }
    }
    .padding(12)
    .frame(width: width, alignment: .topLeading)
    .foregroundColor(Color(palette.text))
    .background(Color(palette.popupBackground))
  }

  private var header: some View {
    HStack(spacing: 8) {
      Text("AI usage quotas")
        .font(.system(size: 13, weight: .semibold))
      Spacer()
      Button { store.refresh() } label: { Image(systemName: "arrow.clockwise") }
        .help("Refresh")
        .keyboardShortcut("r")
      Button("Quit") { NSApp.terminate(nil) }
        .keyboardShortcut("q")
    }
    .buttonStyle(.borderless)
  }

  private func content(_ model: UsageModel, _ palette: Palette, _ width: CGFloat) -> some View {
    VStack(alignment: .leading, spacing: 10) {
      ForEach(Array(model.providerGroups.enumerated()), id: \.offset) { _, group in
        VStack(alignment: .leading, spacing: 6) {
          HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text(group.label)
              .font(.system(size: 12, weight: .semibold))
              .opacity(0.88)
            Text(group.metadataText)
              .font(.system(size: 10))
              .opacity(0.58)
              .lineLimit(1)
              .truncationMode(.tail)
          }
          ForEach(Array(group.accounts.enumerated()), id: \.offset) { _, account in
            AccountCard(model: model, palette: palette, group: group, account: account, width: width)
          }
        }
      }

      if !model.visibleExpiringOpportunities.isEmpty {
        VStack(alignment: .leading, spacing: 6) {
          Text("Use soon")
            .font(.system(size: 12, weight: .semibold))
            .opacity(0.88)
          ForEach(Array(model.visibleExpiringOpportunities.enumerated()), id: \.offset) { _, opportunity in
            VStack(alignment: .leading, spacing: 3) {
              Text(model.expiringOpportunityText(opportunity))
                .font(.system(size: 12, weight: .medium))
                .foregroundColor(Color(palette.warning))
              Text(model.expiringOpportunityDetailText(opportunity))
                .font(.system(size: 11))
                .opacity(0.68)
            }
            .lineLimit(1)
            .truncationMode(.tail)
            .padding(.horizontal, 10)
            .padding(.vertical, 6)
            .frame(width: width, alignment: .leading)
            .background(RoundedRectangle(cornerRadius: 9).fill(Color.white.opacity(0.045)))
            .overlay(RoundedRectangle(cornerRadius: 9).stroke(Color(palette.warning), lineWidth: 1))
          }
        }
      }

      if !model.visibleResetCredits.isEmpty {
        VStack(alignment: .leading, spacing: 6) {
          Text(model.resetCreditsSummaryText)
            .font(.system(size: 12, weight: .semibold))
            .opacity(0.88)
          FlowLayout(spacing: 8) {
            ForEach(Array(model.visibleResetCredits.enumerated()), id: \.offset) { _, credit in
              Text(model.resetCreditText(credit))
                .font(.system(size: 11))
                .opacity(0.72)
            }
          }
        }
      }
    }
    .frame(width: width, alignment: .leading)
  }
}

private struct AccountCard: View {
  let model: UsageModel
  let palette: Palette
  let group: ProviderGroup
  let account: Account
  let width: CGFloat

  var body: some View {
    let kindColor = palette.kind(account.kind).map(Color.init) ?? Color(palette.text)
    let innerWidth = width - 22
    let border = model.quotaRuntimeSeverity(account.worstItem) != .ok
      ? Color(palette.quota(account.worstItem, in: model)) : Color(palette.popupBorder)

    VStack(alignment: .leading, spacing: 5) {
      if account.pooled || group.accounts.count > 1 {
        HStack(spacing: 6) {
          Text(account.title)
            .font(.system(size: 12, weight: .semibold))
            .foregroundColor(kindColor)
          Text(account.name)
            .font(.system(size: 12, weight: .medium))
            .lineLimit(1)
            .truncationMode(.tail)
        }
      }

      ForEach(Array(account.meters.enumerated()), id: \.offset) { _, meter in
        MeterRow(model: model, palette: palette, account: account, meter: meter, width: innerWidth)
      }

      if !account.credits.isEmpty {
        FlowLayout(spacing: 6) {
          ForEach(Array(account.credits.enumerated()), id: \.offset) { _, credit in
            Text(model.creditChipText(credit))
              .font(.system(size: 10))
              .opacity(0.78)
              .padding(.horizontal, 6)
              .padding(.vertical, 2)
              .overlay(Capsule().stroke(Color(palette.popupBorder), lineWidth: 1))
          }
        }
        .frame(width: innerWidth, alignment: .leading)
      }
    }
    .padding(.leading, 12)
    .padding(.trailing, 10)
    .padding(.vertical, 8)
    .frame(width: width, alignment: .leading)
    .background(RoundedRectangle(cornerRadius: 9).fill(Color.white.opacity(0.045)))
    .overlay(RoundedRectangle(cornerRadius: 9).stroke(border, lineWidth: 1))
    .overlay(alignment: .leading) {
      RoundedRectangle(cornerRadius: 1.5)
        .fill(kindColor)
        .frame(width: 3)
        .padding(.vertical, 8)
        .padding(.leading, 1)
    }
  }
}

private struct MeterRow: View {
  let model: UsageModel
  let palette: Palette
  let account: Account
  let meter: QuotaItem
  let width: CGFloat

  var body: some View {
    let quotaColor = Color(palette.quota(meter, in: model))
    let errorText = model.quotaErrorText(meter)

    VStack(alignment: .leading, spacing: 3) {
      HStack(spacing: 8) {
        Text(model.meterLabel(meter, account))
          .font(.system(size: 11, weight: .medium))
          .opacity(0.88)
          .frame(width: width * 0.4, alignment: .leading)
        Text(model.meterResetText(meter))
          .font(.system(size: 11).monospacedDigit())
          .opacity(0.62)
          .frame(width: width * 0.46, alignment: .trailing)
        Text(model.meterValueText(meter))
          .font(.system(size: 11, weight: .semibold).monospacedDigit())
          .foregroundColor(model.quotaRuntimeSeverity(meter) != .ok ? quotaColor : Color(palette.text))
          .frame(width: max(0, width * 0.14 - 16), alignment: .trailing)
      }
      .lineLimit(1)
      .truncationMode(.tail)

      ZStack(alignment: .leading) {
        Capsule().fill(Color.white.opacity(0.1))
        Capsule().fill(quotaColor).frame(width: width * model.meterPercent(meter) / 100)
      }
      .frame(width: width, height: 4)

      if errorText != "" {
        Text("error: " + errorText)
          .font(.system(size: 11))
          .foregroundColor(Color(palette.error))
          .opacity(0.9)
          .lineLimit(2)
          .fixedSize(horizontal: false, vertical: true)
      }
    }
  }
}

private struct ContentHeightKey: PreferenceKey {
  static var defaultValue: CGFloat = 0
  static func reduce(value: inout CGFloat, nextValue: () -> CGFloat) { value = max(value, nextValue()) }
}

/// Lays children out left to right, wrapping onto new rows (QML's Flow).
private struct FlowLayout: Layout {
  var spacing: CGFloat

  func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
    let rows = arrange(subviews, maxWidth: proposal.width ?? .infinity)
    return CGSize(width: rows.width, height: rows.height)
  }

  func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
    let rows = arrange(subviews, maxWidth: bounds.width)
    for (index, origin) in rows.origins.enumerated() {
      subviews[index].place(at: CGPoint(x: bounds.minX + origin.x, y: bounds.minY + origin.y), proposal: .unspecified)
    }
  }

  private func arrange(_ subviews: Subviews, maxWidth: CGFloat) -> (origins: [CGPoint], width: CGFloat, height: CGFloat) {
    var origins: [CGPoint] = []
    var x: CGFloat = 0
    var y: CGFloat = 0
    var rowHeight: CGFloat = 0
    var width: CGFloat = 0
    for subview in subviews {
      let size = subview.sizeThatFits(.unspecified)
      if x > 0, x + size.width > maxWidth {
        x = 0
        y += rowHeight + spacing
        rowHeight = 0
      }
      origins.append(CGPoint(x: x, y: y))
      width = max(width, x + size.width)
      rowHeight = max(rowHeight, size.height)
      x += size.width + spacing
    }
    return (origins, width, y + rowHeight)
  }
}
