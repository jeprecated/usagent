import Foundation
import UsageCoreCheckCases

// Runs every UsageCore check and exits non-zero if any fails.
var failures = 0
for check in allChecks {
  do {
    try check.run()
    print("ok   \(check.name)")
  } catch {
    failures += 1
    print("FAIL \(check.name): \(error)")
  }
}
print("\(allChecks.count - failures)/\(allChecks.count) checks passed")
exit(failures == 0 ? 0 : 1)
