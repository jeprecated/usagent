import Foundation

// UsageCore's checks run both as XCTest (`swift test`, which needs Xcode's XCTest)
// and as the UsageCoreChecks executable (the Nix check phase: nixpkgs' SwiftPM
// cannot run XCTest on Darwin). They use these small assertions instead of XCTest.

public struct Check {
  public let name: String
  public let run: () throws -> Void

  public init(_ name: String, _ run: @escaping () throws -> Void) {
    self.name = name
    self.run = run
  }
}

public struct CheckFailure: Error, CustomStringConvertible {
  public let description: String
}

func fail(_ text: String, _ message: String, _ file: StaticString, _ line: UInt) -> CheckFailure {
  CheckFailure(description: "\(file):\(line): \(text)" + (message == "" ? "" : " — \(message)"))
}

func expectEqual<T: Equatable>(
  _ actual: T, _ expected: T, _ message: String = "", file: StaticString = #fileID, line: UInt = #line
) throws {
  if actual != expected { throw fail("expected \(expected), got \(actual)", message, file, line) }
}

func expectEqual(
  _ actual: Double, _ expected: Double, accuracy: Double, file: StaticString = #fileID, line: UInt = #line
) throws {
  if abs(actual - expected) > accuracy { throw fail("expected \(expected) ± \(accuracy), got \(actual)", "", file, line) }
}

func expect(_ condition: Bool, _ message: String = "", file: StaticString = #fileID, line: UInt = #line) throws {
  if !condition { throw fail("expected true", message, file, line) }
}

func expectFalse(_ condition: Bool, _ message: String = "", file: StaticString = #fileID, line: UInt = #line) throws {
  if condition { throw fail("expected false", message, file, line) }
}

func expectNil<T>(_ value: T?, file: StaticString = #fileID, line: UInt = #line) throws {
  if let value { throw fail("expected nil, got \(value)", "", file, line) }
}

func expectThrows<T>(_ body: @autoclosure () throws -> T, file: StaticString = #fileID, line: UInt = #line) throws {
  do {
    _ = try body()
  } catch {
    return
  }
  throw fail("expected an error", "", file, line)
}

func unwrap<T>(_ value: T?, file: StaticString = #fileID, line: UInt = #line) throws -> T {
  guard let value else { throw fail("unexpected nil", "", file, line) }
  return value
}
