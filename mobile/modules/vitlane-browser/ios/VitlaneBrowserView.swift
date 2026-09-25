import ExpoModulesCore
import UIKit
import WebKit

private final class VitlanePendingAction {
  let commandId: String
  let generation: Int
  let serial: Int
  let promise: Promise
  let purchaseStep: VitlanePurchaseStepContinuity?

  init(
    commandId: String,
    generation: Int,
    serial: Int,
    promise: Promise,
    purchaseStep: VitlanePurchaseStepContinuity? = nil
  ) {
    self.commandId = commandId
    self.generation = generation
    self.serial = serial
    self.promise = promise
    self.purchaseStep = purchaseStep
  }
}

final class VitlaneBrowserView: ExpoView, WKNavigationDelegate, WKUIDelegate {
  let onPageIdentity = EventDispatcher()
  let onControlStateChange = EventDispatcher()
  let onObservation = EventDispatcher()
  let onActionResult = EventDispatcher()
  let onHandoff = EventDispatcher()
  let onNavigationError = EventDispatcher()

  private let tabId = "ios-main"
  private let contentWorld = WKContentWorld.world(name: "VitlaneAgent")
  private let controlBar = VitlaneBrowserControlBar()
  private(set) var webView: WKWebView!

  private var mode: VitlaneControlMode = .paused
  private var runId: String?
  private var deviceId: String?
  private var profileRef: String?
  private var leaseEpoch: Int?
  private var controlGeneration = 1
  private var documentEpoch = 0
  private var lastSequence = 0
  private var acceptedCommandIds = Set<String>()
  private var lastObservation: VitlanePageBinding?
  private var lastObservedAbsoluteURL: String?
  private var lastCandidateKinds = [String: String]()
  private var pageReady = false
  private var generationSynchronized = false
  private var foreground = true
  private var operationPending = false
  private var pendingAction: VitlanePendingAction?
  private var coupangNavigationAllowance: VitlaneCoupangNavigationAllowance?
  private var purchaseSequence = VitlanePurchaseSequenceState()
  private var operationSerial = 0

