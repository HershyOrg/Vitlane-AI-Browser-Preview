import CryptoKit
import Foundation

struct VitlanePurchaseApprovalContinuity: Equatable {
  let approvalId: String
  let approvalDigest: String
  let mode: String
}

struct VitlanePurchaseStepContinuity: Equatable {
  let approval: VitlanePurchaseApprovalContinuity
  let stepId: String
  let stepIndex: Int
  let stepCount: Int

  var expectedResultCode: String {
    switch stepId {
    case "select_option": return "COUPANG_OPTION_SELECTED"
    case "verify_options": return "COUPANG_OPTIONS_VERIFIED"
    case "set_quantity": return "COUPANG_QUANTITY_SET"
    case "add_to_cart": return "COUPANG_ADD_TO_CART_TRIGGERED"
    case "buy_now": return "COUPANG_BUY_NOW_TRIGGERED"
    case "start_checkout": return "COUPANG_CHECKOUT_TRIGGERED"
    default: return "POLICY_DENIED"
    }
  }
}

struct VitlanePurchaseSequenceState {
  private(set) var approval: VitlanePurchaseApprovalContinuity?
  private(set) var nextStepIndex = 0
  private(set) var activeStep: VitlanePurchaseStepContinuity?
  private(set) var blocked = false

  func validate(_ step: VitlanePurchaseStepContinuity) throws {
    guard !blocked, activeStep == nil else { throw sequenceFailure() }
    if let approval {
      guard approval == step.approval else { throw bindingFailure() }
    } else {
      guard step.stepIndex == 0 else { throw sequenceFailure() }
    }
    guard step.stepIndex == nextStepIndex else { throw sequenceFailure() }
  }

  mutating func accept(_ step: VitlanePurchaseStepContinuity) throws {
    try validate(step)
    if approval == nil { approval = step.approval }
    activeStep = step
  }

  mutating func resolve(_ step: VitlanePurchaseStepContinuity, applied: Bool) {
    guard activeStep == step, approval == step.approval else {
      blocked = true
      activeStep = nil
      return
    }
    activeStep = nil
    if applied {
      nextStepIndex = step.stepIndex + 1
    } else {
      // A merchant mutation may have occurred even when its result cannot be
      // proved. A fresh lease/approval is required instead of retrying it.
      blocked = true
    }
  }

  mutating func reset() {
    self = VitlanePurchaseSequenceState()
  }

  private func sequenceFailure() -> VitlaneBrowserFailure {
    VitlaneBrowserFailure(
      "APPROVAL_STEP_MISMATCH",
      "The merchant preparation step is missing, repeated, or out of order."
    )
  }

  private func bindingFailure() -> VitlaneBrowserFailure {
    VitlaneBrowserFailure(
      "APPROVAL_BINDING_MISMATCH",
      "The merchant preparation approval changed during this run."
    )
  }
}

final class VitlaneRecipeStore {
  static let coupangAdapterId = "builtin.coupang.purchase-preparation"
  static let coupangProductSteps: Set<String> = [
    "select_option", "verify_options", "set_quantity", "buy_now", "add_to_cart"
  ]
  static let coupangSteps = coupangProductSteps.union(["start_checkout"])
  static let coupangNavigationSteps: Set<String> = ["buy_now", "start_checkout"]
  private static let maximumKrw = 1_000_000_000_000

  static func validatePublicSearchQuery(_ value: String) throws {
    guard
      !value.isEmpty,
      value.count <= 160,
      value == value.trimmingCharacters(in: .whitespacesAndNewlines)
    else {
      throw VitlaneBrowserFailure("invalid_parameter", "The public search query length is invalid.")
    }
    guard !isSensitivePublicSearchQuery(value) else {
      throw VitlaneBrowserFailure("sensitive_input_forbidden", "Sensitive values must be entered by the user.")
    }
  }

  static func isSensitivePublicSearchQuery(_ value: String) -> Bool {
    let text = value.precomposedStringWithCompatibilityMapping
    let forbiddenPatterns = [
      #"\b(?:password|passwd|passcode|otp|one[- ]?time|cvv|cvc|card\s*number|access[_ -]?token|api[_ -]?key|secret)\b"#,
      #"(?:비밀번호|인증번호|일회용\s*코드|카드\s*번호|보안\s*코드|주민등록)"#,
      #"[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}"#,
      #"https?://"#
    ]
    if forbiddenPatterns.contains(where: { pattern in
      text.range(of: pattern, options: [.regularExpression, .caseInsensitive]) != nil
    }) {
      return true
    }
    return text.filter(\.isNumber).count >= 11
  }

