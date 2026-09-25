package com.vitlane.browser;

import android.app.Activity;
import android.app.AlertDialog;
import android.content.Intent;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.net.http.SslError;
import android.os.Bundle;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.os.SystemClock;
import android.text.InputType;
import android.text.InputFilter;
import android.view.Gravity;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewGroup;
import android.view.WindowManager;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputMethodManager;
import android.webkit.CookieManager;
import android.webkit.PermissionRequest;
import android.webkit.RenderProcessGoneDetail;
import android.webkit.SslErrorHandler;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.EditText;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.ScrollView;
import android.widget.TextView;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayInputStream;
import java.net.HttpURLConnection;
import java.net.URI;
import java.net.URLEncoder;
import java.util.UUID;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;

/** Foreground browser with native controls; API credentials never enter page JavaScript. */
public final class VitlaneBrowserActivity extends Activity {
    private static final int INK = 0xff17251b;
    private static final int MUTED = 0xff5b6b60;
    private static final int SURFACE = 0xfff3f7f1;
    private static final int ACCENT = 0xffd7f5b6;
    private static final int DEFAULT_MAX_STEPS = 20;
    private static final int DEFAULT_TIMEOUT_SECONDS = 300;
    private static Session sPendingSession;

    public interface SettingsHandler {
        void save(String apiKeyInput, String model, int maxSteps, int timeoutSeconds, SettingsCallback callback);
    }

    public interface SettingsCallback {
        void onComplete(String apiKey, String model, int maxSteps, int timeoutSeconds, String error);
    }

    private static final class Session {
        final String token = UUID.randomUUID().toString();
        final long createdAt = SystemClock.elapsedRealtime();
        String apiKey;
        final String model;
        final int maxSteps;
        final int timeoutSeconds;
        SettingsHandler settingsHandler;
        Session(String apiKey, String model, int maxSteps, int timeoutSeconds, SettingsHandler settingsHandler) {
            this.apiKey = apiKey;
            this.model = model;
            this.maxSteps = maxSteps;
            this.timeoutSeconds = timeoutSeconds;
            this.settingsHandler = settingsHandler;
        }
    }

    /** Only an opaque, single-use token is carried by the explicit non-exported Activity intent. */
    public static void launch(Activity activity, String apiKey, String model) {
        launch(activity, apiKey, model, DEFAULT_MAX_STEPS, DEFAULT_TIMEOUT_SECONDS);
    }

    public static void launch(Activity activity, String apiKey, String model, int maxSteps, int timeoutSeconds) {
        launch(activity, apiKey, model, maxSteps, timeoutSeconds, null);
    }

    public static void launch(Activity activity, String apiKey, String model, int maxSteps, int timeoutSeconds,
            SettingsHandler settingsHandler) {
        if (activity == null || activity.isFinishing()) {
            throw new IllegalArgumentException("브라우저를 열 수 있는 화면이 없어요. 다시 시도해 주세요.");
        }
        Session session = new Session(apiKey == null ? "" : apiKey.trim(), model == null ? "gpt-5-mini" : model.trim(),
                maxSteps >= 1 && maxSteps <= 20 ? maxSteps : DEFAULT_MAX_STEPS,
                timeoutSeconds >= 30 && timeoutSeconds <= 300 ? timeoutSeconds : DEFAULT_TIMEOUT_SECONDS, settingsHandler);
        synchronized (VitlaneBrowserActivity.class) {
            if (sPendingSession != null) { sPendingSession.apiKey = ""; sPendingSession.settingsHandler = null; }
            sPendingSession = session;
        }
        try {
            activity.startActivity(new Intent(activity, VitlaneBrowserActivity.class)
                    .putExtra("browser_session", session.token));
        } catch (RuntimeException error) {
            synchronized (VitlaneBrowserActivity.class) {
                if (sPendingSession == session) sPendingSession = null;
                session.apiKey = "";
                session.settingsHandler = null;
            }
            throw new IllegalStateException("브라우저를 열지 못했어요. 다시 시도해 주세요.");
        }
        new Handler(Looper.getMainLooper()).postDelayed(() -> {
            synchronized (VitlaneBrowserActivity.class) {
                if (sPendingSession == session) {
                    session.apiKey = "";
                    session.settingsHandler = null;
                    sPendingSession = null;
                }
            }
        }, 30000);
    }

    private final Handler mUi = new Handler(Looper.getMainLooper());
    private final ExecutorService mWorker = Executors.newSingleThreadExecutor();
    private WebView mWeb;
    private EditText mAddress;
    private EditText mGoalInput;
    private TextView mStatus;
    private TextView mOrigin;
    private ProgressBar mProgress;
    private Button mStart;
    private Button mStop;
    private Button mBack;
    private Button mForward;
    private AlertDialog mConfirmation;
    private AlertDialog mSettingsDialog;
    private EditText mSettingsKeyInput;
    private SettingsHandler mSettingsHandler;
    private long mSettingsRequestId;
    private String mApiKey = "";
    private String mModel = "gpt-5-mini";
    private int mMaxSteps = DEFAULT_MAX_STEPS;
    private int mTimeoutSeconds = DEFAULT_TIMEOUT_SECONDS;
    private String mGoal = "";
    private JSONArray mHistory = new JSONArray();
    private boolean mForeground;
    private boolean mDestroyed;
    private boolean mLoading;
    private boolean mRunning;
    private boolean mBusy;
    private boolean mExpectedNavigation;
    private volatile int mGeneration;
    private long mDocumentEpoch;
    private long mNavigationRequest;
    private long mOperationId;
    private long mDeadline;
    private int mSteps;
    private volatile HttpURLConnection mConnection;
    private Future<?> mTask;