  required init(appContext: AppContext? = nil) {
    super.init(appContext: appContext)
    clipsToBounds = true
    backgroundColor = .systemBackground
    foreground = UIApplication.shared.applicationState == .active

    let userContentController = WKUserContentController()
    if let source = Self.bundledPageAgentSource() {
      let script = WKUserScript(
        source: source,
        injectionTime: .atDocumentStart,
        forMainFrameOnly: true,
        in: contentWorld
      )
      userContentController.addUserScript(script)
    }

    let preferences = WKWebpagePreferences()
    preferences.allowsContentJavaScript = true

    let configuration = WKWebViewConfiguration()
    configuration.defaultWebpagePreferences = preferences
    configuration.userContentController = userContentController
    // Merchant authentication belongs to the app-owned browser profile. The
    // default WKWebsiteDataStore keeps cookies and local storage inside this
    // app so a user can sign in during a handoff and continue in the same
    // browser session. Credentials and cookies are never exported to React
    // Native or the model.
    configuration.websiteDataStore = .default()
    configuration.allowsInlineMediaPlayback = true
    configuration.preferences.javaScriptCanOpenWindowsAutomatically = false

    webView = WKWebView(frame: .zero, configuration: configuration)
    webView.navigationDelegate = self
    webView.uiDelegate = self
    webView.allowsBackForwardNavigationGestures = true
    webView.allowsLinkPreview = true
    webView.scrollView.contentInsetAdjustmentBehavior = .never
    webView.translatesAutoresizingMaskIntoConstraints = false
    webView.accessibilityIdentifier = "vitlane.browser.webview"

    controlBar.translatesAutoresizingMaskIntoConstraints = false
    controlBar.onStop = { [weak self] in self?.stopFromNativeBar() }
    controlBar.onTakeOverOrResume = { [weak self] in self?.takeOverOrResumeFromNativeBar() }
    controlBar.onBack = { [weak self] in self?.navigateBackFromNativeBar() }
    controlBar.onReload = { [weak self] in self?.reloadFromNativeBar() }

    addSubview(controlBar)
    addSubview(webView)
    NSLayoutConstraint.activate([
      controlBar.topAnchor.constraint(equalTo: topAnchor),
      controlBar.leadingAnchor.constraint(equalTo: leadingAnchor),
      controlBar.trailingAnchor.constraint(equalTo: trailingAnchor),
      controlBar.heightAnchor.constraint(greaterThanOrEqualToConstant: 112),
      webView.topAnchor.constraint(equalTo: controlBar.bottomAnchor),
      webView.leadingAnchor.constraint(equalTo: leadingAnchor),
      webView.trailingAnchor.constraint(equalTo: trailingAnchor),
      webView.bottomAnchor.constraint(equalTo: bottomAnchor)
    ])

    let tapRecognizer = UITapGestureRecognizer(target: self, action: #selector(userInteractedWithPage(_:)))
    tapRecognizer.cancelsTouchesInView = false
    webView.addGestureRecognizer(tapRecognizer)
    webView.scrollView.panGestureRecognizer.addTarget(self, action: #selector(userInteractedWithPage(_:)))

    NotificationCenter.default.addObserver(
      self,
      selector: #selector(applicationWillResignActive),
      name: UIApplication.willResignActiveNotification,
      object: nil
    )
    NotificationCenter.default.addObserver(
      self,
      selector: #selector(applicationDidBecomeActive),
      name: UIApplication.didBecomeActiveNotification,
      object: nil
    )
    updateControlBar()
  }

  deinit {
    NotificationCenter.default.removeObserver(self)
    webView?.navigationDelegate = nil
    webView?.uiDelegate = nil
  }

  func load(urlString: String?) {
    guard let urlString else { return }
    guard let url = URL(string: urlString), VitlaneURLPolicy.isWebURL(url) else {
      emitNavigationError(
        code: "invalid_url",
        message: VitlaneBrowserResources.localized("browser.error.navigation"),
        recoverable: true
      )
      return
    }
    if webView.url == url { return }
    if mode == .agent {
      transition(to: .user, reason: "external_navigation_request", stopLoading: true)
    }
    invalidateObservation(incrementDocument: false)
    pageReady = false
    generationSynchronized = false
    webView.load(URLRequest(url: url, cachePolicy: .useProtocolCachePolicy, timeoutInterval: 30))
    updateControlBar(url: url)
  }

  func beginRun(_ binding: [String: Any], promise: Promise) {
    do {
      guard binding.hasExactlyKeys(["runId", "deviceId", "profileRef", "leaseEpoch"]),
        let newRunId = binding.nonEmptyString("runId"), Self.isValidIdentifier(newRunId),
        let newDeviceId = binding.nonEmptyString("deviceId"), Self.isValidIdentifier(newDeviceId),
        let newProfileRef = binding.nonEmptyString("profileRef"), Self.isValidIdentifier(newProfileRef),
        let newLeaseEpoch = binding.int("leaseEpoch"), newLeaseEpoch > 0
      else {
        throw VitlaneBrowserFailure("invalid_run_binding", "The browser run binding is invalid.")
      }
      guard foreground, pageReady, let currentURL = webView.url else {
        throw VitlaneBrowserFailure("page_not_ready", "Wait for the page to finish loading before starting a run.")
      }
      guard VitlaneURLPolicy.isPublicAgentURL(currentURL), VitlaneURLPolicy.nativeSensitiveReason(currentURL) == nil else {
        throw VitlaneBrowserFailure("handoff_required", "This page must remain under user control.")
      }
      if let existingRunId = runId,
        let existingDeviceId = deviceId,
        let existingProfileRef = profileRef,
        let existingLeaseEpoch = leaseEpoch
      {
        guard newDeviceId == existingDeviceId, newProfileRef == existingProfileRef else {
          throw VitlaneBrowserFailure("run_binding_replacement_forbidden", "A mounted browser cannot change its device or profile binding.")
        }
        let distinctRun = newRunId != existingRunId
        let higherLease = newLeaseEpoch > existingLeaseEpoch
        guard distinctRun || higherLease else {
          throw VitlaneBrowserFailure("run_replay", "This run binding is already active or has an older lease.")
        }
      }
      runId = newRunId
      deviceId = newDeviceId
      profileRef = newProfileRef
      leaseEpoch = newLeaseEpoch
      lastSequence = 0
      acceptedCommandIds.removeAll(keepingCapacity: true)
      purchaseSequence.reset()
      transition(to: .agent, reason: "run_started", stopLoading: false) { [weak self] synchronized in
        guard let self else { return }
        if synchronized {
          promise.resolve(self.controlSnapshot())
        } else {
          promise.reject("generation_sync_failed", "The page agent did not acknowledge browser control.")
        }
      }
    } catch let failure as VitlaneBrowserFailure {
      promise.reject(failure.code, failure.message)
    } catch {
      promise.reject("run_start_failed", "The browser run could not be started.")
    }
  }

  func stopAgent(reason: String?) -> [String: Any] {
    transition(to: .stopped, reason: Self.safeReason(reason, fallback: "user_stopped"), stopLoading: true)
    return controlSnapshot()
  }

  func takeOver() -> [String: Any] {
    transition(to: .user, reason: "user_takeover", stopLoading: false)
    return controlSnapshot()
  }

  func resumeAgent(_ promise: Promise) {
    do {
      guard runId != nil, deviceId != nil, profileRef != nil, leaseEpoch != nil else {
        throw VitlaneBrowserFailure("run_required", "Start a browser run before resuming the agent.")
      }
      guard foreground else {
        throw VitlaneBrowserFailure("foreground_required", "Bring the app to the foreground before resuming.")
      }
      guard pageReady, let currentURL = webView.url else {
        throw VitlaneBrowserFailure("page_not_ready", "Wait for the page to finish loading before resuming.")
      }
      guard VitlaneURLPolicy.isPublicAgentURL(currentURL), VitlaneURLPolicy.nativeSensitiveReason(currentURL) == nil else {
        throw VitlaneBrowserFailure("handoff_required", "This page must remain under user control.")
      }
      transition(to: .agent, reason: "agent_resumed", stopLoading: false) { [weak self] synchronized in
        guard let self else { return }
        if synchronized {
          promise.resolve(self.controlSnapshot())
        } else {
          promise.reject("generation_sync_failed", "The page agent did not acknowledge browser control.")
        }
      }
    } catch let failure as VitlaneBrowserFailure {
      promise.reject(failure.code, failure.message)
    } catch {
      promise.reject("resume_failed", "AI control could not be resumed.")
    }
  }

  func goBack() {
    guard webView.canGoBack else { return }
    transition(to: .user, reason: "user_navigation", stopLoading: false)
    webView.goBack()
  }

  func reload() {
    transition(to: .user, reason: "user_navigation", stopLoading: false)
    webView.reload()
  }

  func observe(_ promise: Promise) {
    do {
      try assertAgentCanOperate()
      guard !operationPending else {
        throw VitlaneBrowserFailure("operation_in_progress", "Another browser operation is still running.")
      }
      if let reason = VitlaneURLPolicy.nativeSensitiveReason(webView.url) {
        resolveBlockedObservation(reason: reason, promise: promise)
        return
      }
      guard VitlaneURLPolicy.isPublicAgentURL(webView.url) else {
        requireHandoff(reason: "private_network")
        promise.reject("private_network_blocked", "This origin is outside the public web policy.")
        return
      }

      operationPending = true
      operationSerial += 1
      let serial = operationSerial
      let generation = controlGeneration
      let epoch = documentEpoch
      let source = "return globalThis.__vitlaneAgent.observe(expectedGeneration);"
      webView.callAsyncJavaScript(
        source,
        arguments: ["expectedGeneration": generation],
        in: nil,
        contentWorld: contentWorld
      ) { [weak self] result in
        guard let self else { return }
        self.finishObservation(
          result,
          serial: serial,
          generation: generation,
          epoch: epoch,
          promise: promise
        )
      }
    } catch let failure as VitlaneBrowserFailure {
      promise.reject(failure.code, failure.message)
    } catch {
      promise.reject("observation_failed", "The page could not be observed.")
    }
  }

  func execute(command dictionary: [String: Any], promise: Promise) {
    var commandId = dictionary["commandId"] as? String ?? "unknown"
    var beganOperation = false
    do {
      let command = try VitlaneCommandEnvelope(dictionary: dictionary)
      commandId = command.commandId
      let purchaseStep = try validate(command)
      guard !operationPending else {
        throw VitlaneBrowserFailure("operation_in_progress", "Another browser operation is still running.")
      }
      guard let page = lastObservation else {
        throw VitlaneBrowserFailure("fresh_observation_required", "Observe the page again before executing an action.")
      }

      operationPending = true
      beganOperation = true
      operationSerial += 1
      let serial = operationSerial
      let generation = controlGeneration
      lastSequence = command.sequence
      acceptedCommandIds.insert(command.commandId)

      let kind = command.action.nonEmptyString("kind")
      if kind == "inspect_page" || kind == "finish" {
        operationPending = false
        invalidateObservation(incrementDocument: false)
        resolveAction(
          commandId: command.commandId,
          status: "applied",
          code: kind == "finish" ? "FINISHED" : "OBSERVATION_REQUIRED",
          promise: promise
        )
        return
      }
      if kind == "request_human" {
        operationPending = false
        let reasonCode = command.action.nonEmptyString("reasonCode") ?? "USER_DECISION_REQUIRED"
        requireHandoff(reason: handoffReason(fromProtocolCode: reasonCode))
        resolveAction(commandId: command.commandId, status: "handoff", code: reasonCode, promise: promise)
        return
      }
      if kind == "scroll" {
        let direction = command.action.nonEmptyString("direction")
        guard direction == "up" || direction == "down" else {
          operationPending = false
          throw VitlaneBrowserFailure("invalid_scroll", "The scroll direction is invalid.")
        }
        callScroll(
          commandId: command.commandId,
          direction: direction!,
          page: page,
          generation: generation,
          serial: serial,
          promise: promise
        )
        return
      }
      if kind == "open_candidate" {
        guard
          let candidateRef = command.action.nonEmptyString("candidateRef"),
          Self.isValidIdentifier(candidateRef),
          lastCandidateKinds[candidateRef] == "safe_link"
        else {
          operationPending = false
          throw VitlaneBrowserFailure("REOBSERVE_REQUIRED", "The link candidate is no longer part of this observation.")
        }
        operationPending = false
        requireHandoff(reason: "unsupported_page")
        resolveAction(
          commandId: command.commandId,
          status: "handoff",
          code: "NAVIGATION_REQUIRES_USER",
          promise: promise
        )
        return
      }
      if kind == "run_preparation_step" {
        guard let adapterId = command.action.nonEmptyString("adapterId"),
          command.action.nonEmptyString("recipeVersion") == "1",
          let stepId = command.action.nonEmptyString("stepId"),
          let bindings = command.action.dictionary("bindings")
        else {
          operationPending = false
          throw VitlaneBrowserFailure("POLICY_DENIED", "The preparation recipe is unsupported.")
        }
        if adapterId == VitlaneRecipeStore.coupangAdapterId {
          guard let purchaseStep else {
            operationPending = false
            throw VitlaneBrowserFailure("APPROVAL_REQUIRED", "The purchase preparation approval is missing.")
          }
          try purchaseSequence.accept(purchaseStep)
          callCoupangPreparation(
            commandId: command.commandId,
            stepId: stepId,
            bindings: bindings,
            purchaseStep: purchaseStep,
            page: page,
            generation: generation,
            serial: serial,
            promise: promise
          )
          return
        }
        guard adapterId == "builtin.public-search", stepId == "prepare_query",
          let candidateRef = bindings.nonEmptyString("candidateRef"), Self.isValidIdentifier(candidateRef),
          let query = bindings.nonEmptyString("query")
        else {
          operationPending = false
          throw VitlaneBrowserFailure("POLICY_DENIED", "The preparation recipe is unsupported.")
        }
        guard lastCandidateKinds[candidateRef] == "public_search" else {
          operationPending = false
          throw VitlaneBrowserFailure("REOBSERVE_REQUIRED", "The search candidate is no longer part of this observation.")
        }
        try VitlaneRecipeStore.validatePublicSearchQuery(query)
        callPrepareSearch(
          commandId: command.commandId,
          candidateRef: candidateRef,
          query: query,
          page: page,
          generation: generation,
          serial: serial,
          promise: promise
        )
        return
      }
      operationPending = false
      throw VitlaneBrowserFailure("action_not_allowlisted", "This browser action is not allowlisted.")
    } catch let failure as VitlaneBrowserFailure {
      if beganOperation { operationPending = false }
      let result = actionResult(
        commandId: commandId,
        status: failure.code == "handoff_required" ? "handoff" : "rejected",
        code: failure.code
      )
      onActionResult(result)
      promise.resolve(result)
    } catch {
      if beganOperation { operationPending = false }
      let result = actionResult(
        commandId: commandId,
        status: "rejected",
        code: "command_invalid"
      )
      onActionResult(result)
      promise.resolve(result)
    }
  }

  // MARK: - WKNavigationDelegate

  func webView(
    _ webView: WKWebView,
    decidePolicyFor navigationAction: WKNavigationAction,
    decisionHandler: @escaping (WKNavigationActionPolicy) -> Void
  ) {
    guard let url = navigationAction.request.url, VitlaneURLPolicy.isWebURL(url) else {
      decisionHandler(.cancel)
      requireHandoff(reason: "external_app")
      emitNavigationError(
        code: "external_scheme_blocked",
        message: VitlaneBrowserResources.localized("browser.error.blocked_scheme"),
        recoverable: true
      )
      return
    }

    let isMainFrameRequest = navigationAction.targetFrame?.isMainFrame ?? true
    if isMainFrameRequest && mode == .agent && coupangNavigationAllowance != nil {
      if let allowance = consumeCoupangNavigationAllowance(targetURL: url) {
        decisionHandler(.allow)
        completeCoupangNavigationHandoff(
          allowance,
          reason: VitlaneURLPolicy.nativeSensitiveReason(url) ?? "payment"
        )
      } else {
        decisionHandler(.cancel)
        let reason = VitlaneURLPolicy.isPublicAgentURL(url) ? "unsupported_page" : "private_network"
        requireHandoff(reason: reason)
      }
      return
    }

    let userNavigation: Bool
    switch navigationAction.navigationType {
    case .linkActivated, .formSubmitted, .backForward, .reload, .formResubmitted:
      userNavigation = true
    default:
      userNavigation = false
    }
    if userNavigation && mode == .agent {
      transition(to: .user, reason: "user_navigation", stopLoading: false)
    }

    if isMainFrameRequest && mode == .agent {
      // Only a freshly validated Coupang buy-now/checkout-review activation
      // receives a single main-frame allowance. Every other redirect or
      // script-driven navigation is cancelled before it reaches the network.
      decisionHandler(.cancel)
      let targetIsAllowedSyntax = VitlaneURLPolicy.isPublicAgentURL(url)
        && VitlaneURLPolicy.nativeSensitiveReason(url) == nil
      requireHandoff(reason: targetIsAllowedSyntax ? "unsupported_page" : "private_network")
      return
    }
    if !isMainFrameRequest && mode == .agent {
      let samePublicOrigin = VitlaneURLPolicy.isPublicAgentURL(url)
        && VitlaneURLPolicy.origin(url) == VitlaneURLPolicy.origin(webView.url)
      if !samePublicOrigin {
        decisionHandler(.cancel)
        return
      }
    }

    if navigationAction.targetFrame == nil {
      decisionHandler(.cancel)
      webView.load(navigationAction.request)
      return
    }
    decisionHandler(.allow)
  }

  func webView(_ webView: WKWebView, didStartProvisionalNavigation navigation: WKNavigation!) {
    if pendingAction != nil {
      settlePendingActionAsUnknown(code: "EXECUTION_OUTCOME_UNKNOWN")
    }
    if mode == .agent {
      // Defense in depth for lifecycle paths that bypass policy callbacks.
      requireHandoff(reason: "unsupported_page")
    }
    invalidateObservation(incrementDocument: true)
    pageReady = false
    generationSynchronized = false
    updateControlBar(url: webView.url)
  }

  func webView(_ webView: WKWebView, didCommit navigation: WKNavigation!) {
    updateControlBar(url: webView.url)
  }

  func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
    pageReady = true
    generationSynchronized = false
    if mode == .paused && runId == nil {
      // The first loaded candidate is an interactive user browser until an
      // owner-bound Device Channel exists. Reflect that truth in native chrome.
      transition(to: .user, reason: "page_ready_for_user", stopLoading: false)
    }
    updateControlBar(url: webView.url)
    emitPageIdentity()
    if !VitlaneURLPolicy.isPublicAgentURL(webView.url) {
      requireHandoff(reason: "private_network")
    } else if let reason = VitlaneURLPolicy.nativeSensitiveReason(webView.url) {
      // Login, verification and payment navigation must reach React Native even
      // when the browser was already under user control.
      requireHandoff(reason: reason)
    } else if mode == .agent {
      requireHandoff(reason: "unsupported_page")
    }
  }

  func webView(
    _ webView: WKWebView,
    didFailProvisionalNavigation navigation: WKNavigation!,
    withError error: Error
  ) {
    navigationFailed(error)
  }

  func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: Error) {
    navigationFailed(error)
  }

