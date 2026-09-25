import Foundation

@main
struct VitlanePolicySmoke {
  static func main() throws {
    let sensitiveQueries = [
      "my password is swordfish",
      "otp 123456",
      "card number 4111 1111 1111 1111",
      "person@example.com",
      "https://private.example/path",
      "+82 10 1234 5678",
      "주민등록 900101-1234567"
    ]
    for query in sensitiveQueries {
      precondition(VitlaneRecipeStore.isSensitivePublicSearchQuery(query))
      do {
        try VitlaneRecipeStore.validatePublicSearchQuery(query)
        fatalError("Sensitive query unexpectedly passed: \(query)")
      } catch let failure as VitlaneBrowserFailure {
        precondition(failure.code == "sensitive_input_forbidden")
      }
    }

    try VitlaneRecipeStore.validatePublicSearchQuery("waterproof hiking shoes")
    precondition(!VitlaneRecipeStore.isSensitivePublicSearchQuery("waterproof hiking shoes"))

    precondition(VitlaneURLPolicy.isPublicAgentURL(URL(string: "https://shop.example.com/products")))
    precondition(!VitlaneURLPolicy.isPublicAgentURL(URL(string: "http://shop.example.com/products")))
    precondition(!VitlaneURLPolicy.isPublicAgentURL(URL(string: "https://127.0.0.1/products")))
    precondition(!VitlaneURLPolicy.isPublicAgentURL(URL(string: "https://router.local/products")))
    precondition(
      VitlaneURLPolicy.origin(URL(string: "https://shop.example.com:8443/products"))
        == "https://shop.example.com:8443"
    )
    precondition(
      VitlaneURLPolicy.origin(URL(string: "https://shop.example.com:443/products"))
        == "https://shop.example.com"
    )

    let cartReviewURL = URL(string: "https://cart.coupang.com/cartView.pang")!
    precondition(VitlaneURLPolicy.isExactCoupangCartReviewURL(cartReviewURL))
    precondition(VitlaneURLPolicy.nativeSensitiveReason(cartReviewURL) == nil)
    for url in [
      "https://cart.coupang.com/cartView.pang/",
      "https://cart.coupang.com/cartView.pang?item=123",
      "https://cart.coupang.com/other"
    ] {
      precondition(!VitlaneURLPolicy.isExactCoupangCartReviewURL(URL(string: url)))
      precondition(VitlaneURLPolicy.nativeSensitiveReason(URL(string: url)) == "sensitive_page")
    }
    precondition(
      VitlaneURLPolicy.nativeSensitiveReason(URL(string: "https://checkout.coupang.com/")) == "payment"
    )
    precondition(
      VitlaneURLPolicy.nativeSensitiveReason(URL(string: "https://checkout.coupang.com/order/123")) == "payment"
    )

    var buyNowAllowance = VitlaneCoupangNavigationAllowance(
      commandId: "command_1",
      generation: 7,
      serial: 11,
      stepId: "buy_now",
      sourceURL: "https://www.coupang.com/vp/products/12345?itemId=23456&vendorItemId=34567"
    )!
    let checkoutURL = URL(string: "https://checkout.coupang.com/order/123")!
    precondition(VitlaneURLPolicy.nativeSensitiveReason(checkoutURL) == "payment")
    precondition(buyNowAllowance.consume(
      commandId: "command_1",
      generation: 7,
      serial: 11,
      currentURL: buyNowAllowance.sourceURL,
      targetURL: checkoutURL
    ))
    precondition(!buyNowAllowance.consume(
      commandId: "command_1",
      generation: 7,
      serial: 11,
      currentURL: buyNowAllowance.sourceURL,
      targetURL: checkoutURL
    ))

    var rejectedTargetAllowance = VitlaneCoupangNavigationAllowance(
      commandId: "command_2",
      generation: 7,
      serial: 12,
      stepId: "start_checkout",
      sourceURL: cartReviewURL.absoluteString
    )!
    precondition(!rejectedTargetAllowance.consume(
      commandId: "command_2",
      generation: 7,
      serial: 12,
      currentURL: cartReviewURL.absoluteString,
      targetURL: URL(string: "https://evil.coupang.com/checkout")!
    ))
    precondition(!rejectedTargetAllowance.consume(
      commandId: "command_2",
      generation: 7,
      serial: 12,
      currentURL: cartReviewURL.absoluteString,
      targetURL: checkoutURL
    ))
    precondition(VitlaneCoupangNavigationAllowance(
      commandId: "command_3",
      generation: 7,
      serial: 13,
      stepId: "add_to_cart",
      sourceURL: "https://www.coupang.com/vp/products/12345"
    ) == nil)

    let validationNow = VitlaneDates.parse("2030-01-01T00:00:00.000Z")!
    let singleApproval: [String: Any] = [
      "approvalId": "approval_1",
      "approvalDigest": "sha256:5d6b1b134c4026302b44a38bbdba2cbbfa2a6a590efe5fa2783b83cb7e00318e",
      "currency": "KRW",
      "revision": 1,
      "expiresAt": "2030-01-01T00:05:00.000Z",
      "totalPriceCeilingKrw": 50_000
    ]
    let firstItem = planItem(
      productId: "12345",
      itemId: "23456",
      vendorItemId: "34567",
      quantity: 1,
      ceiling: 20_000
    )
    let secondItem = planItem(
      productId: "45678",
      itemId: "56789",
      vendorItemId: "67890",
      quantity: 2,
      ceiling: 15_000
    )
    let singleSnapshot: [String: Any] = [
      "merchantId": "COUPANG",
      "recipeVersion": "1",
      "mode": "single",
      "route": "single_buy_now",
      "approval": singleApproval,
      "items": [firstItem]
    ]
    let buyNowBindings = productBindings(
      snapshot: singleSnapshot,
      approval: singleApproval,
      item: firstItem
    )
    do {
      let continuity = try VitlaneRecipeStore.validateCoupangPreparation(
        stepId: "buy_now",
        bindings: buyNowBindings,
        now: validationNow
      )
      precondition(continuity == VitlanePurchaseApprovalContinuity(
        approvalId: "approval_1",
        approvalDigest: singleApproval["approvalDigest"] as! String,
        mode: "single"
      ))
    } catch {
      fatalError("Valid buy-now binding failed: \(error)")
    }

    do {
      let verifyStep = try VitlaneRecipeStore.validateCoupangPreparationStep(
        stepId: "verify_options",
        bindings: buyNowBindings,
        now: validationNow
      )
      let quantityStep = try VitlaneRecipeStore.validateCoupangPreparationStep(
        stepId: "set_quantity",
        bindings: buyNowBindings,
        now: validationNow
      )
      let buyStep = try VitlaneRecipeStore.validateCoupangPreparationStep(
        stepId: "buy_now",
        bindings: buyNowBindings,
        now: validationNow
      )
      let stepIndices: [Int] = [verifyStep.stepIndex, quantityStep.stepIndex, buyStep.stepIndex]
      precondition(stepIndices == [0, 1, 2])
      precondition(buyStep.stepCount == 3)

      var progress = VitlanePurchaseSequenceState()
      try progress.accept(verifyStep)
      progress.resolve(verifyStep, applied: true)
      try progress.accept(quantityStep)
      progress.resolve(quantityStep, applied: true)
      try progress.accept(buyStep)
      progress.resolve(buyStep, applied: true)
      precondition(progress.nextStepIndex == 3 && !progress.blocked)

      var uncertain = VitlanePurchaseSequenceState()
      try uncertain.accept(verifyStep)
      uncertain.resolve(verifyStep, applied: false)
      precondition(uncertain.blocked)
      do {
        try uncertain.accept(verifyStep)
        fatalError("An uncertain purchase mutation was retried")
      } catch let failure as VitlaneBrowserFailure {
        precondition(failure.code == "APPROVAL_STEP_MISMATCH")
      }
    } catch {
      fatalError("Valid single-item step sequence failed: \(error)")
    }

    let multiApproval: [String: Any] = [
      "approvalId": "approval_1",
      "approvalDigest": "sha256:e050aef6c7d58c25272e948ff829e9b75d4c401c204db4092c43cb893a0e67fb",
      "currency": "KRW",
      "revision": 1,
      "expiresAt": "2030-01-01T00:05:00.000Z",
      "totalPriceCeilingKrw": 50_000
    ]
    let multiSnapshot: [String: Any] = [
      "merchantId": "COUPANG",
      "recipeVersion": "1",
      "mode": "multi",
      "route": "multi_cart_checkout",
      "approval": multiApproval,
      "items": [firstItem, secondItem]
    ]
    let approvedLines = [firstItem, secondItem].map { item -> [String: Any] in
      let identity = item["offerIdentity"] as! [String: Any]
      return [
        "productId": identity["productId"]!,
        "itemId": identity["itemId"]!,
        "vendorItemId": identity["vendorItemId"]!,
        "quantity": item["quantity"]!,
        "unitPriceCeilingKrw": item["unitPriceCeilingKrw"]!,
        "linePriceCeilingKrw": item["linePriceCeilingKrw"]!
      ]
    }
    let checkoutBindings: [String: Any] = [
      "approvedPreparation": multiSnapshot,
      "approval": multiApproval,
      "approvedLines": approvedLines,
      "expectedOrigin": "https://cart.coupang.com",
      "expectedPath": "/cartView.pang"
    ]
    do {
      let continuity = try VitlaneRecipeStore.validateCoupangPreparation(
        stepId: "start_checkout",
        bindings: checkoutBindings,
        now: validationNow
      )
      precondition(continuity == VitlanePurchaseApprovalContinuity(
        approvalId: "approval_1",
        approvalDigest: multiApproval["approvalDigest"] as! String,
        mode: "multi"
      ))
    } catch {
      fatalError("Valid checkout binding failed: \(error)")
    }

    var mismatchedApproval = singleApproval
    mismatchedApproval["approvalDigest"] = multiApproval["approvalDigest"]
    var mismatchedSnapshot = singleSnapshot
    mismatchedSnapshot["approval"] = mismatchedApproval
    expectPolicyDenied {
      try VitlaneRecipeStore.validateCoupangPreparation(
        stepId: "buy_now",
        bindings: productBindings(
          snapshot: mismatchedSnapshot,
          approval: mismatchedApproval,
          item: firstItem
        ),
        now: validationNow
      )
    }

    var tamperedItem = firstItem
    tamperedItem["query"] = "승인 상품 12345 변경"
    var tamperedSnapshot = singleSnapshot
    tamperedSnapshot["items"] = [tamperedItem]
    expectPolicyDenied {
      try VitlaneRecipeStore.validateCoupangPreparation(
        stepId: "buy_now",
        bindings: productBindings(
          snapshot: tamperedSnapshot,
          approval: singleApproval,
          item: tamperedItem
        ),
        now: validationNow
      )
    }

    var checkoutHostBindings = checkoutBindings
    checkoutHostBindings["expectedOrigin"] = "https://checkout.coupang.com"
    expectPolicyDenied {
      try VitlaneRecipeStore.validateCoupangPreparation(
        stepId: "start_checkout",
        bindings: checkoutHostBindings,
        now: validationNow
      )
    }
    var trailingCartBindings = checkoutBindings
    trailingCartBindings["expectedPath"] = "/cartView.pang/"
    expectPolicyDenied {
      try VitlaneRecipeStore.validateCoupangPreparation(
        stepId: "start_checkout",
        bindings: trailingCartBindings,
        now: validationNow
      )
    }
    var duplicateQueryItem = firstItem
    duplicateQueryItem["productUrl"] = "https://www.coupang.com/vp/products/12345?itemId=23456&itemId=99999&vendorItemId=34567"
    var invalidSnapshot = singleSnapshot
    invalidSnapshot["items"] = [duplicateQueryItem]
    let invalidBindings = productBindings(
      snapshot: invalidSnapshot,
      approval: singleApproval,
      item: duplicateQueryItem
    )
    expectPolicyDenied {
      try VitlaneRecipeStore.validateCoupangPreparation(
        stepId: "buy_now",
        bindings: invalidBindings,
        now: validationNow
      )
    }

    var duplicateOptionGroupItem = firstItem
    duplicateOptionGroupItem["options"] = [
      "kind": "choices",
      "choices": [
        ["groupName": "색상", "valueName": "검정"],
        ["groupName": "색상", "valueName": "흰색"]
      ]
    ]
    var duplicateOptionGroupSnapshot = singleSnapshot
    duplicateOptionGroupSnapshot["items"] = [duplicateOptionGroupItem]
    let duplicateOptionGroupBindings = productBindings(
      snapshot: duplicateOptionGroupSnapshot,
      approval: singleApproval,
      item: duplicateOptionGroupItem
    )
    expectPolicyDenied {
      try VitlaneRecipeStore.validateCoupangPreparation(
        stepId: "buy_now",
        bindings: duplicateOptionGroupBindings,
        now: validationNow
      )
    }
  }

