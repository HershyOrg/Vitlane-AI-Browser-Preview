import Foundation

enum VitlaneControlMode: String {
  case agent
  case paused
  case user
  case stopped
}

struct VitlanePageBinding {
  let tabId: String
  let documentEpoch: Int
  let observationId: String
  let sanitizedURL: String
  let origin: String
  let pathname: String
  let queryOrFragmentPresent: Bool
  let title: String
  let foreground: Bool

  var dictionary: [String: Any] {
    [
      "tabId": tabId,
      "frameId": "frame_main",
      "documentEpoch": documentEpoch,
      "observationId": observationId,
      "topOrigin": origin,
      "frameOrigin": origin,
      "pathname": pathname,
      "queryOrFragmentPresent": queryOrFragmentPresent
    ]
  }
}

struct VitlaneCommandEnvelope {
  let commandId: String
  let runId: String
  let deviceId: String
  let profileRef: String
  let sequence: Int
  let leaseEpoch: Int
  let controlGeneration: Int
  let expiresAt: Date
  let actionHash: String
  let action: [String: Any]
  let serverPermit: String

  init(dictionary: [String: Any]) throws {
    guard dictionary.hasExactlyKeys([
      "protocolVersion", "commandId", "runId", "deviceId", "profileRef", "sequence",
      "leaseEpoch", "controlGeneration", "expiresAt", "actionHash", "action", "serverPermit"
    ]) else {
      throw VitlaneBrowserFailure("invalid_envelope", "The command envelope contains unexpected fields.")
    }
    guard dictionary.int("protocolVersion") == 1 else {
      throw VitlaneBrowserFailure("protocol_mismatch", "Unsupported browser protocol.")
    }
    guard
      let commandId = dictionary.nonEmptyString("commandId"),
      let runId = dictionary.nonEmptyString("runId"),
      let deviceId = dictionary.nonEmptyString("deviceId"),
      let profileRef = dictionary.nonEmptyString("profileRef"),
      let sequence = dictionary.int("sequence"), sequence > 0,
      let leaseEpoch = dictionary.int("leaseEpoch"), leaseEpoch > 0,
      let controlGeneration = dictionary.int("controlGeneration"), controlGeneration >= 0,
      let expiresAtString = dictionary.nonEmptyString("expiresAt"),
      let expiresAt = VitlaneDates.parse(expiresAtString),
      let actionHash = dictionary.nonEmptyString("actionHash"),
      let action = dictionary.dictionary("action"),
      let serverPermit = dictionary.nonEmptyString("serverPermit")
    else {
      throw VitlaneBrowserFailure("invalid_envelope", "The command envelope is incomplete.")
    }
    self.commandId = commandId
    self.runId = runId
    self.deviceId = deviceId
    self.profileRef = profileRef
    self.sequence = sequence
    self.leaseEpoch = leaseEpoch
    self.controlGeneration = controlGeneration
    self.expiresAt = expiresAt
    self.actionHash = actionHash
    self.action = action
    self.serverPermit = serverPermit
  }

  var unsignedDictionary: [String: Any] {
    [
      "protocolVersion": 1,
      "commandId": commandId,
      "runId": runId,
      "deviceId": deviceId,
      "profileRef": profileRef,
      "sequence": sequence,
      "leaseEpoch": leaseEpoch,
      "controlGeneration": controlGeneration,
      "expiresAt": VitlaneDates.string(expiresAt),
      "actionHash": actionHash,
      "action": action
    ]
  }
}

struct VitlaneBrowserFailure: Error {
  let code: String
  let message: String

  init(_ code: String, _ message: String) {
    self.code = code
    self.message = message
  }
}

enum VitlaneDates {
  private static let fractionalFormatter: ISO8601DateFormatter = {
    let formatter = ISO8601DateFormatter()
    formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
    return formatter
  }()

  static func parse(_ value: String) -> Date? {
    guard value.range(
      of: #"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$"#,
      options: .regularExpression
    ) != nil else { return nil }
    return fractionalFormatter.date(from: value)
  }

  static func string(_ date: Date = Date()) -> String {
    fractionalFormatter.string(from: date)
  }
}

extension Dictionary where Key == String, Value == Any {
  func nonEmptyString(_ key: String) -> String? {
    guard let value = self[key] as? String else { return nil }
    let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
    return trimmed.isEmpty ? nil : trimmed
  }