  func webView(
    _ webView: WKWebView,
    createWebViewWith configuration: WKWebViewConfiguration,
    for navigationAction: WKNavigationAction,
    windowFeatures: WKWindowFeatures
  ) -> WKWebView? {
    if let url = navigationAction.request.url, VitlaneURLPolicy.isWebURL(url) {
      if mode == .agent {
        transition(to: .user, reason: "new_window_navigation", stopLoading: false)
      }
      webView.load(navigationAction.request)
    }
    return nil
  }

  // MARK: - Private execution

  private func finishObservation(
    _ result: Result<Any, Error>,
    serial: Int,
    generation: Int,
    epoch: Int,
    promise: Promise
  ) {
    let operationIsCurrent = serial == operationSerial
    if operationIsCurrent { operationPending = false }
    guard operationIsCurrent, generation == controlGeneration, epoch == documentEpoch, mode == .agent else {
      promise.reject("control_changed", "Browser control changed while observing the page.")
      return
    }
    switch result {
    case .failure:
      promise.reject("observation_failed", "The page could not be observed safely.")
    case .success(let value):
      guard let payload = value as? [String: Any], let observationId = payload.nonEmptyString("observationId") else {
        promise.reject("observation_invalid", "The page returned an invalid observation.")
        return
      }
      if (payload["sensitivePage"] as? Bool) == true {
        let reason = payload.nonEmptyString("blockedReason") ?? "sensitive_page"
        resolveBlockedObservation(reason: reason, observationId: observationId, promise: promise)
        return
      }
      guard let url = webView.url else {
        promise.reject("page_missing", "The current page is unavailable.")
        return
      }
      let binding = VitlanePageBinding(
        tabId: tabId,
        documentEpoch: documentEpoch,
        observationId: observationId,
        sanitizedURL: VitlaneURLPolicy.sanitizedURL(url),
        origin: VitlaneURLPolicy.origin(url),
        pathname: VitlaneURLPolicy.pathname(url),
        queryOrFragmentPresent: url.query != nil || url.fragment != nil,
        title: VitlaneURLPolicy.sanitizedText(payload["title"] as? String ?? webView.title ?? ""),
        foreground: foreground
      )
      let sanitizedCandidates = sanitizeCandidates(
        payload["candidates"] as? [[String: Any]] ?? [],
        origin: binding.origin
      )
      lastObservation = binding
      lastObservedAbsoluteURL = url.absoluteString
      let observation: [String: Any] = [
        "observationId": observationId,
        "nativeMetadata": nativeMetadata(binding),
        "untrustedPageData": [
          "pageTypeHint": sanitizePageType(payload["pageTypeHint"] as? String),
          "sessionStateHint": sanitizeSessionStateHint(payload["sessionStateHint"] as? String),
          "title": binding.title,
          "visibleText": VitlaneURLPolicy.sanitizedText(payload["visibleText"] as? String ?? "", limit: 10_000),
          "candidates": sanitizedCandidates
        ],
        "privacy": [
          "inputValuesOmitted": true,
          "secretsOmitted": true,
          "screenshotIncluded": false,
          "urlQueryAndFragmentOmitted": true,
          "collectionStatus": "sanitized",
          "handoffReasonCodes": [],
          "excludedBoundaryCodes": sanitizeExcludedBoundaryCodes(payload["excludedBoundaryCodes"] as? [String] ?? [])
        ]
      ]
      onObservation(observation)
      promise.resolve(observation)
    }
  }

