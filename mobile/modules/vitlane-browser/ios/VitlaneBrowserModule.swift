import ExpoModulesCore

public class VitlaneBrowserModule: Module {
  public func definition() -> ModuleDefinition {
    Name("VitlaneBrowser")

    View(VitlaneBrowserView.self) {
      Events(
        "onPageIdentity",
        "onControlStateChange",
        "onObservation",
        "onActionResult",
        "onHandoff",
        "onNavigationError"
      )

      Prop("url") { (view: VitlaneBrowserView, url: String?) in
        view.load(urlString: url)
      }

      AsyncFunction("beginRun") { (view: VitlaneBrowserView, binding: [String: Any], promise: Promise) in
        view.beginRun(binding, promise: promise)
      }

      AsyncFunction("observe") { (view: VitlaneBrowserView, promise: Promise) in
        view.observe(promise)
      }

      AsyncFunction("execute") { (view: VitlaneBrowserView, command: [String: Any], promise: Promise) in
        view.execute(command: command, promise: promise)
      }

      AsyncFunction("stopAgent") { (view: VitlaneBrowserView, reason: String?) -> [String: Any] in
        view.stopAgent(reason: reason)
      }

      AsyncFunction("takeOver") { (view: VitlaneBrowserView) -> [String: Any] in
        view.takeOver()
      }

      AsyncFunction("resumeAgent") { (view: VitlaneBrowserView, promise: Promise) in
        view.resumeAgent(promise)
      }

      AsyncFunction("goBack") { (view: VitlaneBrowserView) in
        view.goBack()
      }

      AsyncFunction("reload") { (view: VitlaneBrowserView) in
        view.reload()
      }
    }
  }
}
