import Foundation

@main
struct VitlaneCommandPermitSmoke {
  static func main() throws {
    let action: [String: Any] = [
      "kind": "scroll",
      "page": [
        "tabId": "tab_001",
        "frameId": "frame_main",
        "documentEpoch": 7,
        "observationId": "obs_001",
        "topOrigin": "https://shop.example.com",
        "frameOrigin": "https://shop.example.com",
        "pathname": "/products",
        "queryOrFragmentPresent": false
      ],
      "direction": "down",
      "reason": "더 많은 공개 후보를 확인합니다."
    ]
    let command = try VitlaneCommandEnvelope(dictionary: [
      "protocolVersion": 1,
      "commandId": "cmd_golden_001",
      "runId": "run_001",
      "deviceId": "device_001",
      "profileRef": "profile_001",
      "sequence": 1,
      "leaseEpoch": 1,
      "controlGeneration": 0,
      "expiresAt": "2030-01-01T00:00:15.000Z",
      "actionHash": "7fb192368643e2d984926c5c445b8392ea24122203c22125830d0e55169561a8",
      "action": action,
      "serverPermit": "hmac-sha256:sKW91OzpyXrv4HaeGcxp67kgzeO4f7ETe11fEukJ5Ho"
    ])
    let key = Data("test-only-permit-key-32-bytes-minimum-value".utf8)
    try VitlaneCommandPermit.verify(command, keyData: key)

    let account = try VitlaneCommandPermit.keychainAccount(
      deviceId: "device_001",
      profileRef: "profile_001"
    )
    precondition(account == "v1:0aaa4b2a3166c38b20be3562ff86b606b8fc9a9194d11b923f7a9b382ceac8fb")
    let formerlyCollidingA = try VitlaneCommandPermit.keychainAccount(deviceId: "a:b", profileRef: "c")
    let formerlyCollidingB = try VitlaneCommandPermit.keychainAccount(deviceId: "a", profileRef: "b:c")
    precondition(formerlyCollidingA != formerlyCollidingB)

    var tampered = action
    tampered["direction"] = "up"
    let tamperedCommand = try VitlaneCommandEnvelope(dictionary: [
      "protocolVersion": 1,
      "commandId": command.commandId,
      "runId": command.runId,
      "deviceId": command.deviceId,
      "profileRef": command.profileRef,
      "sequence": command.sequence,
      "leaseEpoch": command.leaseEpoch,
      "controlGeneration": command.controlGeneration,
      "expiresAt": "2030-01-01T00:00:15.000Z",
      "actionHash": command.actionHash,
      "action": tampered,
      "serverPermit": command.serverPermit
    ])
    do {
      try VitlaneCommandPermit.verify(tamperedCommand, keyData: key)
      fatalError("Tampered action unexpectedly passed permit verification")
    } catch let failure as VitlaneBrowserFailure {
      precondition(failure.code == "action_hash_mismatch")
    }
  }
}