  private func callScroll(
    commandId: String,
    direction: String,
    page: VitlanePageBinding,
    generation: Int,
    serial: Int,
    promise: Promise
  ) {
    pendingAction = VitlanePendingAction(
      commandId: commandId,
      generation: generation,
      serial: serial,
      promise: promise
    )
    let source = "return globalThis.__vitlaneAgent.scroll(args);"
    let args: [String: Any] = [
      "generation": generation,
      "observationId": page.observationId,
      "direction": direction
    ]
    webView.callAsyncJavaScript(source, arguments: ["args": args], in: nil, contentWorld: contentWorld) {
      [weak self] result in
      self?.finishAction(result, commandId: commandId, generation: generation, serial: serial)
    }
  }

  private func callPrepareSearch(
    commandId: String,
    candidateRef: String,
    query: String,
    page: VitlanePageBinding,
    generation: Int,
    serial: Int,
    promise: Promise
  ) {
    pendingAction = VitlanePendingAction(
      commandId: commandId,
      generation: generation,
      serial: serial,
      promise: promise
    )
    let source = "return globalThis.__vitlaneAgent.prepareSearch(args);"
    let args: [String: Any] = [
      "generation": generation,
      "observationId": page.observationId,
      "candidateRef": candidateRef,
      "query": query
    ]
    webView.callAsyncJavaScript(source, arguments: ["args": args], in: nil, contentWorld: contentWorld) {
      [weak self] result in
      self?.finishAction(result, commandId: commandId, generation: generation, serial: serial)
    }
  }

  private func callCoupangPreparation(
    commandId: String,
    stepId: String,
    bindings: [String: Any],
    purchaseStep: VitlanePurchaseStepContinuity,
    page: VitlanePageBinding,
    generation: Int,
    serial: Int,
    promise: Promise
  ) {
    pendingAction = VitlanePendingAction(
      commandId: commandId,
      generation: generation,
      serial: serial,
      promise: promise,
      purchaseStep: purchaseStep
    )
    if VitlaneRecipeStore.coupangNavigationSteps.contains(stepId) {
      guard
        let sourceURL = webView.url?.absoluteString,
        let allowance = VitlaneCoupangNavigationAllowance(
          commandId: commandId,
          generation: generation,
          serial: serial,
          stepId: stepId,
          sourceURL: sourceURL
        )
      else {
        pendingAction = nil
        operationPending = false
        purchaseSequence.resolve(purchaseStep, applied: false)
        resolveAction(commandId: commandId, status: "rejected", code: "POLICY_DENIED", promise: promise)
        return
      }
      coupangNavigationAllowance = allowance
    } else {
      coupangNavigationAllowance = nil
    }
    var args = bindings
    args["generation"] = generation
    args["observationId"] = page.observationId
    args["stepId"] = stepId
    let source = "return globalThis.__vitlaneAgent.executeCoupangPreparation(args);"
    webView.callAsyncJavaScript(source, arguments: ["args": args], in: nil, contentWorld: contentWorld) {
      [weak self] result in
      self?.finishAction(result, commandId: commandId, generation: generation, serial: serial)
    }
  }

  private func finishAction(
    _ result: Result<Any, Error>,
    commandId: String,
    generation: Int,
    serial: Int
  ) {
    guard
      let pending = pendingAction,
      pending.commandId == commandId,
      pending.generation == generation,
      pending.serial == serial
    else { return }
    pendingAction = nil
    operationPending = false
    let navigationAllowance: VitlaneCoupangNavigationAllowance?
    if
      let allowance = coupangNavigationAllowance,
      allowance.commandId == commandId,
      allowance.generation == generation,
      allowance.serial == serial
    {
      navigationAllowance = allowance
    } else {
      navigationAllowance = nil
    }
    coupangNavigationAllowance = nil
    let controlChanged = serial != operationSerial || generation != controlGeneration || mode != .agent
    if controlChanged {
      if let purchaseStep = pending.purchaseStep {
        purchaseSequence.resolve(purchaseStep, applied: false)
      }
      let outcome = actionResult(
        commandId: commandId,
        status: "outcome_unknown",
        code: "EXECUTION_OUTCOME_UNKNOWN"
      )
      invalidateObservation(incrementDocument: false)
      onActionResult(outcome)
      pending.promise.resolve(outcome)
      return
    }

    switch result {
    case .success(let value):
      invalidateObservation(incrementDocument: false)
      guard
        let payload = value as? [String: Any],
        let code = payload.nonEmptyString("code"),
        Self.isValidIdentifier(code)
      else {
        if let purchaseStep = pending.purchaseStep {
          purchaseSequence.resolve(purchaseStep, applied: false)
        }
        let unknown = actionResult(
          commandId: commandId,
          status: "outcome_unknown",
          code: "EXECUTION_OUTCOME_UNKNOWN"
        )
        onActionResult(unknown)
        pending.promise.resolve(unknown)
        return
      }
      if let purchaseStep = pending.purchaseStep,
        code != purchaseStep.expectedResultCode ||
          (payload["applied"] as? Bool) != true ||
          (payload["outcomeUnknown"] as? Bool) == true
      {
        purchaseSequence.resolve(purchaseStep, applied: false)
        let unknown = actionResult(
          commandId: commandId,
          status: "outcome_unknown",
          code: "EXECUTION_OUTCOME_UNKNOWN"
        )
        onActionResult(unknown)
        pending.promise.resolve(unknown)
        return
      }
      let navigationHandoff = navigationAllowance?.expectedResultCode == code &&
        (payload["applied"] as? Bool) == true &&
        (payload["outcomeUnknown"] as? Bool) != true
      let status = navigationHandoff
        ? "handoff"
        : ((payload["outcomeUnknown"] as? Bool) == true
          ? "outcome_unknown"
          : ((payload["applied"] as? Bool) == true ? "applied" : "rejected"))
      let actionOutcome = actionResult(commandId: commandId, status: status, code: code)
      if let purchaseStep = pending.purchaseStep {
        purchaseSequence.resolve(purchaseStep, applied: true)
      }
      if navigationHandoff {
        requireHandoff(reason: "payment")
      }
      onActionResult(actionOutcome)
      pending.promise.resolve(actionOutcome)
    case .failure(let error):
      if let purchaseStep = pending.purchaseStep {
        purchaseSequence.resolve(purchaseStep, applied: false)
      }
      let code = Self.scriptFailureCode(error)
      if code == "sensitive_page" || code == "final_action_forbidden" || code == "sensitive_input_forbidden" {
        requireHandoff(reason: code == "final_action_forbidden" ? "final_action" : "sensitive_page")
      }
      let preconditionCodes: Set<String> = [
        "generation_mismatch", "stale_observation", "replay_conflict", "sensitive_page",
        "target_changed", "target_not_observed", "sensitive_input_forbidden"
      ]
      let status: String
      let resultCode: String
      if preconditionCodes.contains(code) {
        status = (code == "sensitive_page" || code == "sensitive_input_forbidden") ? "handoff" : "rejected"
        switch code {
        case "replay_conflict": resultCode = "REPLAY_CONFLICT"
        case "target_changed", "target_not_observed", "stale_observation": resultCode = "REOBSERVE_REQUIRED"
        case "sensitive_page", "sensitive_input_forbidden": resultCode = "POLICY_DENIED"
        default: resultCode = "POLICY_DENIED"
        }
      } else {
        status = "outcome_unknown"
        resultCode = "EXECUTION_OUTCOME_UNKNOWN"
      }
      let rejected = actionResult(
        commandId: commandId,
        status: status,
        code: resultCode
      )
      invalidateObservation(incrementDocument: false)
      onActionResult(rejected)
      pending.promise.resolve(rejected)
    }
  }

