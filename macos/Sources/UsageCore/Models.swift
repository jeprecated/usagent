import Foundation

// usagent payloads, decoded leniently like UsageStore reads them: every field is
// optional, numbers must be finite JSON numbers (strings are never coerced), and a
// field of the wrong type reads as missing instead of failing the whole payload.

public struct Overview: Decodable {
  public var quotaItems: [QuotaItem] = []
  public var providers: [ProviderMeta] = []

  public init() {}

  public init(from decoder: Decoder) throws {
    let c = try decoder.container(keyedBy: Key.self)
    quotaItems = c.lossyArray(.quotaItems)
    providers = c.lossyArray(.providers)
  }

  enum Key: String, CodingKey { case quotaItems, providers }
}

public struct QuotaItem: Decodable {
  public var id: String?
  public var provider: String?
  public var label: String?
  public var shortLabel: String?
  public var visible: Bool?
  public var state: String?
  public var severity: String?
  public var unit: String?
  public var remaining: Double?
  public var used: Double?
  public var limit: Double?
  public var percentUsed: Double?
  public var expiresAt: Double?
  public var window: QuotaWindow?
  public var reset: QuotaReset?
  public var refresh: QuotaRefresh?
  public var error: QuotaError?

  public init(from decoder: Decoder) throws {
    let c = try decoder.container(keyedBy: Key.self)
    id = c.lenient(.id)
    provider = c.lenient(.provider)
    label = c.lenient(.label)
    shortLabel = c.lenient(.shortLabel)
    visible = c.lenient(.visible)
    state = c.lenient(.state)
    severity = c.lenient(.severity)
    unit = c.lenient(.unit)
    remaining = c.number(.remaining)
    used = c.number(.used)
    limit = c.number(.limit)
    percentUsed = c.number(.percentUsed)
    expiresAt = c.number(.expiresAt)
    window = c.lenient(.window)
    reset = c.lenient(.reset)
    refresh = c.lenient(.refresh)
    error = c.lenient(.error)
  }

  enum Key: String, CodingKey {
    case id, provider, label, shortLabel, visible, state, severity, unit
    case remaining, used, limit, percentUsed, expiresAt, window, reset, refresh, error
  }
}

public struct QuotaWindow: Decodable {
  public var id: String?
  public var label: String?
  public var kind: String?
  public var resetAt: Double?

  public init(from decoder: Decoder) throws {
    let c = try decoder.container(keyedBy: Key.self)
    id = c.lenient(.id)
    label = c.lenient(.label)
    kind = c.lenient(.kind)
    resetAt = c.number(.resetAt)
  }

  enum Key: String, CodingKey { case id, label, kind, resetAt }
}

public struct QuotaReset: Decodable {
  public var resetAt: Double?

  public init(from decoder: Decoder) throws {
    let c = try decoder.container(keyedBy: Key.self)
    resetAt = c.number(.resetAt)
  }

  enum Key: String, CodingKey { case resetAt }
}

public struct QuotaRefresh: Decodable {
  public var staleAt: Double?
  public var nextRefreshAt: Double?
  public var lastUpdatedAt: Double?

  public init(from decoder: Decoder) throws {
    let c = try decoder.container(keyedBy: Key.self)
    staleAt = c.number(.staleAt)
    nextRefreshAt = c.number(.nextRefreshAt)
    lastUpdatedAt = c.number(.lastUpdatedAt)
  }

  enum Key: String, CodingKey { case staleAt, nextRefreshAt, lastUpdatedAt }
}

public struct QuotaError: Decodable {
  public var code: String?
  public var message: String?
  public var recoverable: Bool?

  public init(from decoder: Decoder) throws {
    let c = try decoder.container(keyedBy: Key.self)
    code = c.lenient(.code)
    message = c.lenient(.message)
    recoverable = c.lenient(.recoverable)
  }

  enum Key: String, CodingKey { case code, message, recoverable }
}

