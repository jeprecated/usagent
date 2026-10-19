import UsageCoreCheckCases
import XCTest

final class UsageCoreTests: XCTestCase {
  func testChecks() {
    XCTAssertFalse(allChecks.isEmpty)
    for check in allChecks {
      XCTAssertNoThrow(try check.run(), check.name)
    }
  }
}