  private func validate(_ command: VitlaneCommandEnvelope) throws -> VitlanePurchaseStepContinuity? {
    try assertAgentCanOperate()
    guard
      Self.isValidIdentifier(command.commandId),
      Self.isValidIdentifier(command.runId),
      Self.isValidIdentifier(command.deviceId),
      Self.isValidIdentifier(command.profileRef)
    else {
      throw VitlaneBrowserFailure("invalid_envelope", "The command identifiers are invalid.")
    }
    guard
      command.runId == runId,
      command.deviceId == deviceId,
      command.profileRef == profileRef,
      command.leaseEpoch == leaseEpoch
    else {
      throw VitlaneBrowserFailure("command_binding_mismatch", "The command belongs to a different device, profile, run, or lease.")
    }
    guard command.controlGeneration == controlGeneration else {
      throw VitlaneBrowserFailure("generation_mismatch", "Browser control changed after this command was issued.")
    }
    let now = Date()
    guard command.expiresAt > now else {
      throw VitlaneBrowserFailure("command_expired", "The browser command has expired.")
    }
    guard command.expiresAt.timeIntervalSince(now) <= 60 else {
      throw VitlaneBrowserFailure("expiry_too_long", "The browser command validity window is too long.")
    }
    try VitlaneCommandPermit.verify(command)
    let purchaseStep = try validateActionShape(command.action)
    guard command.sequence == lastSequence + 1 else {
      throw VitlaneBrowserFailure("sequence_mismatch", "The browser command sequence is missing or repeated.")
    }
    guard !acceptedCommandIds.contains(command.commandId), Self.isValidIdentifier(command.commandId) else {
      throw VitlaneBrowserFailure("command_replay", "This browser command was already received or has an invalid identifier.")
    }
    guard let page = lastObservation else {
      throw VitlaneBrowserFailure("fresh_observation_required", "Observe the page again before executing an action.")
    }
    guard let commandPage = command.action.dictionary("page") else {
      throw VitlaneBrowserFailure("page_binding_missing", "The authorized action has no page binding.")
    }
    guard
      commandPage.nonEmptyString("tabId") == page.tabId,
      commandPage.nonEmptyString("frameId") == "frame_main",
      commandPage.int("documentEpoch") == page.documentEpoch,
      commandPage.nonEmptyString("observationId") == page.observationId,
      commandPage.nonEmptyString("topOrigin") == page.origin,
      commandPage.nonEmptyString("frameOrigin") == page.origin,
      commandPage.nonEmptyString("pathname") == page.pathname,
      commandPage["queryOrFragmentPresent"] as? Bool == page.queryOrFragmentPresent,
      page.foreground,
      foreground,
      webView.url?.absoluteString == lastObservedAbsoluteURL,
      VitlaneURLPolicy.origin(webView.url) == page.origin
    else {
      throw VitlaneBrowserFailure("page_binding_mismatch", "The page changed after it was observed.")
    }
    guard VitlaneURLPolicy.isPublicAgentURL(webView.url) else {
      requireHandoff(reason: "private_network")
      throw VitlaneBrowserFailure("handoff_required", "This origin is outside the public web policy.")
    }
    guard VitlaneURLPolicy.nativeSensitiveReason(webView.url) == nil else {
      requireHandoff(reason: VitlaneURLPolicy.nativeSensitiveReason(webView.url) ?? "sensitive_page")
      throw VitlaneBrowserFailure("handoff_required", "This page must be handled by the user.")
    }
    if let purchaseStep {
      try purchaseSequence.validate(purchaseStep)
    }
    return purchaseStep
  }

  private func validateActionShape(_ action: [String: Any]) throws -> VitlanePurchaseStepContinuity? {
    guard let kind = action.nonEmptyString("kind"), Self.isValidIdentifier(kind) else {
      throw VitlaneBrowserFailure("POLICY_DENIED", "The action kind is invalid.")
    }
    guard action.boundedString("reason", maximum: 500) != nil else {
      throw VitlaneBrowserFailure("POLICY_DENIED", "The action reason is invalid.")
    }
    guard let page = action.dictionary("page"), page.hasExactlyKeys([
      "tabId", "frameId", "documentEpoch", "observationId", "topOrigin", "frameOrigin",
      "pathname", "queryOrFragmentPresent"
    ]) else {
      throw VitlaneBrowserFailure("REOBSERVE_REQUIRED", "The page binding contains unexpected fields.")
    }

    switch kind {
    case "inspect_page":
      guard action.hasExactlyKeys(["kind", "page", "reason"]) else {
        throw VitlaneBrowserFailure("POLICY_DENIED", "The inspect action contains unexpected fields.")
      }
    case "open_candidate":
      guard action.hasExactlyKeys(["kind", "page", "candidateRef", "reason"]),
        let candidateRef = action.nonEmptyString("candidateRef"), Self.isValidIdentifier(candidateRef)
      else {
        throw VitlaneBrowserFailure("POLICY_DENIED", "The link action is invalid.")
      }
    case "scroll":
      guard action.hasExactlyKeys(["kind", "page", "direction", "reason"]),
        let direction = action.nonEmptyString("direction"), ["up", "down"].contains(direction)
      else {
        throw VitlaneBrowserFailure("POLICY_DENIED", "The scroll action is invalid.")
      }
    case "run_preparation_step":
      guard action.hasExactlyKeys([
        "kind", "page", "adapterId", "recipeVersion", "stepId", "bindings", "reason"
      ]),
        action.nonEmptyString("recipeVersion") == "1",
        let adapterId = action.nonEmptyString("adapterId"),
        let stepId = action.nonEmptyString("stepId"),
        let bindings = action.dictionary("bindings")
      else {
        throw VitlaneBrowserFailure("POLICY_DENIED", "The preparation action is invalid.")
      }
      if adapterId == VitlaneRecipeStore.coupangAdapterId {
        return try VitlaneRecipeStore.validateCoupangPreparationStep(
          stepId: stepId,
          bindings: bindings
        )
      }
      guard adapterId == "builtin.public-search",
        stepId == "prepare_query",
        bindings.hasExactlyKeys(["candidateRef", "query"]),
        let candidateRef = bindings.nonEmptyString("candidateRef"), Self.isValidIdentifier(candidateRef),
        let query = bindings.boundedString("query", minimum: 1, maximum: 160),
        query == query.trimmingCharacters(in: .whitespacesAndNewlines)
      else {
        throw VitlaneBrowserFailure("POLICY_DENIED", "The public search preparation action is invalid.")
      }
      do {
        try VitlaneRecipeStore.validatePublicSearchQuery(query)
      } catch {
        throw VitlaneBrowserFailure("POLICY_DENIED", "The public search query contains sensitive data.")
      }
    case "request_human":
      let allowedReasons: Set<String> = [
        "AUTHENTICATION_REQUIRED", "SENSITIVE_INPUT_REQUIRED", "FORM_SUBMISSION_REQUIRED",
        "PAYMENT_OR_COMMITMENT", "UNSUPPORTED_INTERACTION", "CROSS_ORIGIN_FRAME",
        "PRIVATE_NETWORK_BLOCKED", "PAGE_CHANGED", "USER_DECISION_REQUIRED"
      ]
      guard action.hasExactlyKeys(["kind", "page", "reasonCode", "message", "reason"]),
        let reasonCode = action.nonEmptyString("reasonCode"), allowedReasons.contains(reasonCode),
        action.boundedString("message", minimum: 1, maximum: 1000) != nil
      else {
        throw VitlaneBrowserFailure("POLICY_DENIED", "The handoff action is invalid.")
      }
    case "finish":
      guard action.hasExactlyKeys(["kind", "page", "message", "reason"]),
        action.boundedString("message", minimum: 1, maximum: 4000) != nil
      else {
        throw VitlaneBrowserFailure("POLICY_DENIED", "The finish action is invalid.")
      }
    default:
      throw VitlaneBrowserFailure("POLICY_DENIED", "This action is not allowlisted.")
    }
    return nil
  }

