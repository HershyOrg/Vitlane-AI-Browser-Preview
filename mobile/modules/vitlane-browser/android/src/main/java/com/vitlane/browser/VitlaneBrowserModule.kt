package com.vitlane.browser

import android.os.Handler
import android.os.Looper
import expo.modules.kotlin.exception.Exceptions
import expo.modules.kotlin.functions.Queues
import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

class VitlaneBrowserModule : Module() {
  private val main = Handler(Looper.getMainLooper())
  private val pending = ConcurrentHashMap<String, VitlaneBrowserActivity.SettingsCallback>()

  override fun definition() = ModuleDefinition {
    Name("VitlaneBrowser")
    Events("onSettingsRequest")

    AsyncFunction("openAgentBrowser") { apiKey: String, model: String, maxSteps: Int, timeoutSeconds: Int ->
      val activity = appContext.currentActivity ?: throw Exceptions.MissingActivity()
      VitlaneBrowserActivity.launch(activity, apiKey, model, maxSteps, timeoutSeconds) {
          inputKey, selectedModel, steps, seconds, callback ->
        val id = UUID.randomUUID().toString()
        pending[id] = callback
        // This event goes only to the app's React Native runtime, never to a web page.
        sendEvent("onSettingsRequest", mapOf(
          "requestId" to id, "apiKey" to inputKey, "model" to selectedModel,
          "maxSteps" to steps, "timeoutSeconds" to seconds
        ))
        main.postDelayed({
          pending.remove(id)?.onComplete("", "", 20, 300,
            "설정을 저장하지 못했습니다. 브라우저를 닫고 다시 열어 주세요.")
        }, 15000)
      }
    }.runOnQueue(Queues.MAIN)

    AsyncFunction("finishSettingsRequest") {
        requestId: String, apiKey: String, model: String, maxSteps: Int, timeoutSeconds: Int, error: String ->
      val callback = pending.remove(requestId)
      callback?.onComplete(apiKey, model, maxSteps, timeoutSeconds, error.ifEmpty { null })
    }.runOnQueue(Queues.MAIN)

    OnDestroy {
      val callbacks = pending.values.toList()
      pending.clear()
      main.removeCallbacksAndMessages(null)
      main.post {
        callbacks.forEach {
          it.onComplete("", "", 20, 300, "설정 연결이 종료됐습니다. 앱에서 다시 열어 주세요.")
        }
      }
    }
  }
}