public struct ProviderMeta: Decodable {
  public var id: String?
  public var label: String?
  public var state: String?
  public var source: String?
  public var lastUpdatedAt: Double?

  public init(from decoder: Decoder) throws {
    let c = try decoder.container(keyedBy: Key.self)
    id = c.lenient(.id)
    label = c.lenient(.label)
    state = c.lenient(.state)
    source = c.lenient(.source)
    lastUpdatedAt = c.number(.lastUpdatedAt)
  }

  enum Key: String, CodingKey { case id, label, state, source, lastUpdatedAt }
}

public struct ExpiringUsage: Decodable {
  public var opportunities: [Opportunity] = []

  public init() {}

  public init(from decoder: Decoder) throws {
    let c = try decoder.container(keyedBy: Key.self)
    opportunities = c.lossyArray(.opportunities)
  }

  enum Key: String, CodingKey { case opportunities }
}

public struct Opportunity: Decodable {
  public var provider: String?
  public var itemId: String?
  public var unit: String?
  public var confidence: String?
  public var label: String?
  public var estimatedWastedAmount: Double?
  public var timeRemainingMs: Double?
  public var percentRemaining: Double?
  public var reasons: [String] = []

  public init(from decoder: Decoder) throws {
    let c = try decoder.container(keyedBy: Key.self)
    provider = c.lenient(.provider)
    itemId = c.lenient(.itemId)
    unit = c.lenient(.unit)
    confidence = c.lenient(.confidence)
    label = c.lenient(.label)
    estimatedWastedAmount = c.number(.estimatedWastedAmount)
    timeRemainingMs = c.number(.timeRemainingMs)
    percentRemaining = c.number(.percentRemaining)
    reasons = c.lossyArray(.reasons)
  }

  enum Key: String, CodingKey {
    case provider, itemId, unit, confidence, label, estimatedWastedAmount, timeRemainingMs, percentRemaining, reasons
  }
}

public struct ResetCredits: Decodable {
  public var availableCount: Double?
  public var credits: [ResetCredit] = []

  public init() {}

  public init(from decoder: Decoder) throws {
    let c = try decoder.container(keyedBy: Key.self)
    availableCount = c.number(.availableCount)
    credits = c.lossyArray(.credits)
  }

  enum Key: String, CodingKey { case availableCount, credits }
}

public struct ResetCredit: Decodable {
  public var status: String?
  public var expiresAt: String?
  public var title: String?

  public init(from decoder: Decoder) throws {
    let c = try decoder.container(keyedBy: Key.self)
    status = c.lenient(.status)
    expiresAt = c.lenient(.expiresAt)
    title = c.lenient(.title)
  }

  enum Key: String, CodingKey { case status, expiresAt, title }
}

/// Decodes a JSON payload the way UsageStore accepts one: invalid JSON throws, a
/// valid payload that is not an object returns nil (UsageStore ignores it).
public func decodePayload<T: Decodable>(_ type: T.Type, from data: Data) throws -> T? {
  let json = try JSONSerialization.jsonObject(with: data, options: [.fragmentsAllowed])
  guard json is [String: Any] else { return nil }
  return try JSONDecoder().decode(T.self, from: data)
}

private struct Lossy<T: Decodable>: Decodable {
  let value: T?
  init(from decoder: Decoder) throws { value = try? T(from: decoder) }
}

extension KeyedDecodingContainer {
  func lenient<T: Decodable>(_ key: Key) -> T? {
    (try? decodeIfPresent(T.self, forKey: key)) ?? nil
  }

  func number(_ key: Key) -> Double? {
    guard let value: Double = lenient(key), value.isFinite else { return nil }
    return value
  }

  func lossyArray<T: Decodable>(_ key: Key) -> [T] {
    let items: [Lossy<T>]? = lenient(key)
    return (items ?? []).compactMap(\.value)
  }
}