  private func assertAgentCanOperate() throws {
    guard mode == .agent else {
      throw VitlaneBrowserFailure("agent_not_in_control", "Resume the agent before requesting browser work.")
    }
    guard foreground else {
      throw VitlaneBrowserFailure("foreground_required", "Browser work is allowed only in the foreground.")
    }
    guard pageReady, webView.url != nil else {
      throw VitlaneBrowserFailure("page_not_ready", "Wait for the page to finish loading.")
    }
    guard generationSynchronized else {
      throw VitlaneBrowserFailure("generation_sync_pending", "Wait for the page agent to acknowledge browser control.")
    }
    guard runId != nil else {
      throw VitlaneBrowserFailure("run_required", "Start a browser run first.")
    }
  }

  // MARK: - State and events

  private func consumeCoupangNavigationAllowance(targetURL: URL) -> VitlaneCoupangNavigationAllowance? {
    guard var allowance = coupangNavigationAllowance else { return nil }
    coupangNavigationAllowance = nil
    guard
      let pending = pendingAction,
      let currentURL = webView.url?.absoluteString
    else { return nil }
    guard allowance.consume(
      commandId: pending.commandId,
      generation: pending.generation,
      serial: pending.serial,
      currentURL: currentURL,
      targetURL: targetURL
    ) else { return nil }
    return allowance
  }

  private func completeCoupangNavigationHandoff(
    _ allowance: VitlaneCoupangNavigationAllowance,
    reason: String
  ) {
    guard
      let pending = pendingAction,
      pending.commandId == allowance.commandId,
      pending.generation == allowance.generation,
      pending.serial == allowance.serial
    else { return }
    pendingAction = nil
    operationPending = false
    if let purchaseStep = pending.purchaseStep,
      purchaseStep.expectedResultCode == allowance.expectedResultCode
    {
      purchaseSequence.resolve(purchaseStep, applied: true)
    } else if let purchaseStep = pending.purchaseStep {
      purchaseSequence.resolve(purchaseStep, applied: false)
    }
    requireHandoff(reason: reason)
    let result = actionResult(
      commandId: pending.commandId,
      status: "handoff",
      code: allowance.expectedResultCode
    )
    onActionResult(result)
    pending.promise.resolve(result)
  }

  private func transition(
    to newMode: VitlaneControlMode,
    reason: String,
    stopLoading: Bool,
    onAgentReady: ((Bool) -> Void)? = nil
  ) {
    coupangNavigationAllowance = nil
    settlePendingActionAsUnknown(code: "EXECUTION_OUTCOME_UNKNOWN")
    controlGeneration += 1
    operationSerial += 1
    operationPending = false
    mode = newMode
    generationSynchronized = false
    invalidateObservation(incrementDocument: false)
    if stopLoading { webView.stopLoading() }
    updateControlBar()
    var event = controlSnapshot()
    event["reason"] = reason
    onControlStateChange(event)
    guard newMode == .agent else {
      onAgentReady?(false)
      return
    }
    let expectedGeneration = controlGeneration
    synchronizeGenerationWithPage(expectedGeneration: expectedGeneration) { [weak self] synchronized in
      guard let self else { return }
      guard self.mode == .agent, self.controlGeneration == expectedGeneration else {
        onAgentReady?(false)
        return
      }
      self.generationSynchronized = synchronized
      if !synchronized {
        self.mode = .paused
        self.invalidateObservation(incrementDocument: false)
      }
      self.updateControlBar()
      var synchronizedEvent = self.controlSnapshot()
      synchronizedEvent["reason"] = synchronized ? "generation_synchronized" : "generation_sync_failed"
      self.onControlStateChange(synchronizedEvent)
      onAgentReady?(synchronized)
    }
  }

  private func settlePendingActionAsUnknown(code: String) {
    guard let pending = pendingAction else { return }
    pendingAction = nil
    coupangNavigationAllowance = nil
    operationPending = false
    if let purchaseStep = pending.purchaseStep {
      purchaseSequence.resolve(purchaseStep, applied: false)
    }
    let result = actionResult(commandId: pending.commandId, status: "outcome_unknown", code: code)
    onActionResult(result)
    pending.promise.resolve(result)
  }

  private func invalidateObservation(incrementDocument: Bool) {
    if incrementDocument { documentEpoch += 1 }
    lastObservation = nil
    lastObservedAbsoluteURL = nil
    lastCandidateKinds.removeAll(keepingCapacity: true)
  }

  private func synchronizeGenerationWithPage(
    expectedGeneration: Int,
    completion: @escaping (Bool) -> Void
  ) {
    guard pageReady else {
      completion(false)
      return
    }
    let source = "return globalThis.__vitlaneAgent?.setControlGeneration(generation) ?? false;"
    webView.callAsyncJavaScript(
      source,
      arguments: ["generation": expectedGeneration],
      in: nil,
      contentWorld: contentWorld
    ) { result in
      switch result {
      case .success(let value): completion((value as? Bool) == true)
      case .failure: completion(false)
      }
    }
  }

  private func requireHandoff(reason: String) {
    if mode == .agent {
      transition(to: .user, reason: "handoff_\(reason)", stopLoading: false)
    }
    let normalized: String
    switch reason {
    case "login": normalized = "login"
    case "verification": normalized = "verification"
    case "payment": normalized = "payment"
    case "final_action": normalized = "final_action"
    case "external_app": normalized = "external_app"
    case "private_network", "unsupported_page": normalized = "unsupported_page"
    default: normalized = "sensitive_page"
    }
    let messageKey: String
    switch normalized {
    case "login": messageKey = "browser.handoff.login"
    case "verification": messageKey = "browser.handoff.verification"
    case "payment": messageKey = "browser.handoff.payment"
    case "final_action": messageKey = "browser.handoff.final"
    case "unsupported_page": messageKey = "browser.handoff.unsupported"
    default: messageKey = "browser.handoff.sensitive"
    }
    onHandoff([
      "reason": normalized,
      "message": VitlaneBrowserResources.localized(messageKey),
      "origin": VitlaneURLPolicy.origin(webView.url),
      "controlGeneration": controlGeneration
    ])
  }

  private func controlSnapshot() -> [String: Any] {
    [
      "mode": mode.rawValue,
      "runId": runId ?? NSNull(),
      "controlGeneration": controlGeneration,
      "foreground": foreground,
      "pageReady": pageReady && (mode != .agent || generationSynchronized),
      "capabilities": [
        "adapter": "ios-webkit",
        "adapterVersion": "1.0.0",
        "observationMode": "dom-projection",
        "isolatedContentWorld": true,
        "crossOriginFrames": false,
        "screenshots": false,
        "arbitraryJavaScript": false,
        "cookieExport": false,
        "sessionPersistence": "app-scoped",
        "sameSessionCheckout": true,
        "openCandidateAutomation": false,
        "supportedActions": ["inspect_page", "open_candidate", "scroll", "run_preparation_step", "request_human", "finish"]
      ]
    ]
  }

  private func actionResult(
    commandId: String,
    status: String,
    code: String
  ) -> [String: Any] {
    [
      "commandId": commandId,
      "status": status,
      "code": code
    ]
  }

  private func resolveAction(commandId: String, status: String, code: String, promise: Promise) {
    let result = actionResult(commandId: commandId, status: status, code: code)
    onActionResult(result)
    promise.resolve(result)
  }

  private func nativeMetadata(_ binding: VitlanePageBinding) -> [String: Any] {
    [
      "tabId": binding.tabId,
      "frameId": "frame_main",
      "documentEpoch": binding.documentEpoch,
      "topOrigin": binding.origin,
      "frameOrigin": binding.origin,
      "pathname": binding.pathname,
      "queryOrFragmentPresent": binding.queryOrFragmentPresent,
      "foreground": true
    ]
  }

  private func sanitizePageType(_ value: String?) -> String {
    let allowed: Set<String> = ["public", "search", "product", "listing", "unknown"]
    guard let value, allowed.contains(value) else { return "unknown" }
    return value
  }