  func int(_ key: String) -> Int? {
    if self[key] is Bool { return nil }
    if let value = self[key] as? Int { return value }
    if let value = self[key] as? NSNumber {
      let decimal = value.doubleValue
      guard decimal.isFinite, decimal.rounded() == decimal, abs(decimal) <= 9_007_199_254_740_991 else {
        return nil
      }
      return value.intValue
    }
    return nil
  }

  func dictionary(_ key: String) -> [String: Any]? {
    self[key] as? [String: Any]
  }

  func stringDictionary(_ key: String) -> [String: String] {
    if let values = self[key] as? [String: String] { return values }
    guard let values = self[key] as? [String: Any] else { return [:] }
    return values.reduce(into: [String: String]()) { result, pair in
      if let value = pair.value as? String {
        result[pair.key] = value
      }
    }
  }

  func boundedString(_ key: String, minimum: Int = 0, maximum: Int) -> String? {
    guard let value = self[key] as? String, value.count >= minimum, value.count <= maximum else {
      return nil
    }
    let forbiddenControlCharacters = value.unicodeScalars.contains { scalar in
      let code = scalar.value
      return code <= 0x08 || code == 0x0b || code == 0x0c || (code >= 0x0e && code <= 0x1f)
    }
    return forbiddenControlCharacters ? nil : value
  }

  func hasExactlyKeys(_ keys: Set<String>) -> Bool {
    Set(self.keys) == keys
  }

  func hasExactlyKeys(_ keys: [String]) -> Bool {
    hasExactlyKeys(Set(keys))
  }
}

enum VitlaneURLPolicy {
  private static let coupangNavigationHosts: Set<String> = [
    "coupang.com", "www.coupang.com", "cart.coupang.com",
    "checkout.coupang.com", "login.coupang.com"
  ]

  static func isWebURL(_ url: URL) -> Bool {
    guard let scheme = url.scheme?.lowercased() else { return false }
    return (scheme == "https" || scheme == "http") && url.host != nil
  }