  @discardableResult
  static func validateCoupangPreparation(
    stepId: String,
    bindings: [String: Any],
    now: Date = Date()
  ) throws -> VitlanePurchaseApprovalContinuity {
    guard coupangSteps.contains(stepId) else { throw denied() }
    guard
      let approval = bindings["approval"] as? [String: Any],
      let snapshot = bindings["approvedPreparation"] as? [String: Any]
    else { throw denied() }
    try validateApproval(approval, now: now)
    guard
      snapshot.hasExactlyKeys(["merchantId", "recipeVersion", "mode", "route", "approval", "items"]),
      snapshot.nonEmptyString("merchantId") == "COUPANG",
      snapshot.nonEmptyString("recipeVersion") == "1",
      let mode = snapshot.nonEmptyString("mode"), ["single", "multi"].contains(mode),
      snapshot.nonEmptyString("route") == (mode == "single" ? "single_buy_now" : "multi_cart_checkout"),
      let snapshotApproval = snapshot["approval"] as? [String: Any],
      NSDictionary(dictionary: snapshotApproval).isEqual(to: approval),
      let items = snapshot["items"] as? [[String: Any]],
      !items.isEmpty, items.count <= 20,
      (mode == "single" ? items.count == 1 : items.count >= 2)
    else { throw denied() }
    try items.forEach(validatePlanItem)
    let identities = items.compactMap { item -> String? in
      guard let identity = item["offerIdentity"] as? [String: Any] else { return nil }
      return ["productId", "itemId", "vendorItemId"]
        .compactMap { identity.nonEmptyString($0) }
        .joined(separator: ":")
    }
    guard identities.count == items.count, Set(identities).count == items.count else { throw denied() }
    var total = 0
    for item in items {
      guard let lineCeiling = item.int("linePriceCeilingKrw") else { throw denied() }
      let (nextTotal, overflow) = total.addingReportingOverflow(lineCeiling)
      guard !overflow, nextTotal <= maximumKrw else { throw denied() }
      total = nextTotal
    }
    guard total > 0, total <= (approval.int("totalPriceCeilingKrw") ?? 0) else { throw denied() }
    try validateApprovalDigest(snapshot: snapshot, approval: approval)
    guard
      let approvalId = approval.nonEmptyString("approvalId"),
      let approvalDigest = approval.nonEmptyString("approvalDigest")
    else { throw denied() }
    let continuity = VitlanePurchaseApprovalContinuity(
      approvalId: approvalId,
      approvalDigest: approvalDigest,
      mode: mode
    )

    if stepId == "start_checkout" {
      guard
        bindings.hasExactlyKeys(["approvedPreparation", "approval", "approvedLines", "expectedOrigin", "expectedPath"]),
        mode == "multi",
        let lines = bindings["approvedLines"] as? [[String: Any]], lines.count == items.count,
        bindings.nonEmptyString("expectedOrigin") == "https://cart.coupang.com",
        bindings.nonEmptyString("expectedPath") == "/cartView.pang"
      else { throw denied() }
      try lines.forEach(validateLine)
      for (index, item) in items.enumerated() {
        guard let identity = item["offerIdentity"] as? [String: Any] else { throw denied() }
        let expected: [String: Any] = [
          "productId": identity["productId"] as Any,
          "itemId": identity["itemId"] as Any,
          "vendorItemId": identity["vendorItemId"] as Any,
          "quantity": item["quantity"] as Any,
          "unitPriceCeilingKrw": item["unitPriceCeilingKrw"] as Any,
          "linePriceCeilingKrw": item["linePriceCeilingKrw"] as Any
        ]
        guard NSDictionary(dictionary: lines[index]).isEqual(to: expected) else { throw denied() }
      }
      return continuity
    }

    let productKeys = [
      "approvedPreparation", "approval", "targetLineIndex", "productId", "itemId", "vendorItemId",
      "quantity", "unitPriceCeilingKrw", "linePriceCeilingKrw", "expectedOrigin", "expectedPath"
    ]
    let expectedKeys = stepId == "select_option"
      ? productKeys + ["targetOptionIndex", "groupName", "valueName"]
      : productKeys
    guard
      bindings.hasExactlyKeys(expectedKeys),
      let targetLineIndex = bindings.int("targetLineIndex"), targetLineIndex >= 0, targetLineIndex < items.count,
      ["https://coupang.com", "https://www.coupang.com"].contains(bindings.nonEmptyString("expectedOrigin") ?? ""),
      let expectedPath = bindings.nonEmptyString("expectedPath"),
      expectedPath.range(of: #"^/vp/products/[1-9][0-9]*/?$"#, options: .regularExpression) != nil
    else { throw denied() }
    let item = items[targetLineIndex]
    guard let identity = item["offerIdentity"] as? [String: Any] else { throw denied() }
    let expected: [String: Any] = [
      "productId": identity["productId"] as Any,
      "itemId": identity["itemId"] as Any,
      "vendorItemId": identity["vendorItemId"] as Any,
      "quantity": item["quantity"] as Any,
      "unitPriceCeilingKrw": item["unitPriceCeilingKrw"] as Any,
      "linePriceCeilingKrw": item["linePriceCeilingKrw"] as Any
    ]
    let actual = Dictionary(uniqueKeysWithValues: expected.keys.map { ($0, bindings[$0] as Any) })
    try validateLine(actual)
    guard NSDictionary(dictionary: actual).isEqual(to: expected),
      URL(string: "https://www.coupang.com\(expectedPath)")?.lastPathComponent == bindings.nonEmptyString("productId"),
      (stepId != "buy_now" || mode == "single"),
      (stepId != "add_to_cart" || mode == "multi")
    else { throw denied() }

    if stepId == "select_option" {
      guard
        let targetOptionIndex = bindings.int("targetOptionIndex"), targetOptionIndex >= 0,
        let groupName = bindings.nonEmptyString("groupName"), groupName.count <= 120,
        let valueName = bindings.nonEmptyString("valueName"), valueName.count <= 120,
        let options = item["options"] as? [String: Any], options.nonEmptyString("kind") == "choices",
        let choices = options["choices"] as? [[String: Any]], targetOptionIndex < choices.count,
        choices[targetOptionIndex].nonEmptyString("groupName") == groupName,
        choices[targetOptionIndex].nonEmptyString("valueName") == valueName
      else { throw denied() }
    }
    return continuity
  }

  static func validateCoupangPreparationStep(
    stepId: String,
    bindings: [String: Any],
    now: Date = Date()
  ) throws -> VitlanePurchaseStepContinuity {
    let approval = try validateCoupangPreparation(stepId: stepId, bindings: bindings, now: now)
    guard
      let snapshot = bindings["approvedPreparation"] as? [String: Any],
      let items = snapshot["items"] as? [[String: Any]]
    else { throw denied() }

    var sequence: [(stepId: String, lineIndex: Int?, optionIndex: Int?)] = []
    for (lineIndex, item) in items.enumerated() {
      guard let options = item["options"] as? [String: Any], let kind = options.nonEmptyString("kind") else {
        throw denied()
      }
      if kind == "choices" {
        guard let choices = options["choices"] as? [[String: Any]] else { throw denied() }
        for optionIndex in choices.indices {
          sequence.append(("select_option", lineIndex, optionIndex))
        }
      }
      sequence.append(("verify_options", lineIndex, nil))
      sequence.append(("set_quantity", lineIndex, nil))
      sequence.append((approval.mode == "single" ? "buy_now" : "add_to_cart", lineIndex, nil))
    }
    if approval.mode == "multi" {
      sequence.append(("start_checkout", nil, nil))
    }

    let lineIndex = bindings.int("targetLineIndex")
    let optionIndex = bindings.int("targetOptionIndex")
    let matchingIndices = sequence.indices.filter { index in
      let entry = sequence[index]
      return entry.stepId == stepId && entry.lineIndex == lineIndex && entry.optionIndex == optionIndex
    }
    guard matchingIndices.count == 1, let stepIndex = matchingIndices.first else { throw denied() }
    return VitlanePurchaseStepContinuity(
      approval: approval,
      stepId: stepId,
      stepIndex: stepIndex,
      stepCount: sequence.count
    )
  }

  private static func validateApproval(_ value: [String: Any], now: Date) throws {
    guard
      value.hasExactlyKeys(["approvalId", "approvalDigest", "currency", "revision", "expiresAt", "totalPriceCeilingKrw"]),
      let approvalId = value.nonEmptyString("approvalId"), validIdentifier(approvalId),
      let digest = value.nonEmptyString("approvalDigest"),
      digest.range(of: #"^sha256:[a-f0-9]{64}$"#, options: .regularExpression) != nil,
      value.nonEmptyString("currency") == "KRW",
      let revision = value.int("revision"), revision >= 1,
      let expiresAtText = value.nonEmptyString("expiresAt"),
      let expiresAt = iso8601.date(from: expiresAtText), expiresAt > now,
      expiresAt.timeIntervalSince(now) <= 10 * 60,
      let ceiling = value.int("totalPriceCeilingKrw"), ceiling >= 1, ceiling <= maximumKrw
    else { throw denied() }
  }

  private static func validateApprovalDigest(
    snapshot: [String: Any],
    approval: [String: Any]
  ) throws {
    guard
      let suppliedDigest = approval["approvalDigest"] as? String,
      let suppliedBytes = decodeSHA256Digest(suppliedDigest)
    else { throw denied() }

    var unsignedApproval = approval
    unsignedApproval.removeValue(forKey: "approvalDigest")
    var digestMaterial = snapshot
    digestMaterial["approval"] = unsignedApproval
    guard JSONSerialization.isValidJSONObject(digestMaterial) else { throw denied() }

    let canonicalJSON: Data
    do {
      canonicalJSON = try JSONSerialization.data(
        withJSONObject: digestMaterial,
        options: [.sortedKeys, .withoutEscapingSlashes]
      )
    } catch {
      throw denied()
    }
    let expectedBytes = Array(SHA256.hash(data: canonicalJSON))
    guard constantTimeEquals(expectedBytes, suppliedBytes) else { throw denied() }
  }

  private static func decodeSHA256Digest(_ value: String) -> [UInt8]? {
    guard value.hasPrefix("sha256:") else { return nil }
    let hexadecimal = Array(value.dropFirst("sha256:".count).utf8)
    guard hexadecimal.count == 64 else { return nil }
    var bytes: [UInt8] = []
    bytes.reserveCapacity(32)
    for index in stride(from: 0, to: hexadecimal.count, by: 2) {
      guard
        let high = hexadecimalNibble(hexadecimal[index]),
        let low = hexadecimalNibble(hexadecimal[index + 1])
      else { return nil }
      bytes.append((high << 4) | low)
    }
    return bytes
  }

  private static func hexadecimalNibble(_ value: UInt8) -> UInt8? {
    switch value {
    case 48...57:
      return value - 48
    case 97...102:
      return value - 87
    default:
      return nil
    }
  }

  private static func constantTimeEquals(_ lhs: [UInt8], _ rhs: [UInt8]) -> Bool {
    guard lhs.count == rhs.count else { return false }
    var difference: UInt8 = 0
    for index in lhs.indices {
      difference |= lhs[index] ^ rhs[index]
    }
    return difference == 0
  }

  private static func validatePlanItem(_ value: [String: Any]) throws {
    guard
      value.hasExactlyKeys(["query", "offerIdentity", "quantity", "unitPriceCeilingKrw", "linePriceCeilingKrw", "productUrl", "options"]),
      let query = value.nonEmptyString("query"), query.count <= 160, !isSensitivePublicSearchQuery(query),
      let identity = value["offerIdentity"] as? [String: Any],
      let productURLText = value.nonEmptyString("productUrl"), let productURL = URL(string: productURLText),
      ["https://coupang.com", "https://www.coupang.com"].contains(VitlaneURLPolicy.origin(productURL)),
      productURL.user == nil, productURL.password == nil, productURL.fragment == nil,
      let options = value["options"] as? [String: Any]
    else { throw denied() }
    var line = identity
    line["quantity"] = value["quantity"]
    line["unitPriceCeilingKrw"] = value["unitPriceCeilingKrw"]
    line["linePriceCeilingKrw"] = value["linePriceCeilingKrw"]
    try validateLine(line)
    guard
      productURL.path.range(of: #"^/vp/products/[1-9][0-9]*/?$"#, options: .regularExpression) != nil,
      productURL.lastPathComponent == identity.nonEmptyString("productId")
    else { throw denied() }
    let components = URLComponents(url: productURL, resolvingAgainstBaseURL: false)
    let queryItems = components?.queryItems ?? []
    let itemIds = queryItems.filter { $0.name == "itemId" }.compactMap(\.value)
    let vendorItemIds = queryItems.filter { $0.name == "vendorItemId" }.compactMap(\.value)
    guard itemIds == [identity.nonEmptyString("itemId")].compactMap({ $0 }),
      vendorItemIds == [identity.nonEmptyString("vendorItemId")].compactMap({ $0 })
    else { throw denied() }
    if options.nonEmptyString("kind") == "none" {
      guard options.hasExactlyKeys(["kind"]) else { throw denied() }
    } else {
      guard options.hasExactlyKeys(["kind", "choices"]), options.nonEmptyString("kind") == "choices",
        let choices = options["choices"] as? [[String: Any]], !choices.isEmpty, choices.count <= 10,
        choices.allSatisfy({ choice in
          choice.hasExactlyKeys(["groupName", "valueName"]) &&
            (choice.nonEmptyString("groupName")?.count ?? 121) <= 120 &&
            (choice.nonEmptyString("valueName")?.count ?? 121) <= 120
        })
      else { throw denied() }
      let groupNames = choices.compactMap { $0.nonEmptyString("groupName") }
      guard Set(groupNames).count == choices.count else { throw denied() }
    }
  }

  private static func validateLine(_ value: [String: Any]) throws {
    guard
      value.hasExactlyKeys(["productId", "itemId", "vendorItemId", "quantity", "unitPriceCeilingKrw", "linePriceCeilingKrw"]),
      ["productId", "itemId", "vendorItemId"].allSatisfy({ key in
        guard let part = value.nonEmptyString(key) else { return false }
        return part.range(of: #"^[1-9][0-9]{0,19}$"#, options: .regularExpression) != nil
      }),
      let quantity = value.int("quantity"), quantity >= 1, quantity <= 99,
      let unit = value.int("unitPriceCeilingKrw"), unit >= 1, unit <= maximumKrw,
      let line = value.int("linePriceCeilingKrw"), line >= 1, line <= maximumKrw,
      unit <= line / quantity
    else { throw denied() }
  }

  private static let iso8601: ISO8601DateFormatter = {
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return formatter
  }()

  private static func validIdentifier(_ value: String) -> Bool {
    value.count <= 128 && value.range(of: #"^[A-Za-z0-9][A-Za-z0-9._:-]*$"#, options: .regularExpression) != nil
  }

  private static func denied() -> VitlaneBrowserFailure {
    VitlaneBrowserFailure("POLICY_DENIED", "The merchant preparation approval is invalid.")
  }
}

struct VitlaneCoupangNavigationAllowance {
  let commandId: String
  let generation: Int
  let serial: Int
  let stepId: String
  let sourceURL: String
  private(set) var available = true

  var expectedResultCode: String {
    stepId == "buy_now" ? "COUPANG_BUY_NOW_TRIGGERED" : "COUPANG_CHECKOUT_TRIGGERED"
  }

  init?(
    commandId: String,
    generation: Int,
    serial: Int,
    stepId: String,
    sourceURL: String
  ) {
    guard
      VitlaneRecipeStore.coupangNavigationSteps.contains(stepId),
      let source = URL(string: sourceURL),
      VitlaneURLPolicy.isPublicAgentURL(source)
    else { return nil }
    let validSource: Bool
    if stepId == "start_checkout" {
      validSource = VitlaneURLPolicy.isExactCoupangCartReviewURL(source)
    } else {
      validSource = ["https://coupang.com", "https://www.coupang.com"]
        .contains(VitlaneURLPolicy.origin(source)) &&
        source.path.range(of: #"^/vp/products/[1-9][0-9]*/?$"#, options: .regularExpression) != nil
    }
    guard validSource else { return nil }
    self.commandId = commandId
    self.generation = generation
    self.serial = serial
    self.stepId = stepId
    self.sourceURL = sourceURL
  }

  mutating func consume(
    commandId: String,
    generation: Int,
    serial: Int,
    currentURL: String,
    targetURL: URL
  ) -> Bool {
    guard available else { return false }
    available = false
    return self.commandId == commandId &&
      self.generation == generation &&
      self.serial == serial &&
      sourceURL == currentURL &&
      VitlaneURLPolicy.isCoupangNavigationTarget(targetURL)
  }
}

enum VitlaneBrowserResources {
  static let bundle: Bundle = {
    let codeBundle = Bundle(for: VitlaneBrowserResourceToken.self)
    let candidates = [codeBundle, Bundle.main] + Bundle.allFrameworks
    for candidate in candidates {
      if
        let resourceURL = candidate.url(forResource: "VitlaneBrowserResources", withExtension: "bundle"),
        let resourceBundle = Bundle(url: resourceURL)
      {
        return resourceBundle
      }
    }
    return codeBundle
  }()

  static func localized(_ key: String) -> String {
    NSLocalizedString(key, bundle: bundle, comment: "")
  }
}

private final class VitlaneBrowserResourceToken: NSObject {}