  private func sanitizeCandidates(_ candidates: [[String: Any]], origin: String) -> [[String: Any]] {
    var candidateRefs = Set<String>()
    var nodeRefs = Set<String>()
    var candidateKinds = [String: String]()
    let sanitized = candidates.prefix(120).compactMap { candidate -> [String: Any]? in
      guard
        let candidateRef = candidate.nonEmptyString("candidateRef"), Self.isValidIdentifier(candidateRef),
        let nodeRef = candidate.nonEmptyString("nodeRef"), Self.isValidIdentifier(nodeRef),
        let kind = candidate.nonEmptyString("kind"),
        let role = candidate.nonEmptyString("role"),
        !candidateRefs.contains(candidateRef),
        !nodeRefs.contains(nodeRef)
      else { return nil }
      candidateRefs.insert(candidateRef)
      nodeRefs.insert(nodeRef)
      let name = VitlaneURLPolicy.sanitizedText(candidate["name"] as? String ?? "", limit: 200)
      if kind == "public_search", role == "searchbox" {
        candidateKinds[candidateRef] = "public_search"
        return [
          "candidateRef": candidateRef,
          "nodeRef": nodeRef,
          "kind": "public_search",
          "role": "searchbox",
          "name": name
        ]
      }
      if
        kind == "safe_link",
        let hrefString = candidate.nonEmptyString("href"),
        let href = URL(string: hrefString),
        VitlaneURLPolicy.isPublicAgentURL(href),
        VitlaneURLPolicy.origin(href) == origin,
        href.query == nil,
        href.fragment == nil
      {
        candidateKinds[candidateRef] = "safe_link"
        return [
          "candidateRef": candidateRef,
          "nodeRef": nodeRef,
          "kind": "safe_link",
          "role": String(role.prefix(40)),
          "name": name,
          "href": href.absoluteString
        ]
      }
      return nil
    }
    lastCandidateKinds = candidateKinds
    return sanitized
  }

  private func resolveBlockedObservation(
    reason: String,
    observationId: String = "obs_\(UUID().uuidString)",
    promise: Promise
  ) {
    guard let url = webView.url else {
      promise.reject("page_missing", "The current page is unavailable.")
      return
    }
    let binding = VitlanePageBinding(
      tabId: tabId,
      documentEpoch: documentEpoch,
      observationId: observationId,
      sanitizedURL: VitlaneURLPolicy.sanitizedURL(url),
      origin: VitlaneURLPolicy.origin(url),
      pathname: VitlaneURLPolicy.pathname(url),
      queryOrFragmentPresent: url.query != nil || url.fragment != nil,
      title: "",
      foreground: foreground
    )
    let reasonCode: String
    switch reason {
    case "login": reasonCode = "AUTHENTICATION_REQUIRED"
    case "authenticated": reasonCode = "AUTHENTICATION_REQUIRED"
    case "verification": reasonCode = "SENSITIVE_INPUT_REQUIRED"
    case "payment": reasonCode = "PAYMENT_OR_COMMITMENT"
    case "form": reasonCode = "FORM_SUBMISSION_REQUIRED"
    default: reasonCode = "SENSITIVE_INPUT_REQUIRED"
    }
    let observation: [String: Any] = [
      "observationId": observationId,
      "nativeMetadata": nativeMetadata(binding),
      "untrustedPageData": [
        "pageTypeHint": "unknown",
        "sessionStateHint": "unknown",
        "title": "",
        "visibleText": "",
        "candidates": []
      ],
      "privacy": [
        "inputValuesOmitted": true,
        "secretsOmitted": true,
        "screenshotIncluded": false,
        "urlQueryAndFragmentOmitted": true,
        "collectionStatus": "handoff_required",
        "handoffReasonCodes": [reasonCode],
        "excludedBoundaryCodes": []
      ]
    ]
    lastObservation = nil
    onObservation(observation)
    requireHandoff(reason: reason)
    promise.resolve(observation)
  }

  private func handoffReason(fromProtocolCode code: String) -> String {
    switch code {
    case "AUTHENTICATION_REQUIRED": return "login"
    case "SENSITIVE_INPUT_REQUIRED": return "verification"
    case "PAYMENT_OR_COMMITMENT": return "payment"
    case "PRIVATE_NETWORK_BLOCKED": return "private_network"
    default: return "unsupported_page"
    }
  }

  private func sanitizeExcludedBoundaryCodes(_ values: [String]) -> [String] {
    let allowed: Set<String> = [
      "FORM_SUBMISSION_REQUIRED",
      "PAYMENT_OR_COMMITMENT",
      "UNSUPPORTED_INTERACTION",
      "CROSS_ORIGIN_FRAME",
      "PRIVATE_NETWORK_BLOCKED"
    ]
    return Array(Set(values.filter { allowed.contains($0) })).sorted()
  }

  private func sanitizeSessionStateHint(_ value: String?) -> String {
    guard let value, ["authenticated", "anonymous", "unknown"].contains(value) else {
      return "unknown"
    }
    return value
  }

  private func emitPageIdentity() {
    guard let url = webView.url else { return }
    onPageIdentity([
      "page": [
        "tabId": tabId,
        "frameId": "frame_main",
        "documentEpoch": documentEpoch,
        "observationId": NSNull(),
        "url": VitlaneURLPolicy.sanitizedURL(url),
        "origin": VitlaneURLPolicy.origin(url),
        "title": VitlaneURLPolicy.sanitizedText(webView.title ?? ""),
        "foreground": foreground
      ],
      "securityState": VitlaneURLPolicy.securityState(url)
    ])
  }

  private func navigationFailed(_ error: Error) {
    let nsError = error as NSError
    if nsError.domain == NSURLErrorDomain && nsError.code == NSURLErrorCancelled {
      updateControlBar()
      return
    }
    settlePendingActionAsUnknown(code: "EXECUTION_OUTCOME_UNKNOWN")
    pageReady = false
    generationSynchronized = false
    invalidateObservation(incrementDocument: false)
    emitNavigationError(
      code: "navigation_failed",
      message: VitlaneBrowserResources.localized("browser.error.navigation"),
      recoverable: true
    )
    updateControlBar()
  }

  private func emitNavigationError(code: String, message: String, recoverable: Bool) {
    onNavigationError(["code": code, "message": message, "recoverable": recoverable])
  }

  private func updateControlBar(url: URL? = nil) {
    controlBar.update(
      url: url ?? webView?.url,
      mode: mode == .agent && !generationSynchronized ? .paused : mode,
      canGoBack: webView?.canGoBack ?? false
    )
  }

  @objc private func userInteractedWithPage(_ recognizer: UIGestureRecognizer) {
    guard recognizer.state == .began || recognizer.state == .ended else { return }
    if mode == .agent {
      transition(to: .user, reason: "user_interaction", stopLoading: false)
    }
  }

  @objc private func applicationWillResignActive() {
    foreground = false
    if mode == .agent {
      transition(to: .paused, reason: "app_inactive", stopLoading: true)
    } else {
      invalidateObservation(incrementDocument: false)
      updateControlBar()
    }
  }

  @objc private func applicationDidBecomeActive() {
    foreground = true
    invalidateObservation(incrementDocument: false)
    updateControlBar()
    var event = controlSnapshot()
    event["reason"] = "app_foregrounded"
    onControlStateChange(event)
    emitPageIdentity()
  }

  private func stopFromNativeBar() {
    _ = stopAgent(reason: "native_stop_button")
  }

  private func takeOverOrResumeFromNativeBar() {
    // Native chrome provides an immediate escape from agent control. Starting
    // or resuming browser work belongs to the owner-bound app/server flow,
    // which must first obtain a new page identity and sanitized observation.
    // A local button must never manufacture an "AI working" state.
    guard mode == .agent else {
      updateControlBar()
      return
    }
    _ = takeOver()
  }

  private func navigateBackFromNativeBar() {
    goBack()
  }

  private func reloadFromNativeBar() {
    reload()
  }

  private static func bundledPageAgentSource() -> String? {
    guard
      let url = VitlaneBrowserResources.bundle.url(forResource: "VitlanePageAgent", withExtension: "js"),
      let source = try? String(contentsOf: url, encoding: .utf8)
    else { return nil }
    return source
  }

