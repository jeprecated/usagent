import Foundation

public enum FetchError: Error {
  /// A transport failure or a non-2xx status.
  case unavailable(String)
  /// The response was not valid JSON.
  case invalid(String)
}

/// Fetches usagent's JSON endpoints with a short timeout and no caching.
public struct UsageClient {
  let session: URLSession

  public init(timeout: TimeInterval = 5) {
    let configuration = URLSessionConfiguration.ephemeral
    configuration.timeoutIntervalForRequest = timeout
    configuration.timeoutIntervalForResource = timeout
    configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
    session = URLSession(configuration: configuration)
  }

  /// Returns nil when the payload is valid JSON but not an object.
  public func fetch<T: Decodable>(_ type: T.Type, from urlString: String) async throws -> T? {
    guard let url = URL(string: urlString) else { throw FetchError.unavailable("invalid URL") }
    let data: Data
    let response: URLResponse
    do {
      (data, response) = try await session.data(from: url)
    } catch {
      throw FetchError.unavailable(error.localizedDescription)
    }
    if let http = response as? HTTPURLResponse, !(200..<300).contains(http.statusCode) {
      throw FetchError.unavailable(String(http.statusCode))
    }
    do {
      return try decodePayload(T.self, from: data)
    } catch {
      throw FetchError.invalid(error.localizedDescription)
    }
  }

  /// UsageStore's text for a failed usage fetch.
  public static func usageErrorText(_ error: Error) -> String {
    switch error {
    case FetchError.unavailable(let reason): return "usage endpoint unavailable (\(reason))"
    case FetchError.invalid(let reason): return reason
    default: return String(describing: error)
    }
  }
}