  private static func planItem(
    productId: String,
    itemId: String,
    vendorItemId: String,
    quantity: Int,
    ceiling: Int
  ) -> [String: Any] {
    [
      "query": "승인 상품 \(productId)",
      "offerIdentity": [
        "productId": productId,
        "itemId": itemId,
        "vendorItemId": vendorItemId
      ],
      "quantity": quantity,
      "unitPriceCeilingKrw": ceiling / quantity,
      "linePriceCeilingKrw": ceiling,
      "productUrl": "https://www.coupang.com/vp/products/\(productId)?itemId=\(itemId)&vendorItemId=\(vendorItemId)",
      "options": ["kind": "none"]
    ]
  }

  private static func productBindings(
    snapshot: [String: Any],
    approval: [String: Any],
    item: [String: Any]
  ) -> [String: Any] {
    let identity = item["offerIdentity"] as! [String: Any]
    return [
      "approvedPreparation": snapshot,
      "approval": approval,
      "targetLineIndex": 0,
      "productId": identity["productId"]!,
      "itemId": identity["itemId"]!,
      "vendorItemId": identity["vendorItemId"]!,
      "quantity": item["quantity"]!,
      "unitPriceCeilingKrw": item["unitPriceCeilingKrw"]!,
      "linePriceCeilingKrw": item["linePriceCeilingKrw"]!,
      "expectedOrigin": "https://www.coupang.com",
      "expectedPath": "/vp/products/\(identity["productId"]!)"
    ]
  }

  private static func expectPolicyDenied(_ action: () throws -> Void) {
    do {
      try action()
      fatalError("Invalid Coupang policy input unexpectedly passed")
    } catch let failure as VitlaneBrowserFailure {
      precondition(failure.code == "POLICY_DENIED")
    } catch {
      fatalError("Unexpected policy error: \(error)")
    }
  }
}