  private static func isValidIdentifier(_ value: String) -> Bool {
    value.count <= 128 && value.range(of: #"^[A-Za-z0-9][A-Za-z0-9._:-]*$"#, options: .regularExpression) != nil
  }

  private static func safeReason(_ value: String?, fallback: String) -> String {
    guard let value, isValidIdentifier(value) else { return fallback }
    return value
  }

  private static func scriptFailureCode(_ error: Error) -> String {
    let message = (error as NSError).localizedDescription
    let knownCodes = [
      "generation_mismatch", "stale_observation", "replay_conflict", "sensitive_page",
      "target_changed", "target_not_observed", "sensitive_input_forbidden", "input_unsupported"
    ]
    return knownCodes.first(where: { message.contains($0) }) ?? "action_failed"
  }
}

private final class VitlaneBrowserControlBar: UIVisualEffectView {
  var onStop: (() -> Void)?
  var onTakeOverOrResume: (() -> Void)?
  var onBack: (() -> Void)?
  var onReload: (() -> Void)?

  private let securityImage = UIImageView()
  private let originLabel = UILabel()
  private let statusLabel = UILabel()
  private let scopeLabel = UILabel()
  private let backButton = UIButton(type: .system)
  private let reloadButton = UIButton(type: .system)
  private let stopButton = UIButton(type: .system)
  private let takeOverButton = UIButton(type: .system)

  init() {
    super.init(effect: UIBlurEffect(style: .systemMaterial))
    accessibilityIdentifier = "vitlane.browser.native-control-bar"

    securityImage.preferredSymbolConfiguration = UIImage.SymbolConfiguration(pointSize: 13, weight: .semibold)
    securityImage.setContentHuggingPriority(.required, for: .horizontal)

    originLabel.font = UIFontMetrics(forTextStyle: .subheadline).scaledFont(
      for: .monospacedSystemFont(ofSize: 13, weight: .semibold)
    )
    originLabel.adjustsFontForContentSizeCategory = true
    originLabel.textColor = .label
    originLabel.lineBreakMode = .byTruncatingMiddle
    originLabel.accessibilityLabel = VitlaneBrowserResources.localized("browser.a11y.origin")

    statusLabel.font = UIFont.preferredFont(forTextStyle: .caption1)
    statusLabel.adjustsFontForContentSizeCategory = true
    statusLabel.textAlignment = .center
    statusLabel.layer.cornerRadius = 11
    statusLabel.layer.masksToBounds = true
    statusLabel.setContentHuggingPriority(.required, for: .horizontal)
    statusLabel.accessibilityLabel = VitlaneBrowserResources.localized("browser.a11y.status")

    scopeLabel.font = UIFont.preferredFont(forTextStyle: .caption2)
    scopeLabel.adjustsFontForContentSizeCategory = true
    scopeLabel.textColor = .secondaryLabel
    scopeLabel.numberOfLines = 0
    scopeLabel.lineBreakMode = .byWordWrapping
    scopeLabel.text = VitlaneBrowserResources.localized("browser.scope.summary")
    scopeLabel.accessibilityLabel = VitlaneBrowserResources.localized("browser.a11y.scope")

    configureIconButton(
      backButton,
      symbol: "chevron.backward",
      accessibilityLabel: VitlaneBrowserResources.localized("browser.control.back")
    )
    configureIconButton(
      reloadButton,
      symbol: "arrow.clockwise",
      accessibilityLabel: VitlaneBrowserResources.localized("browser.control.reload")
    )
    backButton.addTarget(self, action: #selector(backPressed), for: .touchUpInside)
    reloadButton.addTarget(self, action: #selector(reloadPressed), for: .touchUpInside)

    var stopConfiguration = UIButton.Configuration.tinted()
    stopConfiguration.title = VitlaneBrowserResources.localized("browser.control.stop")
    stopConfiguration.image = UIImage(systemName: "stop.fill")
    stopConfiguration.imagePadding = 5
    stopConfiguration.cornerStyle = .capsule
    stopConfiguration.baseForegroundColor = .systemRed
    stopButton.configuration = stopConfiguration
    stopButton.addTarget(self, action: #selector(stopPressed), for: .touchUpInside)

    var takeOverConfiguration = UIButton.Configuration.filled()
    takeOverConfiguration.title = VitlaneBrowserResources.localized("browser.control.takeover")
    takeOverConfiguration.cornerStyle = .capsule
    takeOverConfiguration.baseBackgroundColor = .label
    takeOverConfiguration.baseForegroundColor = .systemBackground
    takeOverButton.configuration = takeOverConfiguration
    takeOverButton.addTarget(self, action: #selector(takeOverPressed), for: .touchUpInside)

    let originRow = UIStackView(arrangedSubviews: [securityImage, originLabel, statusLabel])
    originRow.axis = .horizontal
    originRow.alignment = .center
    originRow.spacing = 8

    let spacer = UIView()
    let controlsRow = UIStackView(arrangedSubviews: [backButton, reloadButton, spacer, stopButton, takeOverButton])
    controlsRow.axis = .horizontal
    controlsRow.alignment = .center
    controlsRow.spacing = 8

    let stack = UIStackView(arrangedSubviews: [originRow, scopeLabel, controlsRow])
    stack.axis = .vertical
    stack.spacing = 6
    stack.translatesAutoresizingMaskIntoConstraints = false
    contentView.addSubview(stack)
    NSLayoutConstraint.activate([
      stack.leadingAnchor.constraint(equalTo: contentView.leadingAnchor, constant: 12),
      stack.trailingAnchor.constraint(equalTo: contentView.trailingAnchor, constant: -12),
      stack.topAnchor.constraint(equalTo: contentView.topAnchor, constant: 8),
      stack.bottomAnchor.constraint(equalTo: contentView.bottomAnchor, constant: -8),
      statusLabel.heightAnchor.constraint(greaterThanOrEqualToConstant: 22),
      statusLabel.widthAnchor.constraint(greaterThanOrEqualToConstant: 82),
      backButton.widthAnchor.constraint(equalToConstant: 32),
      reloadButton.widthAnchor.constraint(equalToConstant: 32)
    ])
  }

  @available(*, unavailable)
  required init?(coder: NSCoder) {
    fatalError("init(coder:) has not been implemented")
  }

  func update(url: URL?, mode: VitlaneControlMode, canGoBack: Bool) {
    let securityState = VitlaneURLPolicy.securityState(url)
    let origin = VitlaneURLPolicy.origin(url)
    originLabel.text = origin.isEmpty ? VitlaneBrowserResources.localized("browser.origin.unknown") : origin
    backButton.isEnabled = canGoBack

    switch securityState {
    case "secure":
      securityImage.image = UIImage(systemName: "lock.fill")
      securityImage.tintColor = .systemGreen
      securityImage.accessibilityLabel = VitlaneBrowserResources.localized("browser.security.secure")
    case "insecure":
      securityImage.image = UIImage(systemName: "exclamationmark.triangle.fill")
      securityImage.tintColor = .systemOrange
      securityImage.accessibilityLabel = VitlaneBrowserResources.localized("browser.security.insecure")
    default:
      securityImage.image = UIImage(systemName: "globe")
      securityImage.tintColor = .secondaryLabel
      securityImage.accessibilityLabel = VitlaneBrowserResources.localized("browser.security.unknown")
    }

    statusLabel.text = VitlaneBrowserResources.localized("browser.status.\(mode.rawValue)")
    statusLabel.textColor = mode == .agent ? .systemGreen : .secondaryLabel
    statusLabel.backgroundColor = mode == .agent
      ? UIColor.systemGreen.withAlphaComponent(0.12)
      : UIColor.secondarySystemFill

    let canTakeOver = mode == .agent
    takeOverButton.isEnabled = canTakeOver
    takeOverButton.isHidden = !canTakeOver
    var configuration = takeOverButton.configuration
    configuration?.title = VitlaneBrowserResources.localized("browser.control.takeover")
    takeOverButton.configuration = configuration
  }

  private func configureIconButton(_ button: UIButton, symbol: String, accessibilityLabel: String) {
    var configuration = UIButton.Configuration.plain()
    configuration.image = UIImage(systemName: symbol)
    configuration.cornerStyle = .capsule
    button.configuration = configuration
    button.accessibilityLabel = accessibilityLabel
  }

  @objc private func stopPressed() { onStop?() }
  @objc private func takeOverPressed() { onTakeOverOrResume?() }
  @objc private func backPressed() { onBack?() }
  @objc private func reloadPressed() { onReload?() }
}
