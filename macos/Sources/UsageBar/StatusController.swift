import AppKit
import Combine
import SwiftUI
import UsageCore

extension NSColor {
  convenience init(_ rgba: RGBA) {
    self.init(srgbRed: rgba.red, green: rgba.green, blue: rgba.blue, alpha: rgba.alpha)
  }
}

/// The menu bar item. Its title is an attributed string so each part keeps its
/// colour (SwiftUI MenuBarExtra labels render as monochrome templates). Clicking
/// it toggles the detail popover.
@MainActor
final class StatusController: NSObject {
  private let store: Store
  private let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
  private let popover = NSPopover()
  private var subscription: AnyCancellable?

  init(store: Store) {
    self.store = store
    super.init()

    let hosting = NSHostingController(rootView: PopupView(store: store))
    hosting.sizingOptions = .preferredContentSize
    popover.contentViewController = hosting
    popover.behavior = .transient
    popover.appearance = NSAppearance(named: .darkAqua)

    item.button?.target = self
    item.button?.action = #selector(togglePopover(_:))
    subscription = store.$model.sink { [weak self] model in self?.render(model) }
  }

  @objc private func togglePopover(_ sender: Any?) {
    guard let button = item.button else { return }
    if popover.isShown {
      popover.performClose(sender)
    } else {
      NSApp.activate(ignoringOtherApps: true)
      popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
      popover.contentViewController?.view.window?.makeKey()
    }
  }

  private func render(_ model: UsageModel) {
    guard let button = item.button else { return }
    button.attributedTitle = Self.title(model, Palette(colors: model.settings.colors))
    // On a failed fetch the bar keeps the last values, dimmed.
    button.alphaValue = model.error == "" ? 1 : 0.5
    button.toolTip = model.error == "" ? nil : model.error
  }

  /// BarWidget's compact row: "AI: ", then segments joined by " · ", label parts
  /// in the kind colour, non-ok values in their severity colour, times dimmed.
  static func title(_ model: UsageModel, _ palette: Palette) -> NSAttributedString {
    let size = NSFont.menuBarFont(ofSize: 0).pointSize
    let strong = NSFont.monospacedDigitSystemFont(ofSize: size, weight: .semibold)
    let medium = NSFont.monospacedDigitSystemFont(ofSize: size, weight: .medium)
    // The menu bar's own label colour keeps neutral text legible on light and
    // dark menu bars; an explicit `colors.text` setting overrides it.
    let text = model.settings.colors["text"] != nil ? NSColor(palette.text) : NSColor.labelColor
    let title = NSMutableAttributedString()
    func append(_ string: String, _ color: NSColor, _ font: NSFont, _ opacity: CGFloat = 1) {
      title.append(NSAttributedString(string: string, attributes: [
        .foregroundColor: opacity < 1 ? color.withAlphaComponent(opacity) : color,
        .font: font,
      ]))
    }

    append("AI: ", text, medium, 0.88)
    if model.compactBarSegments.isEmpty { append("—", text, medium, 0.68) }
    for (index, segment) in model.compactBarSegments.enumerated() {
      if index > 0 { append(" · ", text, medium, 0.54) }
      for (partIndex, part) in segment.parts.enumerated() {
        let string = (partIndex > 0 ? " " : "") + part.text
        switch part.role {
        case .label:
          append(string, palette.kind(segment.kind).map(NSColor.init) ?? text, strong)
        case .value:
          append(string, part.severity == .ok ? text : NSColor(palette.severity(part.severity)), strong)
        case .time:
          append(string, text, medium, 0.7)
        }
      }
    }
    return title
  }
}