    @Override public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        getWindow().setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_SECURE);
        String token = getIntent().getStringExtra("browser_session");
        synchronized (VitlaneBrowserActivity.class) {
            Session session = sPendingSession;
            if (session != null && session.token.equals(token)
                    && SystemClock.elapsedRealtime() - session.createdAt < 30000) {
                mApiKey = session.apiKey;
                mModel = session.model.isEmpty() ? "gpt-5-mini" : session.model;
                mMaxSteps = session.maxSteps;
                mTimeoutSeconds = session.timeoutSeconds;
                mSettingsHandler = session.settingsHandler;
                session.apiKey = "";
                session.settingsHandler = null;
                sPendingSession = null;
            }
        }
        getIntent().removeExtra("browser_session");
        buildUi();
        configureBrowser();
        status(mApiKey.isEmpty() ? "상단 설정에서 API 키와 모델을 입력하면 AI를 사용할 수 있어요."
                : "사이트를 열고 할 일을 입력하세요. 로그인과 최종 결제는 직접 진행해 주세요.");
        navigate("https://www.google.com/", false);
    }

    private void buildUi() {
        LinearLayout root = column();
        root.setBackgroundColor(Color.WHITE);
        root.setFitsSystemWindows(true);
        LinearLayout header = row();
        header.setPadding(dp(12), dp(5), dp(12), dp(5));
        TextView title = label("Vitlane 브라우저", 17, INK);
        title.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
        header.addView(title, new LinearLayout.LayoutParams(0, dp(44), 1));
        header.addView(button("설정", this::showSettings), new LinearLayout.LayoutParams(dp(66), dp(44)));
        header.addView(button("닫기", () -> finish()), new LinearLayout.LayoutParams(dp(66), dp(44)));
        root.addView(header);

        LinearLayout addressRow = row();
        addressRow.setPadding(dp(10), 0, dp(10), dp(4));
        mAddress = input("웹 주소 또는 검색어");
        mAddress.setSingleLine(true);
        mAddress.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_URI);
        mAddress.setImeOptions(EditorInfo.IME_ACTION_GO);
        mAddress.setOnFocusChangeListener((view, focused) -> {
            if (focused && mRunning) stopRun("주소를 직접 입력하고 있어요.");
        });
        mAddress.setOnEditorActionListener((view, action, event) -> {
            if (action == EditorInfo.IME_ACTION_GO) { openAddress(); return true; }
            return false;
        });
        addressRow.addView(mAddress, new LinearLayout.LayoutParams(0, dp(46), 1));
        addressRow.addView(button("이동", this::openAddress), new LinearLayout.LayoutParams(dp(66), dp(46)));
        root.addView(addressRow);

        LinearLayout toolbar = row();
        toolbar.setPadding(dp(8), 0, dp(8), 0);
        mBack = button("뒤로", () -> {
            stopRun("직접 브라우저를 조작하고 있어요.");
            if (mWeb.canGoBack()) mWeb.goBack();
        });
        mForward = button("앞으로", () -> {
            stopRun("직접 브라우저를 조작하고 있어요.");
            if (mWeb.canGoForward()) mWeb.goForward();
        });
        addEqual(toolbar, mBack, 40);
        addEqual(toolbar, mForward, 40);
        addEqual(toolbar, button("새로고침", () -> {
            stopRun("페이지를 새로 불러오고 있어요.");
            mWeb.reload();
        }), 40);
        addEqual(toolbar, button("쿠팡", () -> navigate("https://www.coupang.com/", false)), 40);
        root.addView(toolbar);
        mOrigin = label("", 11, MUTED);
        mOrigin.setPadding(dp(14), 0, dp(14), dp(4));
        root.addView(mOrigin);
        mProgress = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
        mProgress.setMax(100);
        root.addView(mProgress, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(2)));
        mWeb = new WebView(this);
        root.addView(mWeb, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));

        LinearLayout panel = column();
        panel.setBackgroundColor(SURFACE);
        panel.setPadding(dp(12), dp(8), dp(12), dp(8));
        mStatus = label("", 12, MUTED);
        mStatus.setMaxLines(4);
        mStatus.setPadding(0, 0, 0, dp(6));
        panel.addView(mStatus);
        mGoalInput = input("예: 러닝화를 찾아서 가격을 비교해 줘");
        mGoalInput.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_CAP_SENTENCES);
        mGoalInput.setSingleLine(true);
        mGoalInput.setImeOptions(EditorInfo.IME_ACTION_DONE);
        mGoalInput.setOnEditorActionListener((view, action, event) -> {
            if (action == EditorInfo.IME_ACTION_DONE) { startRun(); return true; }
            return false;
        });
        panel.addView(mGoalInput, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(46)));
        LinearLayout controls = row();
        mStart = button("AI 실행", this::startRun);
        mStart.setBackground(background(ACCENT));
        mStop = button("중지", () -> stopRun("작업을 중지했어요. 브라우저를 직접 조작할 수 있어요."));
        addEqual(controls, mStart, 46);
        addEqual(controls, mStop, 46);
        addEqual(controls, button("직접 조작", () -> stopRun("직접 조작 모드예요. 준비가 되면 AI 실행을 눌러 주세요.")), 46);
        panel.addView(controls);
        root.addView(panel);
        setContentView(root);
        updateControls();
    }

    private void showSettings() {
        if (mDestroyed || isFinishing() || mSettingsDialog != null) return;
        stopRun("설정에서 API 키·모델·실행 한도를 변경할 수 있어요.");
        hideKeyboard();
        LinearLayout form = column();
        form.setPadding(dp(20), dp(8), dp(20), dp(4));
        TextView description = label("이 화면에서 바로 API 키와 모델을 입력하세요. 대화 화면의 설정과 함께 저장됩니다.", 13, MUTED);
        description.setPadding(0, 0, 0, dp(12));
        form.addView(description);
        EditText key = settingsField(form, "OpenAI API 키", "sk-로 시작하는 키", "",
                InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD | InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS, 1024);
        mSettingsKeyInput = key;
        key.setImeOptions(EditorInfo.IME_ACTION_NEXT | EditorInfo.IME_FLAG_NO_EXTRACT_UI);
        if (Build.VERSION.SDK_INT >= 26) key.setImportantForAutofill(View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS);
        TextView keyHelp = label("키를 비워 두면 저장된 키를 사용합니다. 기존 키는 화면에 표시하지 않습니다.", 12, MUTED);
        keyHelp.setPadding(0, dp(4), 0, dp(4));
        form.addView(keyHelp);
        EditText model = settingsField(form, "모델 ID", "예: gpt-5-mini", mModel,
                InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS, 200);
        EditText maxSteps = settingsField(form, "최대 실행 횟수 (1~20회)", "20", String.valueOf(mMaxSteps),
                InputType.TYPE_CLASS_NUMBER, 2);
        EditText timeout = settingsField(form, "작업 제한 시간 (30~300초)", "300", String.valueOf(mTimeoutSeconds),
                InputType.TYPE_CLASS_NUMBER, 3);
        TextView limitsHelp = label("횟수는 AI가 다음 동작을 결정하는 횟수입니다. 한도에 도달하면 현재 작업을 멈춥니다.", 12, MUTED);
        limitsHelp.setPadding(0, dp(6), 0, dp(8));
        form.addView(limitsHelp);
        TextView error = label("", 12, 0xffa33131);
        error.setPadding(0, dp(4), 0, dp(4));
        form.addView(error);
        ScrollView scroll = new ScrollView(this);
        scroll.setFillViewport(true);
        scroll.addView(form);
        AlertDialog dialog = new AlertDialog.Builder(this).setTitle("AI 브라우저 설정")
                .setView(scroll).setPositiveButton("저장", null).setNegativeButton("취소", null).create();
        mSettingsDialog = dialog;
        dialog.setOnDismissListener(ignored -> {
            key.setText("");
            if (mSettingsDialog == dialog) {
                mSettingsDialog = null;
                mSettingsKeyInput = null;
                mSettingsRequestId++;
            }
        });
        dialog.show();
        if (dialog.getWindow() != null) {
            dialog.getWindow().addFlags(WindowManager.LayoutParams.FLAG_SECURE);
            dialog.getWindow().setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
        }
        dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener(ignored -> {
            String keyInput = key.getText().toString().trim();
            String nextModel = model.getText().toString().trim();
            int nextSteps;
            int nextTimeout;
            error.setText("");
            if (!keyInput.isEmpty() && !validKey(keyInput)) {
                error.setText("sk-로 시작하고 공백이 없는 OpenAI API 키를 입력해 주세요.");
                key.requestFocus();
                return;
            }
            if (!validModel(nextModel)) {
                error.setText("모델 ID를 확인해 주세요. 영문·숫자·마침표·하이픈·밑줄·콜론을 사용할 수 있습니다.");
                model.requestFocus();
                return;
            }
            try {
                String stepText = maxSteps.getText().toString().trim();
                String timeoutText = timeout.getText().toString().trim();
                if (!stepText.matches("[0-9]{1,2}") || !timeoutText.matches("[0-9]{2,3}")) throw new IllegalArgumentException();
                nextSteps = Integer.parseInt(stepText);
                nextTimeout = Integer.parseInt(timeoutText);
                if (nextSteps < 1 || nextSteps > 20 || nextTimeout < 30 || nextTimeout > 300) throw new IllegalArgumentException();
            } catch (RuntimeException invalid) {
                error.setText("최대 실행 횟수는 1~20회, 제한 시간은 30~300초로 입력해 주세요.");
                return;
            }
            if (mSettingsHandler == null) {
                error.setText("설정 저장 연결을 사용할 수 없어요. 브라우저를 닫았다가 다시 열어 주세요.");
                return;
            }
            setSettingsSaving(dialog, true, key, model, maxSteps, timeout);
            final long requestId = ++mSettingsRequestId;
            try {
                mSettingsHandler.save(keyInput, nextModel, nextSteps, nextTimeout,
                        (savedKey, savedModel, savedSteps, savedTimeout, failure) -> {
                            if (mDestroyed || !mForeground || mSettingsDialog != dialog || requestId != mSettingsRequestId) return;
                            setSettingsSaving(dialog, false, key, model, maxSteps, timeout);
                            if (failure != null || !validKey(savedKey) || !validModel(savedModel)
                                    || savedSteps < 1 || savedSteps > 20 || savedTimeout < 30 || savedTimeout > 300) {
                                error.setText("설정을 저장하지 못했어요. API 키와 입력값을 확인한 뒤 다시 시도해 주세요.");
                                return;
                            }
                            mApiKey = savedKey;
                            mModel = savedModel;
                            mMaxSteps = savedSteps;
                            mTimeoutSeconds = savedTimeout;
                            key.setText("");
                            dialog.dismiss();
                            status("설정을 저장했어요. 최대 " + mMaxSteps + "회, " + mTimeoutSeconds + "초 동안 실행합니다.");
                            updateControls();
                        });
            } catch (RuntimeException failure) {
                if (mSettingsDialog == dialog && requestId == mSettingsRequestId) {
                    setSettingsSaving(dialog, false, key, model, maxSteps, timeout);
                    error.setText("설정을 저장하지 못했어요. 잠시 후 다시 시도해 주세요.");
                }
            }
        });
    }

    private EditText settingsField(LinearLayout form, String title, String hint, String value, int inputType, int maxLength) {
        TextView label = label(title, 13, INK);
        label.setPadding(0, dp(10), 0, dp(5));
        form.addView(label);
        EditText field = input(hint);
        field.setContentDescription(title);
        field.setSingleLine(true);
        field.setInputType(inputType);
        field.setFilters(new InputFilter[] {new InputFilter.LengthFilter(maxLength)});
        field.setText(value);
        form.addView(field, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(46)));
        return field;
    }

    private static void setSettingsSaving(AlertDialog dialog, boolean saving, EditText... fields) {
        for (EditText field : fields) field.setEnabled(!saving);
        dialog.setCancelable(!saving);
        dialog.getButton(AlertDialog.BUTTON_POSITIVE).setEnabled(!saving);
        dialog.getButton(AlertDialog.BUTTON_POSITIVE).setText(saving ? "저장 중…" : "저장");
        dialog.getButton(AlertDialog.BUTTON_NEGATIVE).setEnabled(!saving);
    }

    private static boolean validKey(String value) {
        return value != null && value.length() <= 1024 && value.matches("sk-[A-Za-z0-9_-]+");
    }

    private static boolean validModel(String value) {
        return value != null && value.matches("[A-Za-z0-9][A-Za-z0-9._:-]{0,199}");
    }

    private void dismissSettings() {
        mSettingsRequestId++;
        if (mSettingsKeyInput != null) { mSettingsKeyInput.setText(""); mSettingsKeyInput = null; }
        if (mSettingsDialog != null) { mSettingsDialog.dismiss(); mSettingsDialog = null; }
    }

    @SuppressWarnings("deprecation")
    private void configureBrowser() {
        WebSettings settings = mWeb.getSettings();
        settings.setJavaScriptEnabled(true);
        settings.setDomStorageEnabled(true);
        settings.setAllowFileAccess(false);
        settings.setAllowContentAccess(false);
        settings.setAllowFileAccessFromFileURLs(false);
        settings.setAllowUniversalAccessFromFileURLs(false);
        settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
        settings.setJavaScriptCanOpenWindowsAutomatically(false);
        settings.setSupportMultipleWindows(false);
        if (Build.VERSION.SDK_INT >= 26) settings.setSafeBrowsingEnabled(true);
        settings.setMediaPlaybackRequiresUserGesture(true);
        settings.setBuiltInZoomControls(true);
        settings.setDisplayZoomControls(false);
        CookieManager.getInstance().setAcceptThirdPartyCookies(mWeb, false);
        mWeb.setOnTouchListener((view, event) -> {
            if (event.getActionMasked() == MotionEvent.ACTION_DOWN && mRunning) {
                stopRun("화면을 터치해서 직접 조작 모드로 전환했어요.");
            }
            return false;
        });
        mWeb.setDownloadListener((url, agent, disposition, mime, length) ->
                stopRun("파일 다운로드는 이 브라우저에서 지원하지 않아요."));
        mWeb.setWebChromeClient(new WebChromeClient() {
            @Override public void onProgressChanged(WebView view, int progress) {
                mProgress.setProgress(progress);
                mProgress.setVisibility(progress == 100 ? View.INVISIBLE : View.VISIBLE);
            }
            @Override public void onPermissionRequest(PermissionRequest request) { request.deny(); }
        });
        mWeb.setWebViewClient(new WebViewClient() {
            @Override public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                if (!allowedUrl(request.getUrl().toString())) {
                    if (request.isForMainFrame()) stopRun("공개 HTTPS 웹사이트만 열 수 있어요.");
                    return true;
                }
                if (request.isForMainFrame() && request.hasGesture() && mRunning) {
                    stopRun("직접 선택한 페이지로 이동하고 있어요.");
                }
                return false;
            }
            @Override public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest request) {
                if (!BrowserUrlPolicy.isPublicNetworkUrl(request.getUrl().toString())) {
                    if (request.isForMainFrame()) mUi.post(() -> stopRun("이 주소는 브라우저에서 열 수 없어요."));
                    return blockedResponse();
                }
                return null;
            }
            @Override public void onPageStarted(WebView view, String url, android.graphics.Bitmap favicon) {
                mDocumentEpoch++;
                mLoading = true;
                if (!allowedUrl(url)) {
                    view.stopLoading();
                    stopRun("공개 HTTPS 웹사이트만 열 수 있어요.");
                    return;
                }
                if (mRunning && !mExpectedNavigation) stopRun("페이지가 바뀌었어요. 확인 후 AI 실행을 다시 눌러 주세요.");
                mAddress.setText(url);
                mOrigin.setText(displayOrigin(url));
                updateControls();
            }
            @Override public void onPageFinished(WebView view, String url) {
                if (mDestroyed || !url.equals(view.getUrl())) return;
                mLoading = false;
                mAddress.setText(url);
                mOrigin.setText(displayOrigin(url));
                updateControls();
                if (mRunning && mExpectedNavigation) {
                    mExpectedNavigation = false;
                    mBusy = false;
                    scheduleObservation(mGeneration, 600);
                }
            }
            @Override public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
                if (request.isForMainFrame()) {
                    mLoading = false;
                    stopRun("페이지를 불러오지 못했어요. 주소와 인터넷 연결을 확인해 주세요.");
                }
            }
            @Override public void onReceivedSslError(WebView view, SslErrorHandler handler, SslError error) {
                handler.cancel();
                stopRun("보안 연결을 확인할 수 없어 페이지 열기를 중지했어요.");
            }
            @Override public boolean onRenderProcessGone(WebView view, RenderProcessGoneDetail detail) {
                mWeb = null;
                if (view.getParent() instanceof ViewGroup) ((ViewGroup) view.getParent()).removeView(view);
                view.destroy();
                stopRun("브라우저가 종료됐어요. 대화 화면에서 다시 열어 주세요.");
                finish();
                return true;
            }
        });
    }

    private void openAddress() {
        String value = mAddress.getText().toString().trim();
        if (value.isEmpty()) return;
        try {
            if (!value.contains("://")) {
                value = value.contains(".") && !value.contains(" ") ? "https://" + value
                        : "https://www.google.com/search?q=" + URLEncoder.encode(value, "UTF-8");
            }
            hideKeyboard();
            navigate(value, false);
        } catch (Exception error) {
            status("올바른 웹 주소나 검색어를 입력해 주세요.");
        }
    }

    private void navigate(String raw, boolean automated) {
        if (!automated) stopRun("페이지를 열고 있어요.");
        final String url;
        try { url = BrowserUrlPolicy.requirePublicHttps(raw); }
        catch (RuntimeException error) { stopRun("공개 HTTPS 웹사이트만 열 수 있어요."); return; }
        final long request = ++mNavigationRequest;
        mOperationId++;
        final int generation = mGeneration;
        if (automated) { mExpectedNavigation = true; mBusy = true; }
        mWorker.execute(() -> {
            boolean allowed = BrowserUrlPolicy.isPublicNetworkUrl(url);
            mUi.post(() -> {
                if (mDestroyed || request != mNavigationRequest || (automated && !valid(generation))) return;
                if (!allowed) { stopRun("이 주소는 브라우저에서 열 수 없어요."); return; }
                mLoading = true;
                mWeb.loadUrl(url);
                updateControls();
                if (automated) navigationWatchdog(generation);
            });
        });
    }

    private void startRun() {
        if (mRunning || mDestroyed) return;
        if (mApiKey.isEmpty()) { status("설정에서 API 키와 모델을 입력해 주세요."); showSettings(); return; }
        if (!mForeground || mLoading || !allowedUrl(mWeb.getUrl())) {
            status("웹페이지 로딩이 끝난 뒤 AI 실행을 눌러 주세요.");
            return;
        }
        mGoal = mGoalInput.getText().toString().trim();
        if (mGoal.isEmpty() || mGoal.length() > 4000) { status("할 일을 4,000자 이내로 입력해 주세요."); return; }
        hideKeyboard();
        mGeneration++;
        mRunning = true;
        mBusy = false;
        mExpectedNavigation = false;
        mSteps = 0;
        mHistory = new JSONArray();
        long timeoutMs = mTimeoutSeconds * 1000L;
        mDeadline = SystemClock.elapsedRealtime() + timeoutMs;
        updateControls();
        status("페이지를 확인하고 있어요. 화면을 터치하면 즉시 직접 조작 모드로 전환돼요.");
        scheduleObservation(mGeneration, 100);
        final int generation = mGeneration;
        mUi.postDelayed(() -> {
            if (mRunning && generation == mGeneration) stopRun("작업 시간이 초과됐어요. 페이지를 확인하고 다시 요청해 주세요.");
        }, timeoutMs);
    }

    private boolean valid(int generation) {
        if (!mRunning || !mForeground || mDestroyed || generation != mGeneration) return false;
        if (SystemClock.elapsedRealtime() >= mDeadline || mSteps > mMaxSteps) {
            stopRun("이번 작업의 실행 한도에 도달했어요. 페이지를 확인하고 다시 요청해 주세요.");
            return false;
        }
        return true;
    }

    private void scheduleObservation(int generation, long delay) {
        mUi.postDelayed(() -> observe(generation), delay);
    }

    private void observe(int generation) {
        if (!valid(generation) || mBusy || mLoading) return;
        if (mSteps >= mMaxSteps) { stopRun("이번 작업의 실행 한도에 도달했어요. 결과를 확인하고 다시 요청해 주세요."); return; }
        mOperationId++;
        mBusy = true;
        final long epoch = mDocumentEpoch;
        final String url = mWeb.getUrl();
        if (!allowedUrl(url)) { stopRun("이 페이지에서는 AI를 실행할 수 없어요."); return; }
        status((mSteps + 1) + "/" + mMaxSteps + " · 페이지 내용을 확인하고 있어요.");
        mWeb.evaluateJavascript(BrowserPageScript.observe(), value -> {
            if (!samePage(generation, epoch, url)) return;
            try {
                JSONObject observation = new JSONObject(value);
                if (!url.equals(observation.optString("url"))) throw new IllegalStateException();
                if (observation.optBoolean("sensitive", true)) {
                    stopRun("로그인·개인정보·주문·결제 화면은 직접 진행해 주세요. AI 페이지 전송을 멈췄어요.");
                    return;
                }
                mSteps++;
                status(mSteps + "/" + mMaxSteps + " · AI가 다음 동작을 정하고 있어요.");
                final String apiKey = mApiKey;
                final String model = mModel;
                final String goal = mGoal;
                final JSONArray history = new JSONArray(mHistory.toString());
                mTask = mWorker.submit(() -> {
                    try {
                        if (generation != mGeneration) return;
                        JSONObject action = BrowserAgent.plan(apiKey, model, goal, observation, history, connection -> {
                            mConnection = connection;
                            if (connection != null && generation != mGeneration) {
                                connection.disconnect();
                                throw new IllegalStateException("CANCELLED");
                            }
                        });
                        mUi.post(() -> {
                            if (samePage(generation, epoch, url)) handleAction(generation, epoch, url, observation, action);
                        });
                    } catch (Exception error) {
                        final String failure = error instanceof BrowserAgent.PlannerException
                                ? clean(error.getMessage(), 240)
                                : "AI 연결에 실패했어요. API 키·사용 한도·인터넷 연결을 확인해 주세요.";
                        mUi.post(() -> {
                            if (generation == mGeneration && mRunning) {
                                stopRun(failure);
                            }
                        });
                    } finally { mConnection = null; }
                });
            } catch (Exception error) {
                stopRun("현재 페이지를 읽지 못했어요. 페이지를 직접 확인해 주세요.");
            }
        });
    }

    private boolean samePage(int generation, long epoch, String url) {
        if (!valid(generation)) return false;
        if (mLoading || epoch != mDocumentEpoch || url == null || !url.equals(mWeb.getUrl())) {
            stopRun("페이지가 바뀌어 AI 동작을 중지했어요. 내용을 확인한 뒤 다시 실행해 주세요.");
            return false;
        }
        return true;
    }

    private void handleAction(int generation, long epoch, String url, JSONObject observation, JSONObject action) {
        try {
            String type = action.getString("type");
            String message = clean(action.optString("message"), 300);
            if ("finish".equals(type) || "handoff".equals(type)) {
                record(type, "handoff".equals(type) ? "handoff" : "applied", message);
                stopRun(message.isEmpty() ? "현재 페이지를 직접 확인해 주세요." : "AI · " + message);
                return;
            }
            if ("navigate".equals(type)) {
                if (sideEffectDestination(action.getString("url"))) {
                    stopRun("계정·장바구니·주문·변경 요청이 포함된 주소는 직접 열어 주세요.");
                    return;
                }
                record(type, "applied", "페이지 이동 요청");
                navigate(action.getString("url"), true);
                return;
            }
            if ("inspect".equals(type)) {
                record(type, "applied", "페이지 확인");
                mBusy = false;
                scheduleObservation(generation, 600);
                return;
            }
            if ("click".equals(type) || "type".equals(type)) {
                JSONObject target = findTarget(observation, action.getString("targetId"));
                if (target == null) throw new IllegalStateException();
                String label = clean(target.optString("label"), 120);
                if (finalCommitment(label)) {
                    stopRun("최종 주문·결제·개인정보 입력은 페이지에서 직접 진행해 주세요.");
                    return;
                }
                if ("type".equals(type)) {
                    if (!target.optBoolean("search", false)) { stopRun("검색 외 입력란은 직접 입력해 주세요."); return; }
                    action.put("submit", action.optBoolean("submit", true));
                } else {
                    String href = target.optString("href");
                    boolean ordinaryLink = !href.isEmpty() && !sideEffectDestination(href) && !sideEffectLabel(label);
                    if (!href.isEmpty() && !allowedUrl(href)) {
                        stopRun("공개 HTTPS 링크만 열 수 있어요.");
                        return;
                    }
                    if (!ordinaryLink && !target.optBoolean("search", false)) {
                        confirmClick(generation, epoch, url, action,
                                label + (href.isEmpty() ? "" : "\n" + clean(href, 240)));
                        return;
                    }
                }
            } else if (!"scroll".equals(type)) {
                throw new IllegalStateException();
            }
            executeAction(generation, epoch, url, action);
        } catch (Exception error) {
            stopRun("실행할 동작을 확인하지 못했어요. 페이지를 직접 확인해 주세요.");
        }
    }

    private void confirmClick(int generation, long epoch, String url, JSONObject action, String label) {
        status("웹페이지 버튼을 누르기 전에 확인이 필요해요.");
        mConfirmation = new AlertDialog.Builder(this).setTitle("이 항목을 실행할까요?")
                .setMessage(displayOrigin(url) + "\n\n" + (label.isEmpty() ? "이름 없는 버튼" : label)
                        + "\n\n사이트에서 선택·장바구니 등의 변경이 일어날 수 있어요. 최종 주문과 결제는 직접 진행합니다.")
                .setPositiveButton("누르기", (dialog, which) -> {
                    mConfirmation = null;
                    if (samePage(generation, epoch, url)) {
                        try {
                            action.put("approved", true);
                            executeAction(generation, epoch, url, action);
                        } catch (Exception error) { stopRun("동작 승인을 확인하지 못했어요."); }
                    }
                }).setNegativeButton("직접 조작", (dialog, which) -> stopRun("직접 조작 모드로 전환했어요."))
                .setOnCancelListener(dialog -> stopRun("동작을 취소했어요. 브라우저를 직접 조작할 수 있어요."))
                .create();
        mConfirmation.show();
        if (mConfirmation.getWindow() != null) mConfirmation.getWindow().addFlags(WindowManager.LayoutParams.FLAG_SECURE);
    }

    private void executeAction(int generation, long epoch, String url, JSONObject action) {
        if (!samePage(generation, epoch, url)) return;
        final String type = action.optString("type");
        final boolean mayNavigate = "click".equals(type) || ("type".equals(type) && action.optBoolean("submit", false));
        final long operation = ++mOperationId;
        mExpectedNavigation = mayNavigate;
        status(mSteps + "/" + mMaxSteps + " · " + ("scroll".equals(type) ? "페이지를 스크롤하고 있어요." : "요청한 동작을 실행하고 있어요."));
        mWeb.evaluateJavascript(BrowserPageScript.action(action, url), value -> {
            if (!valid(generation)) return;
            // A submitted search/click may navigate before its callback reaches Java. Do not repeat it.
            if (mDocumentEpoch != epoch || mLoading || !url.equals(mWeb.getUrl())) {
                if (mayNavigate && mExpectedNavigation) { navigationWatchdog(generation); return; }
                if (!mExpectedNavigation && mDocumentEpoch != epoch) return;
                stopRun("동작 후 페이지가 바뀌었어요. 결과를 직접 확인해 주세요.");
                return;
            }
            try {
                JSONObject result = new JSONObject(value);
                if (!result.optBoolean("ok")) {
                    record(type, "rejected", "페이지 동작을 확인하지 못함");
                    stopRun("페이지에서 동작을 완료하지 못했어요. 결과를 직접 확인해 주세요.");
                    return;
                }
                record(type, "applied", clean(result.optString("message"), 160));
                if (result.optBoolean("navigating", false)) {
                    mExpectedNavigation = true;
                    navigationWatchdog(generation);
                    return;
                }
                // Some sites route asynchronously after their click/submit handler returns.
                mUi.postDelayed(() -> {
                    if (!valid(generation) || operation != mOperationId || mLoading || mDocumentEpoch != epoch) return;
                    mExpectedNavigation = false;
                    mBusy = false;
                    scheduleObservation(generation, 100);
                }, mayNavigate ? 900 : 400);
            } catch (Exception error) {
                record(type, "unknown", "동작 결과를 확인하지 못함");
                stopRun("동작 결과를 확인하지 못했어요. 같은 동작을 다시 실행하지 않고 멈췄어요.");
            }
        });
    }

    private void navigationWatchdog(int generation) {
        final long epoch = mDocumentEpoch;
        final long operation = mOperationId;
        final boolean loading = mLoading;
        mUi.postDelayed(() -> {
            if (!valid(generation) || operation != mOperationId || !mExpectedNavigation) return;
            if (!mLoading && epoch == mDocumentEpoch) {
                mExpectedNavigation = false;
                mBusy = false;
                scheduleObservation(generation, 100);
            } else if (mLoading && loading) {
                stopRun("페이지 이동이 지연되고 있어요. 로딩 후 다시 실행해 주세요.");
            } else if (mLoading) {
                navigationWatchdog(generation);
            }
        }, loading ? 20000 : 1500);
    }

    private void stopRun(String message) {
        mGeneration++;
        mNavigationRequest++;
        mOperationId++;
        mRunning = false;
        mBusy = false;
        mExpectedNavigation = false;
        HttpURLConnection connection = mConnection;
        mConnection = null;
        if (connection != null) connection.disconnect();
        if (mTask != null) { mTask.cancel(true); mTask = null; }
        if (mConfirmation != null) { mConfirmation.dismiss(); mConfirmation = null; }
        if (!mDestroyed) { status(message); updateControls(); }
    }

    private void record(String type, String state, String message) {
        try {
            mHistory.put(new JSONObject().put("type", type).put("status", state).put("message", clean(message, 200)));
            if (mHistory.length() > 20) mHistory.remove(0);
        } catch (Exception ignored) { }
    }

    @Override protected void onResume() {
        super.onResume();
        mForeground = true;
        if (mWeb != null) mWeb.onResume();
        updateControls();
    }

    @Override protected void onPause() {
        mForeground = false;
        dismissSettings();
        stopRun("앱을 벗어나 AI 작업을 중지했어요.");
        if (mWeb != null) mWeb.onPause();
        super.onPause();
    }

    @Override protected void onStop() {
        mApiKey = "";
        mGoal = "";
        mHistory = new JSONArray();
        dismissSettings();
        status("설정을 열어 저장하면 기존 API 키를 다시 사용할 수 있어요. 키 입력란은 비워 두세요.");
        updateControls();
        super.onStop();
    }

    @Override public void onBackPressed() {
        stopRun("직접 브라우저를 조작하고 있어요.");
        if (mWeb != null && mWeb.canGoBack()) mWeb.goBack();
        else super.onBackPressed();
    }

    @Override protected void onDestroy() {
        stopRun("");
        mDestroyed = true;
        mApiKey = "";
        dismissSettings();
        mSettingsHandler = null;
        mGoal = "";
        mHistory = new JSONArray();
        mUi.removeCallbacksAndMessages(null);
        mWorker.shutdownNow();
        if (mWeb != null) {
            mWeb.stopLoading();
            mWeb.setWebChromeClient(null);
            mWeb.setWebViewClient(new WebViewClient());
            if (mWeb.getParent() instanceof ViewGroup) ((ViewGroup) mWeb.getParent()).removeView(mWeb);
            mWeb.destroy();
            mWeb = null;
        }
        super.onDestroy();
    }

    private void updateControls() {
        if (mStart == null) return;
        mStart.setEnabled(!mRunning && !mLoading);
        mStart.setText(mRunning ? "AI 실행 중" : "AI 실행");
        mStop.setEnabled(mRunning);
        mGoalInput.setEnabled(!mRunning);
        if (mWeb != null) {
            mBack.setEnabled(mWeb.canGoBack());
            mForward.setEnabled(mWeb.canGoForward());
        }
    }

    private static JSONObject findTarget(JSONObject observation, String id) throws Exception {
        JSONArray elements = observation.getJSONArray("elements");
        for (int i = 0; i < elements.length(); i++) {
            JSONObject element = elements.getJSONObject(i);
            if (id.equals(element.optString("id"))) return element;
        }
        return null;
    }

    private static boolean finalCommitment(String value) {
        return value.matches("(?is).*(?:결제\\s*(?:하기|확정|완료)|주문\\s*(?:하기|확정|완료)|구매\\s*확정|예약\\s*확정|구독\\s*(?:하기|확정)|취소\\s*확정|place\\s*order|pay\\s*now|confirm\\s*(?:purchase|order|payment|booking)|subscribe\\s*now).*");
    }

    private static boolean sideEffectLabel(String value) {
        return value.matches("(?is).*(?:장바구니|바로\\s*구매|구매\\s*하기|삭제|제거|취소|구독|로그아웃|탈퇴|찜|좋아요|팔로우|\\b(?:add\\s*to\\s*(?:cart|bag|basket)|buy|purchase|delete|remove|cancel|subscribe|unsubscribe|log\\s*out|sign\\s*out|wishlist|follow|like|vote|redeem|claim)\\b).*");
    }

    private static boolean sideEffectDestination(String raw) {
        try {
            URI uri = new URI(raw);
            String path = uri.getPath() == null ? "" : uri.getPath();
            String query = uri.getQuery() == null ? "" : uri.getQuery();
            return path.matches("(?is).*(?:^|/)(?:cart|checkout|payments?|pay|orders?|purchase|buy|logout|signout|unsubscribe|remove|delete|cancel|subscribe|booking|reserve|wishlist|follow|like|vote|redeem|claim)(?:[/._-]|$).*")
                    || query.matches("(?is).*(?:^|&)(?:action|op|operation|method|cmd|do|event)=[^&]*(?:cart|buy|purchase|order|pay|delete|remove|cancel|subscribe|logout|signout|follow|like|vote|redeem|claim)[^&]*(?:&|$).*");
        } catch (Exception error) { return true; }
    }

    private static boolean allowedUrl(String url) {
        try { BrowserUrlPolicy.requirePublicHttps(url); return true; }
        catch (RuntimeException error) { return false; }
    }

    private static WebResourceResponse blockedResponse() {
        return new WebResourceResponse("text/plain", "UTF-8", 403, "Blocked", null,
                new ByteArrayInputStream(new byte[0]));
    }

    private static String displayOrigin(String url) {
        try { URI parsed = new URI(url); return parsed.getScheme() + "://" + parsed.getHost(); }
        catch (Exception ignored) { return "주소 확인 중"; }
    }

    private static String clean(String value, int max) {
        if (value == null) return "";
        String result = value.replaceAll("[\\p{Cc}\\p{Cf}]", " ").replaceAll("\\s+", " ").trim();
        return result.length() > max ? result.substring(0, max) : result;
    }

    private void hideKeyboard() {
        View focused = getCurrentFocus();
        if (focused != null) {
            InputMethodManager manager = (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
            if (manager != null) manager.hideSoftInputFromWindow(focused.getWindowToken(), 0);
            focused.clearFocus();
        }
    }

    private void status(String message) { if (mStatus != null) mStatus.setText(clean(message, 420)); }
    private int dp(int value) { return Math.round(value * getResources().getDisplayMetrics().density); }
    private LinearLayout column() { LinearLayout view = new LinearLayout(this); view.setOrientation(LinearLayout.VERTICAL); return view; }
    private LinearLayout row() { LinearLayout view = new LinearLayout(this); view.setOrientation(LinearLayout.HORIZONTAL); view.setGravity(Gravity.CENTER_VERTICAL); return view; }
    private void addEqual(LinearLayout row, View view, int height) { row.addView(view, new LinearLayout.LayoutParams(0, dp(height), 1)); }
    private TextView label(String text, int size, int color) {
        TextView view = new TextView(this); view.setText(text); view.setTextColor(color); view.setTextSize(size); view.setGravity(Gravity.CENTER_VERTICAL); return view;
    }
    private EditText input(String hint) {
        EditText view = new EditText(this); view.setHint(hint); view.setTextColor(INK); view.setHintTextColor(MUTED);
        view.setTextSize(14); view.setPadding(dp(12), 0, dp(12), 0); view.setBackground(background(Color.WHITE));
        view.setSaveEnabled(false);
        if (Build.VERSION.SDK_INT >= 26) view.setImportantForAutofill(View.IMPORTANT_FOR_AUTOFILL_NO);
        return view;
    }
    private Button button(String text, Runnable action) {
        Button view = new Button(this); view.setText(text); view.setTextSize(12); view.setTextColor(INK); view.setAllCaps(false);
        view.setMinWidth(0); view.setMinimumWidth(0); view.setPadding(dp(5), 0, dp(5), 0); view.setBackground(background(SURFACE));
        view.setOnClickListener(ignored -> action.run()); return view;
    }
    private GradientDrawable background(int color) {
        GradientDrawable drawable = new GradientDrawable(); drawable.setColor(color); drawable.setCornerRadius(dp(10)); return drawable;
    }
}