  static func encodedHost(_ url: URL?) -> String {
    guard let url else { return "" }
    if #available(iOS 16.0, *) {
      return url.host(percentEncoded: true) ?? ""
    }
    return url.host ?? ""
  }

  static func origin(_ url: URL?) -> String {
    guard let url, let scheme = url.scheme?.lowercased() else { return "" }
    let host = encodedHost(url).lowercased()
    guard !host.isEmpty else { return "" }
    let defaultPort = (scheme == "https" && url.port == 443) || (scheme == "http" && url.port == 80)
    let port = defaultPort ? "" : url.port.map { ":\($0)" } ?? ""
    return "\(scheme)://\(host)\(port)"
  }

  static func sanitizedURL(_ url: URL?) -> String {
    guard let url else { return "" }
    let sourceComponents = URLComponents(url: url, resolvingAgainstBaseURL: false)
    var components = URLComponents()
    components.scheme = url.scheme?.lowercased()
    components.percentEncodedHost = encodedHost(url).lowercased()
    components.port = url.port
    let sourcePath = sourceComponents?.percentEncodedPath ?? url.path
    let path = sourcePath.isEmpty ? "/" : String(sourcePath.prefix(512))
    components.percentEncodedPath = path
    return components.string ?? origin(url)
  }

  static func pathname(_ url: URL?) -> String {
    guard let url else { return "/" }
    let encoded = URLComponents(url: url, resolvingAgainstBaseURL: false)?.percentEncodedPath ?? url.path
    return encoded.isEmpty ? "/" : String(encoded.prefix(1024))
  }

  static func securityState(_ url: URL?) -> String {
    guard let url, let scheme = url.scheme?.lowercased() else { return "unknown" }
    if scheme == "https" { return "secure" }
    if scheme == "http" { return "insecure" }
    return "unknown"
  }

  static func isPublicAgentURL(_ url: URL?) -> Bool {
    guard
      let url,
      url.scheme?.lowercased() == "https",
      url.user == nil,
      url.password == nil,
      url.port == nil || url.port == 443
    else { return false }
    let host = encodedHost(url).lowercased().trimmingCharacters(in: CharacterSet(charactersIn: "."))
    if host.isEmpty || !host.contains(".") || host.contains(":") { return false }
    let deniedSuffixes = [
      "localhost", ".localhost", ".local", ".internal", ".lan", ".home.arpa",
      ".test", ".invalid", ".example"
    ]
    if deniedSuffixes.contains(where: { host == String($0.dropFirst($0.first == "." ? 1 : 0)) || host.hasSuffix($0) }) {
      return false
    }
    let octets = host.split(separator: ".").compactMap { Int($0) }
    if octets.count == 4, octets.allSatisfy({ (0...255).contains($0) }) {
      let first = octets[0]
      let second = octets[1]
      if first == 0 || first == 10 || first == 127 || first >= 224 { return false }
      if first == 100 && (64...127).contains(second) { return false }
      if first == 169 && second == 254 { return false }
      if first == 172 && (16...31).contains(second) { return false }
      if first == 192 && (second == 0 || second == 168) { return false }
      if first == 198 && (second == 18 || second == 19 || second == 51) { return false }
      if first == 203 && second == 0 { return false }
    }
    return true
  }

  static func isExactCoupangCartReviewURL(_ url: URL?) -> Bool {
    guard
      let url,
      let components = URLComponents(url: url, resolvingAgainstBaseURL: false),
      isPublicAgentURL(url),
      origin(url) == "https://cart.coupang.com",
      components.percentEncodedPath == "/cartView.pang",
      components.percentEncodedQuery == nil,
      components.percentEncodedFragment == nil
    else { return false }
    return true
  }

  static func isCoupangNavigationTarget(_ url: URL?) -> Bool {
    guard
      let url,
      isPublicAgentURL(url),
      url.scheme?.lowercased() == "https"
    else { return false }
    return coupangNavigationHosts.contains(encodedHost(url).lowercased())
  }

  static func nativeSensitiveReason(_ url: URL?) -> String? {
    guard let url else { return "unsupported_page" }
    let host = encodedHost(url).lowercased()
    let path = url.path.lowercased()
    let combined = "\(host)\(path)"

    // The native host admits only this exact, query-free cart review route.
    // The bundled page agent still has to prove the page is a cart review and
    // never projects its cart contents into an observation. Every other cart
    // or checkout URL remains a user-controlled surface.
    if isExactCoupangCartReviewURL(url) {
      return nil
    }
    if host == "checkout.coupang.com" {
      return "payment"
    }
    if host == "cart.coupang.com" {
      return "sensitive_page"
    }

    let paymentHosts = [
      "paypal.", "stripe.", "adyen.", "checkout.com", "toss.im", "tosspayments.",
      "inicis.", "kcp.", "nicepay.", "kakao.com", "pay.naver.com", "apple.com"
    ]
    if paymentHosts.contains(where: { combined.contains($0) }) {
      return "payment"
    }

    let sensitiveSegments = [
      "login", "signin", "sign-in", "auth", "password", "passwd", "otp", "captcha",
      "challenge", "verify", "verification", "checkout", "payment", "billing", "card",
      "account", "profile", "order", "purchase", "address", "wallet", "subscription",
      "session", "cancel/confirm", "booking/confirm"
    ]
    if sensitiveSegments.contains(where: { path.contains($0) }) {
      if path.contains("otp") || path.contains("verify") || path.contains("captcha") || path.contains("challenge") {
        return "verification"
      }
      if path.contains("payment") || path.contains("billing") || path.contains("checkout") || path.contains("card") {
        return "payment"
      }
      if path.contains("login") || path.contains("signin") || path.contains("auth") || path.contains("password") {
        return "login"
      }
      return "sensitive_page"
    }
    let privateCommerceSegments = Set(["cart", "basket", "bag"])
    if path.split(separator: "/").contains(where: { privateCommerceSegments.contains(String($0)) }) {
      return "sensitive_page"
    }
    return nil
  }

  static func sanitizedText(_ value: String, limit: Int = 180) -> String {
    var result = String(value.prefix(limit))
    let patterns = [
      #"[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}"#,
      #"(?:\+?\d[\d .-]{7,}\d)"#,
      #"(?:\d[ -]*?){13,19}"#,
      #"(?:otp|verification\s*code|one[- ]?time\s*code|인증번호|일회용\s*코드)\s*[:#=-]?\s*\d{4,8}"#,
      #"(?:api[_ -]?key|access[_ -]?token|secret|password|비밀번호)\s*[:=]\s*\S+"#
    ]
    for pattern in patterns {
      result = result.replacingOccurrences(
        of: pattern,
        with: "[redacted]",
        options: [.regularExpression, .caseInsensitive]
      )
    }
    return result.trimmingCharacters(in: .whitespacesAndNewlines)
  }
}
