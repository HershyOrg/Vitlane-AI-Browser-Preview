import CryptoKit
import Foundation
import Security

enum VitlaneCommandPermit {
  private static let keychainService = "com.vitlane.browser.command-permit"

  static func verify(_ command: VitlaneCommandEnvelope) throws {
    let keyData = try permitKey(deviceId: command.deviceId, profileRef: command.profileRef)
    try verify(command, keyData: keyData)
  }

  static func verify(_ command: VitlaneCommandEnvelope, keyData: Data) throws {
    guard keyData.count >= 32 else {
      throw VitlaneBrowserFailure("permit_key_unavailable", "The device command permit key is too short.")
    }
    guard command.actionHash.range(of: #"^[a-f0-9]{64}$"#, options: .regularExpression) != nil else {
      throw VitlaneBrowserFailure("action_hash_mismatch", "The authorized action hash is invalid.")
    }
    let actionData = Data(try canonicalize(command.action).utf8)
    let actualActionHash = SHA256.hash(data: actionData).map { String(format: "%02x", $0) }.joined()
    guard actualActionHash == command.actionHash else {
      throw VitlaneBrowserFailure("action_hash_mismatch", "The authorized action was modified.")
    }
    guard command.serverPermit.range(
      of: #"^hmac-sha256:[A-Za-z0-9_-]{43}$"#,
      options: .regularExpression
    ) != nil else {
      throw VitlaneBrowserFailure("invalid_server_permit", "The command has no valid server permit.")
    }

    let message = Data(try canonicalize(command.unsignedDictionary).utf8)
    guard let supplied = base64URLData(String(command.serverPermit.dropFirst("hmac-sha256:".count))) else {
      throw VitlaneBrowserFailure("invalid_server_permit", "The server permit encoding is invalid.")
    }
    guard HMAC<SHA256>.isValidAuthenticationCode(supplied, authenticating: message, using: SymmetricKey(data: keyData)) else {
      throw VitlaneBrowserFailure("invalid_server_permit", "The command server permit did not verify.")
    }
  }

  static func canonicalize(_ value: Any) throws -> String {
    if value is NSNull { return "null" }
    if let string = value as? String { return jsonString(string) }
    if let bool = value as? Bool { return bool ? "true" : "false" }
    if let number = value as? NSNumber {
      if CFGetTypeID(number) == CFBooleanGetTypeID() { return number.boolValue ? "true" : "false" }
      let double = number.doubleValue
      guard double.isFinite else { throw VitlaneBrowserFailure("canonicalization_failed", "A command number is not finite.") }
      if floor(double) == double { return String(format: "%.0f", locale: Locale(identifier: "en_US_POSIX"), double) }
      return String(double)
    }
    if let int = value as? Int { return String(int) }
    if let array = value as? [Any] {
      return "[\(try array.map(canonicalize).joined(separator: ","))]"
    }
    if let dictionary = value as? [String: Any] {
      let fields = try dictionary.keys.sorted().map { key in
        "\(jsonString(key)):\(try canonicalize(dictionary[key] as Any))"
      }
      return "{\(fields.joined(separator: ","))}"
    }
    throw VitlaneBrowserFailure("canonicalization_failed", "The command contains an unsupported value.")
  }

  private static func permitKey(deviceId: String, profileRef: String) throws -> Data {
    let account = try keychainAccount(deviceId: deviceId, profileRef: profileRef)
    let query: [String: Any] = [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrService as String: keychainService,
      kSecAttrAccount as String: account,
      kSecReturnData as String: true,
      kSecMatchLimit as String: kSecMatchLimitOne
    ]
    var result: CFTypeRef?
    let status = SecItemCopyMatching(query as CFDictionary, &result)
    guard status == errSecSuccess, let key = result as? Data, key.count >= 32 else {
      throw VitlaneBrowserFailure(
        "permit_key_unavailable",
        "The device command permit key is unavailable. Re-authenticate this device profile."
      )
    }
    return key
  }

  static func keychainAccount(deviceId: String, profileRef: String) throws -> String {
    let canonicalBinding = try canonicalize([
      "deviceId": deviceId,
      "profileRef": profileRef,
      "purpose": "vitlane-browser-command-permit-v1"
    ])
    let digest = SHA256.hash(data: Data(canonicalBinding.utf8))
      .map { String(format: "%02x", $0) }
      .joined()
    return "v1:\(digest)"
  }

  private static func base64URLData(_ value: String) -> Data? {
    var base64 = value.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
    base64 += String(repeating: "=", count: (4 - base64.count % 4) % 4)
    return Data(base64Encoded: base64)
  }

  private static func jsonString(_ value: String) -> String {
    var result = "\""
    for scalar in value.unicodeScalars {
      switch scalar.value {
      case 0x22: result += "\\\""
      case 0x5c: result += "\\\\"
      case 0x08: result += "\\b"
      case 0x0c: result += "\\f"
      case 0x0a: result += "\\n"
      case 0x0d: result += "\\r"
      case 0x09: result += "\\t"
      case 0x00...0x1f: result += String(format: "\\u%04x", scalar.value)
      default: result.unicodeScalars.append(scalar)
      }
    }
    result += "\""
    return result
  }
}
