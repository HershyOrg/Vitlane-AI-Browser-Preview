package com.vitlane.browser;

import android.animation.ObjectAnimator;
import android.animation.ValueAnimator;
import android.app.Activity;
import android.app.AlertDialog;
import android.Manifest;
import android.content.Intent;
import android.content.pm.PackageManager;
import android.graphics.Color;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.location.Location;
import android.net.http.SslError;
import android.os.Bundle;
import android.os.Build;
import android.os.Handler;
import android.os.Looper;
import android.os.Message;
import android.os.SystemClock;
import android.text.InputType;
import android.text.InputFilter;
import android.util.Base64;
import android.view.Gravity;
import android.view.InputDevice;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewGroup;
import android.view.ViewConfiguration;
import android.view.WindowManager;
import android.view.animation.DecelerateInterpolator;
import android.view.animation.OvershootInterpolator;
import android.view.inputmethod.EditorInfo;
import android.view.inputmethod.InputMethodManager;
import android.webkit.CookieManager;
import android.webkit.GeolocationPermissions;
import android.webkit.JsResult;
import android.webkit.JsPromptResult;
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
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ProgressBar;
import android.widget.ScrollView;
import android.widget.TextView;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.net.HttpURLConnection;
import java.net.URI;
import java.net.URLEncoder;
import java.util.UUID;
import java.util.ArrayList;
import java.util.HashSet;
import java.util.List;
import java.util.Set;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.Future;

/** Foreground chat with an optional browser; API credentials never enter page JavaScript. */
public final class VitlaneBrowserActivity extends Activity {
    private static final int INK = 0xff171821;
    private static final int MUTED = 0xff6f7280;
    private static final int CANVAS = 0xfff5f6fa;
    private static final int SURFACE = 0xfff6f7fb;
    private static final int ACCENT = 0xffeeedff;
    private static final int BRAND = 0xff5856d6;
    private static final int BORDER = 0xffe7e7ef;
    private static final int POSITIVE = 0xff2f9e61;
    private static final int DEFAULT_MAX_STEPS = 20;
    private static final int DEFAULT_TIMEOUT_SECONDS = 300;
    private static final int LOCATION_PERMISSION_REQUEST = 7202;
    private static Session sPendingSession;

    public interface SettingsHandler {
        void request(boolean restore, String apiKeyInput, String model, int maxSteps, int timeoutSeconds, SettingsCallback callback);
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
        Session session = new Session(apiKey == null ? "" : apiKey.trim(), model == null ? BrowserAgent.DEFAULT_MODEL : model.trim(),
                maxSteps >= 1 && maxSteps <= 100 ? maxSteps : DEFAULT_MAX_STEPS,
                timeoutSeconds >= 30 && timeoutSeconds <= 1800 ? timeoutSeconds : DEFAULT_TIMEOUT_SECONDS, settingsHandler);
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
    private volatile WebView mWeb;
    private EditText mAddress;
    private EditText mGoalInput;
    private BrowserChatTimeline mTimeline;
    private LinearLayout mBrowserPane;
    private Button mBrowserToggle;
    private Button mChatTab;
    private Button mPageToggle;
    private TextView mChatMode;
    private TextView mBrowserActivity;
    private View mStatusDot;
    private LinearLayout mComposerPanel;
    private ObjectAnimator mStatusPulse;
    private String mRenderedStartText = "";
    private boolean mPageStateInitialized;
    private boolean mRenderedPageVisible;
    private boolean mTaskControlsShown;
    private volatile boolean mBrowserEnabled = true;
    private String mSuspendedUrl = "";
    private boolean mPageVisible;
    private boolean mResumeAfterRendererRecovery;
    private boolean mChatBusy;
    private boolean mRouting;
    private volatile long mRouteSerial;
    private long mLocationRouteSerial;
    private String mLocationRouteText = "";
    private JSONArray mLocationRouteConversation;
    private BrowserLocationProvider mLocationProvider;
    private boolean mLocationUseAuthorized;
    private String mQueuedGoal = "";
    private volatile long mChatSerial;
    private long mConversationSerial;
    private JSONArray mChatMessages = new JSONArray();
    private JSONArray mBrowserConversation = new JSONArray();
    private Runnable mSubmitHumanAnswer;
    private TextView mStatus;
    private TextView mOrigin;
    private ProgressBar mProgress;
    private Button mStart;
    private Button mStop;
    private Button mBack;
    private Button mForward;
    private Button mResume;
    private Button mDetails;
    private Button mManual;
    private LinearLayout mHumanPanel;
    private LinearLayout mRunControls;
    private LinearLayout mTaskControls;
    private TextView mHumanQuestion;
    private EditText mHumanAnswer;
    private String mHumanMode = "";
    private long mHumanSerial;
    private JSONArray mUserReplies = new JSONArray();
    private JSONObject mAnswerGrant;
    private final List<EditText> mHumanFields = new ArrayList<>();
    private BrowserTouchPolicy mTouch;
    private long mUserScrollUntil;
    private AlertDialog mSettingsDialog;
    private AlertDialog mSecretDialog;
    private AlertDialog mSitePromptDialog;
    private JsPromptResult mSitePromptResult;
    private EditText mSettingsKeyInput;
    private SettingsHandler mSettingsHandler;
    private long mSettingsRequestId;
    private boolean mRestoreSettings;
    private boolean mRestoringSettings;
    private String mApiKey = "";
    private String mModel = BrowserAgent.DEFAULT_MODEL;
    private int mMaxSteps = DEFAULT_MAX_STEPS;
    private int mTimeoutSeconds = DEFAULT_TIMEOUT_SECONDS;
    private String mGoal = "";
    private JSONArray mHistory = new JSONArray();
    private final BrowserTaskMemory mMemory = new BrowserTaskMemory();
    private BrowserTaskContract mContract;
    private JSONObject mResearch = BrowserResearchPlanner.empty("");
    private int mPlannerRepairs;
    private JSONObject mLastObservation;
    private JSONObject mPendingAction;
    private JSONObject mBeforeAction;
    private String mLastResult = "";
    private boolean mHasTask;
    private boolean mCompleted;
    private volatile long mPlanSerial;
    private long mObservationSerial;
    private long mSettleStarted;
    private long mActionStarted;
    private int mRecoveries;
    private int mNoProgress;
    private String mLastActionKey = "";
    private int mRepeatedAction;
    private int mLoopRepairs;
    private boolean mForeground;
    private volatile boolean mDestroyed;
    private boolean mLoading;
    private long mLoadSerial;
    private boolean mRunning;
    private boolean mBusy;
    private boolean mExpectedNavigation;
    private boolean mNativeTapInProgress;
    private long mAgentGestureUntil;
    private volatile int mGeneration;
    private long mDocumentEpoch;
    private volatile long mNavigationRequest;
    private long mOperationId;
    private long mDeadline;
    private int mSteps;
    private volatile HttpURLConnection mConnection;
    private Future<?> mTask;
    private BrowserPrivateDataStore mPrivateData;
    private final Set<String> mPersonalDataFailures = new HashSet<>();
    private final Set<String> mPersonalDataApplied = new HashSet<>();
    private final List<String> mSecretRedactions = new ArrayList<>();
    private int mStableObservations;
    private String mStableSignature = "";

    @Override public void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        getWindow().setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
        getWindow().addFlags(WindowManager.LayoutParams.FLAG_SECURE);
        getWindow().setStatusBarColor(CANVAS);
        getWindow().setNavigationBarColor(CANVAS);
        if (Build.VERSION.SDK_INT >= 23) getWindow().getDecorView().setSystemUiVisibility(
                View.SYSTEM_UI_FLAG_LIGHT_STATUS_BAR);
        String token = getIntent().getStringExtra("browser_session");
        synchronized (VitlaneBrowserActivity.class) {
            Session session = sPendingSession;
            if (session != null && session.token.equals(token)
                    && SystemClock.elapsedRealtime() - session.createdAt < 30000) {
                mApiKey = session.apiKey;
                mModel = session.model.isEmpty() ? BrowserAgent.DEFAULT_MODEL : session.model;
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
        mLocationProvider = new BrowserLocationProvider(this, mUi);
        configureBrowser();
        mPrivateData = new BrowserPrivateDataStore(this);
        mForeground = true;
        BrowserTaskService.attach(this);
        status(mApiKey.isEmpty() ? "설정에서 API 키와 모델을 연결해 주세요." : "메시지를 보내 대화를 시작하세요.");
        mTimeline.addAssistant("무엇을 도와드릴까요?\n필요하면 제가 웹을 찾아보고 작업을 진행합니다. 비밀번호·인증·결제 단계만 브라우저를 보여드리고, 필요한 정보는 이 채팅에서 여쭤볼게요.");
        addStarterSuggestions();
    }

    private void buildUi() {
        LinearLayout root = column();
        root.setBackgroundColor(CANVAS);
        root.setFitsSystemWindows(true);
        LinearLayout header = row();
        header.setPadding(dp(16), dp(8), dp(12), dp(6));
        TextView avatar = label("V", 17, Color.WHITE);
        avatar.setGravity(Gravity.CENTER);
        avatar.setTypeface(Typeface.create("sans-serif-medium", Typeface.BOLD));
        avatar.setBackground(circleBackground(BRAND));
        avatar.setContentDescription("Vitlane");
        LinearLayout.LayoutParams avatarParams = new LinearLayout.LayoutParams(dp(38), dp(38));
        avatarParams.setMarginEnd(dp(10));
        header.addView(avatar, avatarParams);
        LinearLayout identity = column();
        TextView title = label("Vitlane", 19, INK);
        title.setTypeface(Typeface.create("sans-serif-medium", Typeface.BOLD));
        title.setLetterSpacing(-0.02f);
        identity.addView(title, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(24)));
        mChatMode = label("AI가 요청에 맞춰 대화 또는 웹 작업을 선택합니다", 11, MUTED);
        identity.addView(mChatMode, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(18)));
        header.addView(identity, new LinearLayout.LayoutParams(0, dp(44), 1));
        Button fresh = button("새 대화", this::newConversation);
        fresh.setContentDescription("새 대화");
        header.addView(fresh, new LinearLayout.LayoutParams(dp(72), dp(42)));
        Button settings = button("설정", this::showSettings);
        settings.setContentDescription("설정");
        header.addView(settings, new LinearLayout.LayoutParams(dp(56), dp(42)));
        root.addView(header, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(60)));

        LinearLayout tabs = row();
        tabs.setPadding(dp(4), dp(4), dp(4), dp(4));
        tabs.setBackground(background(0xffebeef4, 22));
        mChatTab = button("채팅", () -> setPageVisible(false));
        mChatTab.setContentDescription("채팅 탭");
        mPageToggle = button("브라우저", this::openBrowserPage);
        mPageToggle.setContentDescription("브라우저 탭");
        addEqual(tabs, mChatTab, 38);
        addEqual(tabs, mPageToggle, 38);
        LinearLayout.LayoutParams tabParams = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, dp(46));
        tabParams.setMargins(dp(14), dp(2), dp(14), dp(9));
        root.addView(tabs, tabParams);

        FrameLayout content = new FrameLayout(this);
        content.setBackgroundColor(Color.WHITE);
        root.addView(content, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
        mBrowserPane = column();
        mBrowserPane.setBackgroundColor(Color.WHITE);
        content.addView(mBrowserPane, new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        LinearLayout addressRow = row();
        addressRow.setPadding(dp(12), dp(10), dp(12), dp(6));
        mAddress = input("웹 주소 또는 검색어");
        mAddress.setBackground(strokeBackground(Color.WHITE, 18, BORDER));
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
        Button go = button("이동", this::openAddress);
        go.setTextColor(Color.WHITE);
        go.setBackground(background(BRAND, 16));
        LinearLayout.LayoutParams goParams = new LinearLayout.LayoutParams(dp(62), dp(44));
        goParams.setMarginStart(dp(8));
        addressRow.addView(go, goParams);
        mBrowserPane.addView(addressRow);
        LinearLayout toolbar = row();
        toolbar.setPadding(dp(10), 0, dp(10), dp(6));
        mBack = button("뒤로", () -> { stopRun("직접 브라우저를 조작하고 있어요."); if (mWeb.canGoBack()) mWeb.goBack(); });
        mForward = button("앞으로", () -> { stopRun("직접 브라우저를 조작하고 있어요."); if (mWeb.canGoForward()) mWeb.goForward(); });
        addEqual(toolbar, mBack, 38);
        addEqual(toolbar, mForward, 38);
        addEqual(toolbar, button("새로고침", () -> { stopRun("페이지를 새로 불러오고 있어요."); mWeb.reload(); }), 38);
        addEqual(toolbar, button("채팅에 보기", this::shareCurrentPage), 38);
        mBrowserPane.addView(toolbar);
        mOrigin = label("", 11, MUTED);
        mOrigin.setPadding(dp(14), dp(2), dp(14), dp(3));
        mBrowserPane.addView(mOrigin);
        mBrowserActivity = label("대기 중 · AI 조작은 이 화면에 실시간으로 표시됩니다", 11, MUTED);
        mBrowserActivity.setPadding(dp(14), 0, dp(14), dp(7));
        mBrowserActivity.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
        mBrowserPane.addView(mBrowserActivity);
        mProgress = new ProgressBar(this, null, android.R.attr.progressBarStyleHorizontal);
        mProgress.setMax(100);
        mBrowserPane.addView(mProgress, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(2)));
        mWeb = new WebView(this);
        mBrowserPane.addView(mWeb, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
        mTimeline = new BrowserChatTimeline(this);
        content.addView(mTimeline.getView(), new FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT));
        mHumanPanel = column();
        mHumanPanel.setPadding(dp(14), dp(12), dp(14), dp(12));
        mHumanPanel.setBackground(strokeBackground(Color.WHITE, 20, BORDER));
        mHumanPanel.setElevation(dp(4));
        mHumanPanel.setVisibility(View.GONE);
        mTimeline.addInteraction(mHumanPanel);

        mComposerPanel = column();
        mComposerPanel.setBackground(strokeBackground(Color.WHITE, 24, BORDER));
        mComposerPanel.setPadding(dp(12), dp(8), dp(12), dp(8));
        mComposerPanel.setElevation(dp(10));
        LinearLayout statusRow = row();
        mStatusDot = new View(this);
        mStatusDot.setBackground(circleBackground(POSITIVE));
        LinearLayout.LayoutParams dotParams = new LinearLayout.LayoutParams(dp(8), dp(8));
        dotParams.setMarginStart(dp(2));
        dotParams.setMarginEnd(dp(9));
        statusRow.addView(mStatusDot, dotParams);
        mStatus = label("", 12, MUTED);
        mStatus.setMaxLines(2);
        mStatus.setMinHeight(dp(30));
        mStatus.setAccessibilityLiveRegion(View.ACCESSIBILITY_LIVE_REGION_POLITE);
        mStatus.setPadding(0, 0, 0, dp(2));
        statusRow.addView(mStatus, new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1));
        mComposerPanel.addView(statusRow);
        mRunControls = row();
        mGoalInput = input("메시지를 입력하세요");
        mGoalInput.setBackground(background(SURFACE, 18));
        mGoalInput.setContentDescription("메시지");
        mGoalInput.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_CAP_SENTENCES | InputType.TYPE_TEXT_FLAG_MULTI_LINE);
        mGoalInput.setFilters(new InputFilter[]{new InputFilter.LengthFilter(4000)});
        mGoalInput.setMaxLines(3);
        mGoalInput.setImeOptions(EditorInfo.IME_ACTION_SEND | EditorInfo.IME_FLAG_NO_EXTRACT_UI);
        mGoalInput.setOnEditorActionListener((view, action, event) -> {
            if (action == EditorInfo.IME_ACTION_SEND) { sendMessage(); return true; }
            return false;
        });
        mRunControls.addView(mGoalInput, new LinearLayout.LayoutParams(0, dp(54), 1));
        mStart = button("보내기", this::sendMessage);
        mStart.setTextColor(Color.WHITE);
        mStart.setTextSize(13);
        mStart.setTypeface(Typeface.create("sans-serif-medium", Typeface.BOLD));
        mStart.setBackground(background(BRAND, 18));
        LinearLayout.LayoutParams sendParams = new LinearLayout.LayoutParams(dp(72), dp(48));
        sendParams.setMarginStart(dp(8));
        mRunControls.addView(mStart, sendParams);
        mComposerPanel.addView(mRunControls);
        mTaskControls = row();
        mTaskControls.setPadding(0, dp(6), 0, 0);
        mTaskControls.setVisibility(View.GONE);
        mStop = button("중지", () -> stopRun("작업을 중지했어요. 대화나 직접 조작을 계속할 수 있어요."));
        mResume = button("계속", () -> beginRun(true));
        addEqual(mTaskControls, mStop, 38);
        addEqual(mTaskControls, mResume, 38);
        mDetails = button("작업 기록", this::showTaskDetails);
        addEqual(mTaskControls, mDetails, 38);
        mComposerPanel.addView(mTaskControls);
        TextView privacy = label("대화는 AI에 전달됩니다 · 화면 이미지는 이 대화에서만 표시합니다", 10, MUTED);
        privacy.setGravity(Gravity.CENTER);
        privacy.setPadding(0, dp(5), 0, 0);
        mComposerPanel.addView(privacy);
        LinearLayout.LayoutParams composerParams = new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        composerParams.setMargins(dp(10), dp(8), dp(10), dp(10));
        root.addView(mComposerPanel, composerParams);
        setContentView(root);
        updateBrowserVisibility();
        updateControls();
        animateEntrance(header, 20);
        animateEntrance(tabs, 75);
        animateEntrance(mComposerPanel, 130);
    }

    private void addStarterSuggestions() {
        if (mTimeline == null) return;
        List<View> actions = mTimeline.addSuggestions(
                "상품을 찾아 비교해줘", "여행 옵션을 알아봐줘", "웹 자료를 조사해줘");
        for (View action : actions) action.setOnClickListener(view -> {
            if (!(view instanceof Button) || mRunning || mChatBusy || mRouting) return;
            String prompt = ((Button) view).getText().toString();
            mGoalInput.setText(prompt);
            mGoalInput.setSelection(prompt.length());
            sendMessage();
        });
    }

    private void setBrowserEnabled(boolean enabled) {
        if (mDestroyed || enabled == mBrowserEnabled) return;
        stopRun(enabled ? "브라우저를 켰어요. 웹에서 할 일을 메시지로 보내 주세요." : "브라우저를 껐어요. 일반 대화를 계속할 수 있어요.");
        mBrowserEnabled = enabled;
        mGoalInput.setHint(enabled ? "웹에서 할 일을 입력하세요" : "메시지를 입력하세요");
        mPageVisible = false;
        mConversationSerial++;
        mDocumentEpoch++;
        mLastObservation = null;
        mLoading = false;
        if (enabled) {
            if (mWeb == null) {
                mWeb = new WebView(this);
                mBrowserPane.addView(mWeb, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
                configureBrowser();
            }
            mWeb.onResume();
            if (!allowedUrl(mWeb.getUrl())) navigate(allowedUrl(mSuspendedUrl) ? mSuspendedUrl : "https://www.google.com/", false);
        } else if (mWeb != null) {
            mSuspendedUrl = allowedUrl(mWeb.getUrl()) ? mWeb.getUrl() : "";
            WebView previous = mWeb;
            mWeb = null;
            releaseWebView(previous);
        }
        updateBrowserVisibility();
        updateControls();
    }

    private void releaseWebView(WebView web) {
        web.stopLoading();
        web.setWebChromeClient(null);
        web.setWebViewClient(new WebViewClient());
        web.setOnTouchListener(null);
        web.setOnScrollChangeListener(null);
        if (web.getParent() instanceof ViewGroup) ((ViewGroup)web.getParent()).removeView(web);
        web.destroy();
    }

    private void openBrowserPage() {
        if (!mBrowserEnabled) setBrowserEnabled(true);
        setPageVisible(true);
    }

    private void setPageVisible(boolean visible) {
        if (visible && !mBrowserEnabled) return;
        hideKeyboard();
        mPageVisible = visible;
        updateBrowserVisibility();
    }

    private void updateBrowserVisibility() {
        if (mBrowserPane == null) return;
        // Keep an enabled page laid out behind the opaque chat so DOM actions and snapshots have a real viewport.
        mBrowserPane.setVisibility(mBrowserEnabled ? View.VISIBLE : View.GONE);
        mBrowserPane.setImportantForAccessibility(mPageVisible ? View.IMPORTANT_FOR_ACCESSIBILITY_AUTO : View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS);
        View chat = mTimeline.getView();
        boolean changed = !mPageStateInitialized || mRenderedPageVisible != mPageVisible;
        mPageStateInitialized = true;
        mRenderedPageVisible = mPageVisible;
        if (changed) {
            chat.animate().cancel();
            if (mPageVisible) {
                if (motionEnabled() && chat.isAttachedToWindow() && chat.getVisibility() == View.VISIBLE) {
                    chat.animate().alpha(0f).translationX(-dp(12)).setDuration(180)
                            .setInterpolator(new DecelerateInterpolator()).withEndAction(() -> {
                                if (mPageVisible) chat.setVisibility(View.GONE);
                                chat.setAlpha(1f);
                                chat.setTranslationX(0f);
                            }).start();
                } else {
                    chat.setVisibility(View.GONE);
                    chat.setAlpha(1f);
                    chat.setTranslationX(0f);
                }
            } else {
                chat.setVisibility(View.VISIBLE);
                if (motionEnabled() && chat.isAttachedToWindow()) {
                    chat.setAlpha(0f);
                    chat.setTranslationX(-dp(12));
                    chat.animate().alpha(1f).translationX(0f).setDuration(230)
                            .setInterpolator(new DecelerateInterpolator(1.5f)).start();
                } else {
                    chat.setAlpha(1f);
                    chat.setTranslationX(0f);
                }
            }
        }
        if (mPageToggle != null) {
            mPageToggle.setEnabled(mBrowserEnabled);
            mPageToggle.setText(mRunning ? "브라우저 · 진행 중" : "브라우저");
            mPageToggle.setTextColor(mPageVisible ? BRAND : MUTED);
            mPageToggle.setBackground(background(mPageVisible ? Color.WHITE : Color.TRANSPARENT, 18));
            mPageToggle.setElevation(mPageVisible ? dp(2) : 0);
        }
        if (mChatTab != null) {
            mChatTab.setTextColor(mPageVisible ? MUTED : BRAND);
            mChatTab.setBackground(background(mPageVisible ? Color.TRANSPARENT : Color.WHITE, 18));
            mChatTab.setElevation(mPageVisible ? 0 : dp(2));
        }
        if (mBrowserToggle != null) {
            mBrowserToggle.setText(mBrowserEnabled ? "브라우저 끄기" : "브라우저 켜기");
            mBrowserToggle.setBackground(background(mBrowserEnabled ? ACCENT : SURFACE));
        }
        mChatMode.setText(mPageVisible ? "현재 작업 화면 · 필요하면 직접 이어서 조작하세요" :
                mRunning || mChatBusy || mRouting ? "백그라운드에서 요청을 처리하고 있어요" :
                "대화하고, 필요할 때 웹 작업까지 맡겨보세요");
    }

    private void newConversation() {
        stopRun("새 대화를 시작했어요.");
        mConversationSerial++;
        mChatMessages = new JSONArray();
        mBrowserConversation = new JSONArray();
        mUserReplies = new JSONArray();
        mHistory = new JSONArray();
        mMemory.clear();
        mPersonalDataFailures.clear();
        mPersonalDataApplied.clear();
        mContract = null;
        mResearch = BrowserResearchPlanner.empty("");
        mGoal = "";
        mLastResult = "";
        mLastObservation = null;
        mHasTask = mCompleted = false;
        mGoalInput.setText("");
        if (mTimeline != null) {
            mTimeline.clear();
            mTimeline.addInteraction(mHumanPanel);
            mTimeline.addAssistant("새 대화를 시작했어요. 무엇을 도와드릴까요?");
            addStarterSuggestions();
        }
        setPageVisible(false);
        updateControls();
    }

    private void addChatHistory(String role, String message) {
        if (message == null || message.trim().isEmpty()) return;
        try {
            String bounded = message.length() > 12000 ? message.substring(0, 12000) : message;
            JSONArray next = new JSONArray(mChatMessages.toString());
            next.put(new JSONObject().put("role", role).put("content", bounded));
            mChatMessages = BrowserChat.sanitizeMessages(next);
        } catch (Exception ignored) { }
    }

    private void addUserMessage(String message) {
        if (mTimeline != null) mTimeline.addUser(message);
        addChatHistory("user", message);
    }

    private void addAssistantMessage(String message) {
        if (mTimeline != null) mTimeline.addAssistant(message);
        addChatHistory("assistant", message);
    }

    private void sendMessage() {
        if ("answer".equals(mHumanMode) && mSubmitHumanAnswer != null) { mSubmitHumanAnswer.run(); return; }
        if (mDestroyed || mRunning || mChatBusy || mRouting || !mHumanMode.isEmpty()) return;
        if (mRestoringSettings) { status("저장된 설정을 불러오는 중이에요."); return; }
        if (mApiKey.isEmpty()) { status("설정에서 API 키와 모델을 연결해 주세요."); showSettings(); return; }
        String text = mGoalInput.getText().toString().trim();
        if (text.isEmpty()) return;
        if (BrowserAgent.isSecretUserInput(text)) {
            mGoalInput.setText("");
            if (mTimeline != null) mTimeline.addAssistant("비밀번호·OTP·카드번호는 채팅에 입력하지 마세요. 필요한 사이트 화면에서 기기 보관소를 열어 입력할 수 있습니다.");
            status("보안정보를 AI에 보내지 않았어요.");
            return;
        }
        startRouting(text);
    }

    private void startRouting(String text) {
        if (mLocationProvider != null) mLocationProvider.cancel();
        clearPendingLocationRoute();
        mLocationUseAuthorized = false;
        addUserMessage(text);
        mGoalInput.setText("");
        hideKeyboard();
        mRouting = true;
        status("요청을 이해하고 웹 작업이 필요한지 판단하고 있어요…");
        BrowserTaskService.start(this, "요청을 분석하고 있어요.");
        requestNotificationPermission();
        updateControls();
        final long serial = ++mRouteSerial;
        final String apiKey = mApiKey, model = mModel;
        final JSONArray conversation;
        try { conversation = new JSONArray(mChatMessages.toString()); }
        catch (Exception invalid) { mRouting = false; status("요청을 준비하지 못했어요."); updateControls(); return; }
        mTask = mWorker.submit(() -> {
            try {
                JSONObject decision = BrowserIntentRouter.decide(apiKey, model, text, conversation, connection -> {
                    if (serial != mRouteSerial) { if (connection != null) connection.disconnect(); throw new IllegalStateException("CANCELLED"); }
                    mConnection = connection;
                });
                mUi.post(() -> {
                    if (mDestroyed || serial != mRouteSerial || !mRouting) return;
                    handleRouteDecision(serial, text, conversation, decision);
                });
            } catch (Exception error) {
                String message = error instanceof BrowserIntentRouter.RouteException ? error.getMessage()
                        : "요청을 분류하지 못했어요. API 설정과 인터넷 연결을 확인해 주세요.";
                mUi.post(() -> {
                    if (mDestroyed || serial != mRouteSerial || !mRouting) return;
                    mRouting = false;
                    addAssistantMessage(message + "\n메시지를 다시 보내면 재시도합니다.");
                    status(message);
                    BrowserTaskService.update(this, "요청을 확인하지 못했습니다", message, false);
                    updateControls();
                });
            } finally { if (serial == mRouteSerial) mConnection = null; }
        });
    }

    private void handleRouteDecision(long serial, String text, JSONArray conversation, JSONObject decision) {
        if (mDestroyed || serial != mRouteSerial || !mRouting) return;
        if ("chat".equals(decision.optString("mode"))) {
            mRouting = false;
            startChat(text, false);
            return;
        }
        if (decision.optBoolean("clarificationNeeded")) {
            requestPreSearchClarification(serial, text, conversation, decision);
            return;
        }
        if (!decision.optBoolean("needsLocation")) {
            startBrowserResearch(serial, text, conversation, null);
            return;
        }
        mLocationRouteSerial = serial;
        mLocationRouteText = text;
        try { mLocationRouteConversation = new JSONArray(conversation.toString()); }
        catch (Exception invalid) { mLocationRouteConversation = new JSONArray(); }
        if (!BrowserLocationProvider.hasPermission(this)) {
            status("주변·현재 위치 기준으로 찾기 위해 위치 권한을 요청합니다.");
            requestPermissions(new String[]{Manifest.permission.ACCESS_COARSE_LOCATION,
                    Manifest.permission.ACCESS_FINE_LOCATION}, LOCATION_PERMISSION_REQUEST);
            return;
        }
        resolveLocationForRoute();
    }

    /** Ask one decision-blocking catalog question before spending a search or browser action. */
    private void requestPreSearchClarification(long routeSerial, String originalGoal, JSONArray conversation,
            JSONObject decision) {
        if (mDestroyed || routeSerial != mRouteSerial || !mRouting) return;
        final String question = clean(decision.optString("clarificationQuestion"), 400);
        if (question.isEmpty() || BrowserAgent.isPrivateUserInput(question)) {
            try {
                JSONObject bypass = new JSONObject(decision.toString()).put("clarificationNeeded", false)
                        .put("clarificationQuestion", "").put("clarificationOptions", new JSONArray());
                handleRouteDecision(routeSerial, originalGoal, conversation, bypass);
            } catch (Exception invalid) {
                mRouting = false;
                addAssistantMessage("검색 전 확인 질문을 준비하지 못했어요. 메시지를 다시 보내 주세요.");
                updateControls();
            }
            return;
        }
        String display = "검색 전에 한 가지만 확인할게요.\n" + question;
        showHumanPanel("answer", display, false);
        BrowserTaskService.update(this, "검색 조건을 확인하고 있어요", question, true);
        final long humanSerial = mHumanSerial;
        final EditText answer = mGoalInput;
        answer.setSaveEnabled(false);
        answer.setFilters(new InputFilter[]{new InputFilter.LengthFilter(1000)});
        answer.setMaxLines(3);
        mHumanAnswer = answer;
        mSubmitHumanAnswer = () -> {
            if (routeSerial != mRouteSerial || humanSerial != mHumanSerial || !mRouting
                    || !"answer".equals(mHumanMode)) return;
            String value = answer.getText().toString().trim();
            if (value.isEmpty()) { status("검색에 반영할 답변을 입력해 주세요."); return; }
            if (BrowserAgent.isPrivateUserInput(value)) {
                answer.setText("");
                status("검색 전 질문에는 비밀번호·결제·개인정보를 입력하지 마세요.");
                return;
            }
            try {
                addUserMessage(value);
                JSONArray updatedConversation = new JSONArray(mChatMessages.toString());
                String resolvedGoal = originalGoal + "\n검색 전 사용자 확인 조건: " + question + " → " + clean(value, 1000);
                JSONObject resumed = new JSONObject(decision.toString()).put("clarificationNeeded", false)
                        .put("clarificationQuestion", "").put("clarificationOptions", new JSONArray());
                clearHumanPanel();
                status("확인한 조건을 반영해 검색을 시작합니다…");
                handleRouteDecision(routeSerial, resolvedGoal, updatedConversation, resumed);
            } catch (Exception error) { status("답변을 검색 조건에 반영하지 못했어요. 다시 시도해 주세요."); }
        };
        JSONArray options = decision.optJSONArray("clarificationOptions");
        if (options != null) for (int i = 0; i < options.length(); i++) {
            String option = clean(options.optString(i), 80);
            if (option.isEmpty()) continue;
            mHumanPanel.addView(button(option, () -> {
                if (humanSerial != mHumanSerial || !"answer".equals(mHumanMode)) return;
                answer.setText(option);
                mSubmitHumanAnswer.run();
            }));
        }
        mHumanPanel.addView(button("직접 입력한 답변으로 검색", mSubmitHumanAnswer));
        mHumanPanel.addView(button("요청 취소", () -> stopRun("검색 전 확인을 취소했어요.")));
        updateControls();
    }

    private void resolveLocationForRoute() {
        final long serial = mLocationRouteSerial;
        final String text = mLocationRouteText;
        final JSONArray conversation = mLocationRouteConversation == null ? new JSONArray() : mLocationRouteConversation;
        if (mDestroyed || serial != mRouteSerial || !mRouting) { clearPendingLocationRoute(); return; }
        mLocationUseAuthorized = BrowserLocationProvider.hasPermission(this);
        if (!mLocationUseAuthorized || mLocationProvider == null) {
            clearPendingLocationRoute();
            startBrowserResearch(serial, text, conversation, BrowserLocationContext.unavailable("permission_denied"));
            return;
        }
        status("현재 위치를 확인하고 있어요…");
        mLocationProvider.resolve((location, failure) -> {
            if (mDestroyed || serial != mRouteSerial || !mRouting) return;
            clearPendingLocationRoute();
            if (location == null) startBrowserResearch(serial, text, conversation,
                    BrowserLocationContext.unavailable(failure.isEmpty() ? "unavailable" : failure));
            else startBrowserResearch(serial, text, conversation, location);
        });
    }

    private void startBrowserResearch(long serial, String text, JSONArray conversation, Object location) {
        if (mDestroyed || serial != mRouteSerial || !mRouting) return;
        status("웹 검색으로 답을 찾고, 필요할 때만 브라우저 실행 경로를 만들고 있어요…");
        final String apiKey = mApiKey, model = mModel;
        mTask = mWorker.submit(() -> {
            JSONObject locationContext = null;
            if (location instanceof Location) locationContext = BrowserLocationContext.fromLocation(this, (Location)location);
            else if (location instanceof JSONObject) locationContext = (JSONObject)location;
            JSONObject research;
            boolean directEntryAllowed = false;
            try {
                research = BrowserResearchPlanner.research(apiKey, model, text, conversation, locationContext, connection -> {
                    if (serial != mRouteSerial) { if (connection != null) connection.disconnect(); throw new IllegalStateException("CANCELLED"); }
                    mConnection = connection;
                });
                String entry = BrowserResearchPlanner.entryUrl(research);
                directEntryAllowed = !entry.isEmpty() && BrowserUrlPolicy.isPublicNetworkUrl(entry);
            } catch (BrowserResearchPlanner.ResearchException unavailable) {
                research = BrowserResearchPlanner.empty(unavailable.getMessage());
            } catch (Exception unavailable) {
                research = BrowserResearchPlanner.empty("사전 웹 검색에 연결하지 못했습니다.");
            }
            final JSONObject preparedResearch = research;
            final boolean canOpenEntry = directEntryAllowed;
            mUi.post(() -> completeBrowserResearch(serial, text, preparedResearch, canOpenEntry));
        });
    }

    private void completeBrowserResearch(long serial, String text, JSONObject preparedResearch, boolean canOpenEntry) {
        if (mDestroyed || serial != mRouteSerial || !mRouting) return;
        mRouting = false;
        mResearch = preparedResearch;
        if (BrowserResearchPlanner.isSearchOnly(preparedResearch)) {
            mLastResult = searchAnswer(preparedResearch);
            addAssistantMessage(mLastResult);
            addResearchSourceCards();
            status("웹 검색 결과만으로 답변을 완료했어요. 브라우저 조작은 실행하지 않았습니다.");
            BrowserTaskService.complete(this, "웹 검색 답변이 도착했습니다", clean(mLastResult, 240));
            updateControls();
            return;
        }
        mQueuedGoal = text;
        String entry = canOpenEntry ? BrowserResearchPlanner.entryUrl(preparedResearch) : "";
        String destination = entry.isEmpty() ? "https://www.google.com/" : entry;
        status(preparedResearch.optBoolean("searched")
                ? "검색만으로 끝낼 수 없어 목표 페이지에서 실제 상태를 확인합니다."
                : "사전 검색을 사용할 수 없어 브라우저에서 직접 조사합니다. "
                        + clean(preparedResearch.optString("summary"), 180));
        if (mWeb != null && (!allowedUrl(mWeb.getUrl()) || !destination.equals(mWeb.getUrl()))) {
            trackPageLoad(mWeb);
            mWeb.loadUrl(destination);
            updateControls();
        } else if (!mLoading) beginRun(false);
        else updateControls();
    }

    private String searchAnswer(JSONObject research) {
        StringBuilder result = new StringBuilder(BrowserResearchPlanner.answer(research));
        JSONArray selected = research.optJSONArray("answerSourceUrls"), sources = research.optJSONArray("sources");
        if (selected == null || selected.length() == 0) return result.toString();
        result.append("\n\n출처");
        for (int i = 0; i < selected.length(); i++) {
            String url = selected.optString(i), title = "";
            if (sources != null) for (int j = 0; j < sources.length(); j++) {
                JSONObject source = sources.optJSONObject(j);
                if (source != null && url.equals(source.optString("url"))) { title = source.optString("title"); break; }
            }
            result.append("\n• ").append(title.isEmpty() ? url : title).append("\n").append(url);
        }
        return result.toString();
    }

    private void clearPendingLocationRoute() {
        mLocationRouteSerial = 0;
        mLocationRouteText = "";
        mLocationRouteConversation = null;
    }

    private void startChat(String text) { startChat(text, true); }

    private void startChat(String text, boolean addMessage) {
        if (addMessage) {
            addUserMessage(text);
            mGoalInput.setText("");
            hideKeyboard();
        }
        mChatBusy = true;
        status("답변을 준비하고 있어요…");
        BrowserTaskService.update(this, "Vitlane AI가 답변 중입니다", "앱을 벗어나도 계속 처리합니다.", false);
        updateControls();
        final long serial = ++mChatSerial;
        final String apiKey = mApiKey, model = mModel;
        final JSONArray messages;
        try { messages = new JSONArray(mChatMessages.toString()); }
        catch (Exception invalid) { stopRun("대화를 준비하지 못했어요. 다시 보내 주세요."); return; }
        mTask = mWorker.submit(() -> {
            try {
                if (serial != mChatSerial) return;
                JSONObject reply = BrowserChat.reply(apiKey, model, messages, connection -> {
                    if (serial != mChatSerial) { if (connection != null) connection.disconnect(); throw new IllegalStateException("CANCELLED"); }
                    mConnection = connection;
                });
                mUi.post(() -> {
                    if (mDestroyed || serial != mChatSerial || !mChatBusy) return;
                    mChatBusy = false;
                    addAssistantMessage(reply.optString("message"));
                    status(reply.optBoolean("incomplete") ? "답변이 일부만 생성됐어요. 이어서 요청할 수 있습니다." : "메시지를 보내 대화를 이어가세요.");
                    BrowserTaskService.complete(this, "답변이 도착했습니다", clean(reply.optString("message"), 240));
                    updateControls();
                });
            } catch (Exception error) {
                final String message = error instanceof BrowserChat.ChatException ? error.getMessage() : "답변을 받지 못했어요. API 설정과 인터넷 연결을 확인해 주세요.";
                mUi.post(() -> {
                    if (mDestroyed || serial != mChatSerial || !mChatBusy) return;
                    mChatBusy = false;
                    if (mTimeline != null) mTimeline.addAssistant(message + "\n메시지를 다시 보내면 재시도합니다.");
                    status(message);
                    updateControls();
                });
            } finally { if (serial == mChatSerial) mConnection = null; }
        });
    }

    private View addPageMessage(String message, JSONObject expected) {
        if (mTimeline == null) return null;
        View bubble = mTimeline.addAssistantMessage(message);
        addChatHistory("assistant", message);
        attachPageSnapshot(bubble, expected);
        return bubble;
    }

    private void attachPageSnapshot(View bubble, JSONObject expected) {
        attachPageSnapshot(bubble, expected, true);
    }

    /** User-only RAM preview permission. This flag is never copied into model input. */
    private static boolean localPreviewSafe(JSONObject observation) {
        return observation != null && !observation.optBoolean("sensitive", true)
                && observation.optBoolean("localPreviewSafe", false);
    }

    private void attachPageSnapshot(View bubble, JSONObject expected, boolean preferSourceImage) {
        if (bubble == null || mDestroyed || !mForeground || !mBrowserEnabled || mWeb == null || mLoading
                || !localPreviewSafe(expected) || !allowedUrl(mWeb.getUrl())) return;
        final long epoch = mDocumentEpoch, conversation = mConversationSerial, operation = mOperationId;
        final String url = mWeb.getUrl(), document = expected.optString("documentId");
        if (document.isEmpty() || !url.equals(expected.optString("url"))) return;
        final String signature = BrowserObservationPolicy.signature(expected);
        mWeb.evaluateJavascript(BrowserPageScript.observe(), value -> {
            if (mDestroyed || !mForeground || !mBrowserEnabled || mWeb == null || mLoading || epoch != mDocumentEpoch
                    || conversation != mConversationSerial || operation != mOperationId || !url.equals(mWeb.getUrl()) || !mHumanFields.isEmpty()) return;
            Bitmap snapshot = null;
            try {
                JSONObject current = new JSONObject(value);
                if (!localPreviewSafe(current) || !url.equals(current.optString("url"))
                        || !document.equals(current.optString("documentId")) || !signature.equals(BrowserObservationPolicy.signature(current))) return;
                JSONObject source = null;
                if (preferSourceImage) {
                    JSONArray images = current.optJSONArray("images");
                    source = images == null ? null : images.optJSONObject(0);
                }
                int width = mWeb.getWidth(), height = mWeb.getHeight();
                if (width < 2 || height < 2) return;
                float scale = Math.min(1f, Math.min(720f / width, 960f / height));
                snapshot = Bitmap.createBitmap(Math.max(1, Math.round(width * scale)), Math.max(1, Math.round(height * scale)), Bitmap.Config.ARGB_8888);
                Canvas canvas = new Canvas(snapshot);
                canvas.scale(scale, scale);
                mWeb.draw(canvas);
                mTimeline.attachScreenshot(bubble, snapshot, url, null);
                // Never make the visible preview depend on a remote image server. The screenshot
                // appears immediately; a verified page image replaces it only after it downloads.
                if (source != null) attachSourceImage(bubble, source, url, conversation);
            } catch (RuntimeException | OutOfMemoryError ignored) {
                // Keep the text result if the device cannot allocate a thumbnail.
            } catch (Exception ignored) { }
            finally { if (snapshot != null) snapshot.recycle(); }
        });
    }

    private boolean attachSourceImage(View bubble, JSONObject image, String pageUrl, long conversation) {
        String source = image.optString("src"), alt = clean(image.optString("alt"), 200);
        try { source = BrowserUrlPolicy.requirePublicHttps(source); }
        catch (RuntimeException invalid) { return false; }
        String cookie = null;
        try { cookie = CookieManager.getInstance().getCookie(source); }
        catch (RuntimeException ignored) { }
        final String imageUrl = source, imageCookie = cookie;
        final BrowserChatTimeline timeline = mTimeline;
        try {
            mWorker.execute(() -> {
                Bitmap bitmap = BrowserImageLoader.load(imageUrl, imageCookie);
                mUi.post(() -> {
                    try {
                        if (mDestroyed || timeline == null || timeline != mTimeline || conversation != mConversationSerial) return;
                        if (bitmap != null) timeline.attachSourceImage(bubble, bitmap, pageUrl, alt);
                    } finally { if (bitmap != null && !bitmap.isRecycled()) bitmap.recycle(); }
                });
            });
            return true;
        } catch (RuntimeException unavailable) { return false; }
    }

    private void shareCurrentPage() {
        if (mWeb == null || mLoading || !mBrowserEnabled || mDestroyed) { status("페이지가 열린 뒤 다시 눌러 주세요."); return; }
        final long epoch = mDocumentEpoch, conversation = mConversationSerial;
        final String url = mWeb.getUrl();
        mWeb.evaluateJavascript(BrowserPageScript.observe(), value -> {
            if (mDestroyed || !mForeground || !mBrowserEnabled || mWeb == null || mLoading
                    || epoch != mDocumentEpoch || conversation != mConversationSerial || !url.equals(mWeb.getUrl())) return;
            try {
                JSONObject page = new JSONObject(value);
                if (!url.equals(page.optString("url"))) return;
                if (!localPreviewSafe(page)) {
                    if (mTimeline != null) mTimeline.addAssistant("이 화면에는 비밀번호·인증·결제 보안정보 입력란이 있어 이미지를 남기지 않았어요. 브라우저에서 직접 확인해 주세요.");
                } else {
                    addPageMessage("현재 확인한 페이지\n" + clean(page.optString("title"), 200) + "\n" + displayOrigin(url), page);
                }
                setPageVisible(false);
            } catch (Exception invalid) { status("현재 화면을 확인하지 못했어요. 다시 시도해 주세요."); }
        });
    }

    private void showSettings() {
        if (mDestroyed || isFinishing() || mSettingsDialog != null) return;
        if (mRestoringSettings) { status("저장된 설정을 불러오는 중이에요."); return; }
        hideKeyboard();
        if (mRunning || mChatBusy || mRouting)
            browserActivity("설정 열림 · 현재 AI 작업은 중단하지 않고 계속 실행합니다");
        LinearLayout form = column();
        form.setPadding(dp(20), dp(8), dp(20), dp(4));
        TextView description = label("이 화면에서 바로 API 키와 모델을 입력하세요. 설정을 열거나 닫아도 진행 중인 작업은 계속됩니다.", 13, MUTED);
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
        EditText model = settingsField(form, "모델 ID", "예: " + BrowserAgent.DEFAULT_MODEL, mModel,
                InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS, 200);
        EditText maxSteps = settingsField(form, "최대 실행 횟수 (1~100회)", "20", String.valueOf(mMaxSteps),
                InputType.TYPE_CLASS_NUMBER, 3);
        EditText timeout = settingsField(form, "작업 제한 시간 (30~1800초)", "300", String.valueOf(mTimeoutSeconds),
                InputType.TYPE_CLASS_NUMBER, 4);
        TextView limitsHelp = label("횟수는 AI가 다음 동작을 결정하는 횟수입니다. 한도에 도달하면 현재 작업을 멈춥니다.", 12, MUTED);
        limitsHelp.setPadding(0, dp(6), 0, dp(8));
        form.addView(limitsHelp);
        TextView privateInfo = label(privateDataSummary(), 12, MUTED);
        privateInfo.setPadding(0, dp(10), 0, dp(6));
        form.addView(privateInfo);
        Button clearPrivate = button("저장된 개인정보·보안정보와 사용 기록 삭제", () -> {
            if (mPrivateData != null) mPrivateData.clearAll();
            privateInfo.setText("저장된 개인정보와 보안정보가 없습니다.");
            status("기기에 저장된 개인정보·보안정보와 사용 기록을 삭제했어요.");
        });
        form.addView(clearPrivate, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(42)));
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
                if (!stepText.matches("[0-9]{1,3}") || !timeoutText.matches("[0-9]{2,4}")) throw new IllegalArgumentException();
                nextSteps = Integer.parseInt(stepText);
                nextTimeout = Integer.parseInt(timeoutText);
                if (nextSteps < 1 || nextSteps > 100 || nextTimeout < 30 || nextTimeout > 1800) throw new IllegalArgumentException();
            } catch (RuntimeException invalid) {
                error.setText("최대 실행 횟수는 1~100회, 제한 시간은 30~1800초로 입력해 주세요.");
                return;
            }
            if (mSettingsHandler == null) {
                error.setText("설정 저장 연결을 사용할 수 없어요. 브라우저를 닫았다가 다시 열어 주세요.");
                return;
            }
            setSettingsSaving(dialog, true, key, model, maxSteps, timeout);
            final long requestId = ++mSettingsRequestId;
            try {
                mSettingsHandler.request(false, keyInput, nextModel, nextSteps, nextTimeout,
                        (savedKey, savedModel, savedSteps, savedTimeout, failure) -> {
                            if (mDestroyed || !mForeground || mSettingsDialog != dialog || requestId != mSettingsRequestId) return;
                            setSettingsSaving(dialog, false, key, model, maxSteps, timeout);
                            if (failure != null || !validKey(savedKey) || !validModel(savedModel)
                                    || savedSteps < 1 || savedSteps > 100 || savedTimeout < 30 || savedTimeout > 1800) {
                                error.setText(failure != null ? failure : "설정을 저장하지 못했어요. API 키와 입력값을 확인한 뒤 다시 시도해 주세요.");
                                return;
                            }
                            mRestoreSettings = false;
                            mApiKey = savedKey;
                            mModel = savedModel;
                            mMaxSteps = savedSteps;
                            mTimeoutSeconds = savedTimeout;
                            key.setText("");
                            dialog.dismiss();
                            status(mModel + " 설정을 저장했어요. 최대 " + mMaxSteps + "회, " + mTimeoutSeconds + "초 동안 실행합니다.");
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

    private String privateDataSummary() {
        if (mPrivateData == null) return "저장된 개인정보와 보안정보가 없습니다.";
        try {
            JSONArray summary = mPrivateData.summary(), usage = mPrivateData.recentUsage();
            JSONArray secrets = mPrivateData.secretSummary(), secretUsage = mPrivateData.recentSecretUsage();
            if (summary.length() == 0 && secrets.length() == 0) return "저장된 개인정보와 보안정보가 없습니다.";
            StringBuilder text = new StringBuilder("기기에 암호화해 저장한 정보: ");
            for (int i = 0; i < summary.length(); i++) {
                if (i > 0) text.append(", ");
                JSONObject item = summary.getJSONObject(i);
                text.append(kindLabel(item.getString("kind"))).append(" ").append(item.getInt("useCount")).append("회 사용");
            }
            for (int i = 0; i < secrets.length(); i++) {
                if (summary.length() > 0 || i > 0) text.append(", ");
                JSONObject item = secrets.getJSONObject(i);
                text.append(secretKindLabel(item.getString("kind"))).append(" ").append(item.getInt("useCount")).append("회 사용");
            }
            for (int i = 0; i < Math.min(5, usage.length()); i++) {
                JSONObject item = usage.getJSONObject(i);
                text.append("\n• ").append(kindLabel(item.getString("kind"))).append(" · ")
                        .append(clean(item.getString("origin"), 120)).append(" · ")
                        .append(clean(item.getString("purpose"), 160));
            }
            for (int i = 0; i < Math.min(5, secretUsage.length()); i++) {
                JSONObject item = secretUsage.getJSONObject(i);
                text.append("\n• ").append(secretKindLabel(item.getString("kind"))).append(" · ")
                        .append(clean(item.getString("origin"), 120)).append(" · ")
                        .append(clean(item.getString("purpose"), 160));
            }
            return text.toString();
        } catch (Exception error) { return "저장된 기기 보관소 기록을 읽지 못했습니다."; }
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
        WebView.setWebContentsDebuggingEnabled(false);
        WebSettings settings = mWeb.getSettings();
        settings.setJavaScriptEnabled(true);
        settings.setDomStorageEnabled(true);
        settings.setAllowFileAccess(false);
        settings.setAllowContentAccess(false);
        settings.setAllowFileAccessFromFileURLs(false);
        settings.setAllowUniversalAccessFromFileURLs(false);
        settings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
        settings.setJavaScriptCanOpenWindowsAutomatically(false);
        settings.setSupportMultipleWindows(true);
        if (Build.VERSION.SDK_INT >= 26) settings.setSafeBrowsingEnabled(true);
        settings.setMediaPlaybackRequiresUserGesture(true);
        settings.setGeolocationEnabled(true);
        settings.setBuiltInZoomControls(true);
        settings.setDisplayZoomControls(false);
        CookieManager.getInstance().setAcceptCookie(true);
        CookieManager.getInstance().setAcceptThirdPartyCookies(mWeb, false);
        mTouch = new BrowserTouchPolicy(ViewConfiguration.get(this).getScaledTouchSlop());
        mWeb.setOnTouchListener((view, event) -> onBrowserTouch(event));
        mWeb.setOnScrollChangeListener((view, x, y, oldX, oldY) -> {
            if (mRunning && mTouch != null && !mTouch.isActive() && SystemClock.elapsedRealtime() < mUserScrollUntil) {
                invalidateForUserScroll();
                scheduleObservation(mGeneration, 350);
            }
        });
        final WebView configuredWeb = mWeb;
        mWeb.setDownloadListener((url, agent, disposition, mime, length) -> {
            if (mBrowserEnabled && configuredWeb == mWeb && !mDestroyed)
                stopRun("파일 다운로드는 이 브라우저에서 지원하지 않아요.");
        });
        mWeb.setWebChromeClient(new WebChromeClient() {
            @Override public void onProgressChanged(WebView view, int progress) {
                if (!mBrowserEnabled || view != mWeb) return;
                if (mProgress != null) {
                    mProgress.setProgress(progress);
                    mProgress.setVisibility(progress == 100 ? View.INVISIBLE : View.VISIBLE);
                }
                // Some WebView/SPA combinations reach 100% without a reliable onPageFinished.
                // DOM readiness and two stable observations still gate all agent actions.
                if (progress == 100 && mLoading) {
                    final long load = mLoadSerial;
                    mUi.postDelayed(() -> {
                        if (load == mLoadSerial && mLoading && view == mWeb)
                            completePageLoad(view, view.getUrl(), false);
                    }, 250);
                }
            }
            @Override public void onPermissionRequest(PermissionRequest request) { request.deny(); }
            @Override public void onGeolocationPermissionsShowPrompt(String origin, GeolocationPermissions.Callback callback) {
                boolean allow = mLocationUseAuthorized && BrowserLocationProvider.hasPermission(VitlaneBrowserActivity.this)
                        && mBrowserEnabled && configuredWeb == mWeb && sameOrigin(origin, configuredWeb.getUrl());
                callback.invoke(origin, allow, false);
            }
            @Override public boolean onJsAlert(WebView view, String url, String message, JsResult result) {
                // Informational site alerts do not need a blocking dialog. Confirm/prompt dialogs remain interactive.
                if (mBrowserEnabled && view == mWeb) status("사이트 알림: " + clean(message, 300));
                result.confirm();
                return true;
            }
            @Override public boolean onJsConfirm(WebView view, String url, String message, JsResult result) {
                if (!mBrowserEnabled || view != mWeb || mDestroyed) { result.cancel(); return true; }
                if (finalCommitment(message)) {
                    result.cancel();
                    requestManualInput("결제가 발생하는 사이트 확인 단계입니다. 금액과 결제수단을 확인한 뒤 직접 진행해 주세요.");
                } else {
                    browserActivity("사이트 확인창 처리 · 일반 작업을 계속합니다");
                    result.confirm();
                }
                return true;
            }
            @Override public boolean onJsPrompt(WebView view, String url, String message, String defaultValue, JsPromptResult result) {
                if (!mBrowserEnabled || view != mWeb || mDestroyed) { result.cancel(); return true; }
                showSitePrompt(clean(message, 300), result);
                return true;
            }
            @Override public boolean onCreateWindow(WebView view, boolean isDialog, boolean isUserGesture, Message resultMsg) {
                if (!mBrowserEnabled || view != mWeb || mDestroyed || !(resultMsg.obj instanceof WebView.WebViewTransport)) return false;
                browserActivity("새 창 요청 확인 · 공개 HTTPS 주소를 현재 탭에서 엽니다");
                final boolean automated = mRunning && (isAgentGesture()
                        || (mPendingAction != null && "click".equals(mPendingAction.optString("type"))));
                final WebView popup = new WebView(VitlaneBrowserActivity.this);
                WebSettings popupSettings = popup.getSettings();
                popupSettings.setJavaScriptEnabled(false);
                popupSettings.setDomStorageEnabled(false);
                popupSettings.setAllowFileAccess(false);
                popupSettings.setAllowContentAccess(false);
                popupSettings.setMixedContentMode(WebSettings.MIXED_CONTENT_NEVER_ALLOW);
                popupSettings.setSupportMultipleWindows(false);
                final boolean[] captured = {false};
                popup.setWebViewClient(new WebViewClient() {
                    private boolean capture(String raw) {
                        if (captured[0] || raw == null || "about:blank".equals(raw)) return false;
                        captured[0] = true;
                        final String destination;
                        try { destination = BrowserUrlPolicy.requirePublicHttpsNavigation(raw); }
                        catch (RuntimeException invalid) {
                            String fallback = BrowserUrlPolicy.publicHttpsFallback(raw);
                            popup.stopLoading(); popup.destroy();
                            if (!fallback.isEmpty()) {
                                mUi.post(() -> { if (!mDestroyed && mBrowserEnabled) navigate(fallback, automated); });
                            } else if (automated) {
                                recoverBlockedNavigation(raw, "새 창이 외부 앱 또는 지원하지 않는 주소를 요청해 현재 페이지를 유지했습니다.");
                            } else status("새 창의 웹 주소를 열 수 없어 현재 페이지를 유지했어요.");
                            return true;
                        }
                        popup.stopLoading(); popup.destroy();
                        mUi.post(() -> { if (!mDestroyed && mBrowserEnabled) navigate(destination, automated); });
                        return true;
                    }
                    @Override public boolean shouldOverrideUrlLoading(WebView popupView, WebResourceRequest request) {
                        return request.isForMainFrame() && capture(request.getUrl().toString());
                    }
                    @SuppressWarnings("deprecation") @Override public boolean shouldOverrideUrlLoading(WebView popupView, String url) {
                        return capture(url);
                    }
                    @Override public void onPageStarted(WebView popupView, String url, Bitmap favicon) { capture(url); }
                    @Override public WebResourceResponse shouldInterceptRequest(WebView popupView, WebResourceRequest request) {
                        return blockedResponse();
                    }
                });
                WebView.WebViewTransport transport = (WebView.WebViewTransport) resultMsg.obj;
                transport.setWebView(popup);
                resultMsg.sendToTarget();
                return true;
            }
        });
        mWeb.setWebViewClient(new WebViewClient() {
            @Override public boolean shouldOverrideUrlLoading(WebView view, WebResourceRequest request) {
                if (!mBrowserEnabled || view != mWeb || mDestroyed) return true;
                String requested = request.getUrl().toString(), destination;
                try { destination = BrowserUrlPolicy.requirePublicHttpsNavigation(requested); }
                catch (RuntimeException invalid) {
                    if (request.isForMainFrame()) {
                        if (request.hasGesture() && mRunning && !isAgentGesture())
                            stopRun("직접 선택한 링크를 확인하고 있어요.");
                        String fallback = BrowserUrlPolicy.publicHttpsFallback(requested);
                        if (!fallback.isEmpty()) navigate(fallback, mRunning);
                        else if (mRunning) recoverBlockedNavigation(requested,
                                "사이트가 외부 앱 또는 지원하지 않는 주소를 요청해 현재 페이지를 유지했습니다.");
                        else status("이 주소를 열 수 없어 현재 페이지를 유지했어요.");
                    }
                    return true;
                }
                if (request.isForMainFrame() && request.hasGesture() && mRunning && !isAgentGesture()) {
                    stopRun("직접 선택한 페이지로 이동하고 있어요.");
                }
                if (!requested.equals(destination)) {
                    if (request.isForMainFrame()) {
                        trackPageLoad(view);
                        view.loadUrl(destination);
                    }
                    return true;
                }
                return false;
            }
            @Override public WebResourceResponse shouldInterceptRequest(WebView view, WebResourceRequest request) {
                if (!mBrowserEnabled || view != mWeb || mDestroyed) return blockedResponse();
                if (!BrowserUrlPolicy.isPublicNetworkUrl(request.getUrl().toString())) {
                    if (request.isForMainFrame()) mUi.post(() -> {
                        if (!mDestroyed && mBrowserEnabled && view == mWeb && request.getUrl().toString().equals(view.getUrl())) {
                            view.stopLoading();
                            mLoading = false;
                            mLoadSerial++;
                            if (mRunning) recoverBlockedNavigation(request.getUrl().toString(),
                                    "이 주소의 공개 네트워크 연결을 확인할 수 없어 이전 페이지를 유지했습니다.");
                            else status("이 주소는 브라우저에서 열 수 없어 이전 페이지를 유지했어요.");
                            restoreLastSafePage(view);
                        }
                    });
                    return blockedResponse();
                }
                return null;
            }
            @Override public void onPageStarted(WebView view, String url, android.graphics.Bitmap favicon) {
                if (!mBrowserEnabled || view != mWeb) { view.stopLoading(); return; }
                mAnswerGrant = null;
                if (!mHumanMode.isEmpty()) {
                    setPageVisible(true);
                }
                mDocumentEpoch++;
                mObservationSerial++;
                mOperationId++;
                trackPageLoad(view);
                if (!allowedUrl(url)) {
                    view.stopLoading();
                    mLoading = false;
                    mLoadSerial++;
                    String fallback = BrowserUrlPolicy.publicHttpsFallback(url);
                    if (!fallback.isEmpty()) navigate(fallback, mRunning);
                    else {
                        if (mRunning) recoverBlockedNavigation(url,
                                "지원하지 않는 주소로 이동하려 해 이전 페이지를 유지했습니다.");
                        else status("이 주소를 열 수 없어 이전 페이지를 유지했어요.");
                        restoreLastSafePage(view);
                    }
                    return;
                }
                if (mRunning) {
                    cancelActivePlan();
                    mBusy = false;
                    mExpectedNavigation = true;
                    mSettleStarted = SystemClock.elapsedRealtime();
                    status("페이지 이동을 감지했어요. 새 화면에서 작업을 이어갑니다.");
                    navigationWatchdog(mGeneration);
                }
                mAddress.setText(url);
                mOrigin.setText(displayOrigin(url));
                updateControls();
            }
            @Override public void onPageFinished(WebView view, String url) {
                completePageLoad(view, url, false);
            }
            @Override public void doUpdateVisitedHistory(WebView view, String url, boolean isReload) {
                if (!mBrowserEnabled || view != mWeb) return;
                if (!mHumanMode.isEmpty() && !url.equals(mAddress.getText().toString())) mAnswerGrant = null;
                // pushState/hash navigation may not create a document or call onPageStarted.
                if (mRunning && !mLoading && mLastObservation != null
                        && !url.equals(mLastObservation.optString("url"))) {
                    mDocumentEpoch++;
                    mOperationId++;
                    cancelActivePlan();
                    mBusy = false;
                    mExpectedNavigation = false;
                    mSettleStarted = SystemClock.elapsedRealtime();
                    scheduleObservation(mGeneration, 250);
                }
                mAddress.setText(url);
                mOrigin.setText(displayOrigin(url));
            }
            @Override public void onReceivedError(WebView view, WebResourceRequest request, WebResourceError error) {
                if (mBrowserEnabled && view == mWeb && request.isForMainFrame() && request.getUrl().toString().equals(view.getUrl())) {
                    mLoading = false;
                    stopRun("페이지를 불러오지 못했어요. 주소와 인터넷 연결을 확인해 주세요.");
                }
            }
            @Override public void onReceivedHttpError(WebView view, WebResourceRequest request, WebResourceResponse response) {
                if (!mBrowserEnabled || view != mWeb || mDestroyed || !request.isForMainFrame()
                        || !request.getUrl().toString().equals(view.getUrl()) || response == null) return;
                int statusCode = response.getStatusCode();
                if (statusCode != 401 && statusCode != 403 && statusCode != 429) return;
                mLoading = false;
                mLoadSerial++;
                mExpectedNavigation = false;
                requestManualInput(statusCode == 403
                        ? "이 사이트가 AI 브라우저의 직접 접속을 거부했습니다. 현재 브라우저에서 사이트 확인·로그인 또는 사람 확인을 마친 뒤 입력 완료·계속을 눌러 주세요."
                        : "이 사이트가 로그인 또는 잠시 후 재접속을 요구합니다. 현재 브라우저에서 필요한 단계를 마친 뒤 입력 완료·계속을 눌러 주세요.");
            }
            @Override public void onReceivedSslError(WebView view, SslErrorHandler handler, SslError error) {
                handler.cancel();
                if (mBrowserEnabled && view == mWeb && error != null && error.getUrl().equals(view.getUrl()))
                    stopRun("보안 연결을 확인할 수 없어 페이지 열기를 중지했어요.");
            }
            @Override public boolean onRenderProcessGone(WebView view, RenderProcessGoneDetail detail) {
                if (mDestroyed || view != mWeb) return true;
                String lastUrl = allowedUrl(view.getUrl()) ? view.getUrl() : "https://www.google.com/";
                boolean resume = mRunning && mHasTask && !mCompleted;
                boolean keepVisible = mPageVisible || !mHumanMode.isEmpty();
                mSuspendedUrl = lastUrl;
                mWeb = null;
                mDocumentEpoch++;
                mConversationSerial++;
                mLoading = false;
                if (mRunning || !mHumanMode.isEmpty()) stopRun("브라우저 화면을 복구하고 있어요. 준비되면 자동으로 계속합니다.");
                releaseWebView(view);
                if (mDestroyed) return true;
                mBrowserEnabled = true;
                mPageVisible = keepVisible;
                mResumeAfterRendererRecovery = resume;
                mWeb = new WebView(VitlaneBrowserActivity.this);
                mBrowserPane.addView(mWeb, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, 0, 1));
                configureBrowser();
                trackPageLoad(mWeb);
                updateBrowserVisibility();
                updateControls();
                status("브라우저 화면을 다시 준비하고 있어요.");
                mWeb.loadUrl(lastUrl);
                return true;
            }
        });
    }

    private void trackPageLoad(WebView view) {
        mLoading = true;
        final long load = ++mLoadSerial;
        mUi.postDelayed(() -> {
            if (mDestroyed || !mBrowserEnabled || view != mWeb || load != mLoadSerial || !mLoading) return;
            String current = view.getUrl();
            if (!allowedUrl(current)) {
                mLoading = false;
                mLoadSerial++;
                status("페이지를 불러오지 못했어요. 주소와 인터넷 연결을 확인해 주세요.");
                updateControls();
                return;
            }
            view.stopLoading();
            completePageLoad(view, current, true);
        }, 30000);
    }

    private void showSitePrompt(String message, JsPromptResult result) {
        if (mSitePromptDialog != null) mSitePromptDialog.dismiss();
        setPageVisible(true);
        browserActivity("사이트 입력창 · 입력값은 AI에 전달되지 않습니다");
        EditText value = input(message.isEmpty() ? "사이트에 입력할 값" : message);
        value.setSingleLine(true);
        value.setFilters(new InputFilter[]{new InputFilter.LengthFilter(512)});
        boolean secret = BrowserAgent.isSecretUserInput(message);
        if (secret) value.setInputType(InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
        AlertDialog dialog = new AlertDialog.Builder(this).setTitle("사이트 입력")
                .setMessage("이 값은 현재 웹페이지에만 전달되며 AI 대화에는 포함되지 않습니다.")
                .setView(value).setNegativeButton("취소", (ignored, which) -> resolveSitePrompt(result, null))
                .setPositiveButton("입력", (ignored, which) -> {
                    String answer = value.getText().toString();
                    if (!answer.isEmpty()) {
                        if (secret || answer.length() >= 2) {
                            if (mSecretRedactions.size() >= 20) mSecretRedactions.remove(0);
                            mSecretRedactions.add(answer);
                        }
                        resolveSitePrompt(result, answer);
                    } else resolveSitePrompt(result, null);
                    value.setText("");
                }).create();
        mSitePromptDialog = dialog;
        mSitePromptResult = result;
        dialog.setOnCancelListener(ignored -> resolveSitePrompt(result, null));
        dialog.setOnDismissListener(ignored -> {
            value.setText("");
            if (mSitePromptResult == result) resolveSitePrompt(result, null);
            if (mSitePromptDialog == dialog) mSitePromptDialog = null;
        });
        dialog.show();
    }

    private void resolveSitePrompt(JsPromptResult expected, String value) {
        if (mSitePromptResult != expected) return;
        mSitePromptResult = null;
        if (value == null) expected.cancel();
        else expected.confirm(value);
    }

    private void completePageLoad(WebView view, String url, boolean timedOut) {
        if (mDestroyed || !mBrowserEnabled || view != mWeb || url == null || !url.equals(view.getUrl())) return;
        boolean wasLoading = mLoading;
        mLoading = false;
        mLoadSerial++;
        mAddress.setText(url);
        mOrigin.setText(displayOrigin(url));
        if (timedOut) status("페이지 응답이 늦어 로딩을 멈추고 현재 표시된 내용을 확인합니다.");
        updateControls();
        if (mRunning && (mExpectedNavigation || wasLoading)) {
            mExpectedNavigation = false;
            mBusy = false;
            mSettleStarted = SystemClock.elapsedRealtime();
            scheduleObservation(mGeneration, timedOut ? 700 : 200);
        }
        if (!mQueuedGoal.isEmpty() && !mRouting && !mRunning && mHumanMode.isEmpty()) beginRun(false);
        if (mResumeAfterRendererRecovery && !mRunning && mHumanMode.isEmpty()) {
            mResumeAfterRendererRecovery = false;
            beginRun(true);
        }
    }

    private boolean onBrowserTouch(MotionEvent event) {
        if (mNativeTapInProgress) return false;
        int kind = event.getActionMasked();
        if (kind == MotionEvent.ACTION_DOWN) {
            mTouch.down(event.getX(), event.getY(), event.getEventTime());
            if (mRunning) {
                invalidateForUserScroll();
            }
        } else if (kind == MotionEvent.ACTION_MOVE || kind == MotionEvent.ACTION_POINTER_DOWN
                || kind == MotionEvent.ACTION_POINTER_UP) {
            mTouch.move(event.getX(), event.getY(), event.getPointerCount());
        } else if (kind == MotionEvent.ACTION_UP || kind == MotionEvent.ACTION_CANCEL) {
            BrowserTouchPolicy.Result result = kind == MotionEvent.ACTION_UP
                    ? mTouch.up(event.getX(), event.getY(), event.getEventTime()) : mTouch.cancel();
            if (mRunning) {
                if (result == BrowserTouchPolicy.Result.SCROLL) {
                    mUserScrollUntil = SystemClock.elapsedRealtime() + 1200;
                    status("스크롤한 화면을 다시 읽고 작업을 이어갑니다.");
                    scheduleObservation(mGeneration, 350);
                } else if (result != BrowserTouchPolicy.Result.NONE) {
                    stopRun("페이지를 직접 선택했어요. 준비되면 이어서 실행을 눌러 주세요.");
                }
            }
        }
        return false;
    }

    private void invalidateForUserScroll() {
        cancelActivePlan();
        mNavigationRequest++;
        if (!mLoading && mPendingAction != null && ("search".equals(mPendingAction.optString("type"))
                || "navigate".equals(mPendingAction.optString("type")))) {
            // A DNS check may still be queued. Cancel it without treating an undispatched navigation as failure.
            mPendingAction = null;
            mBeforeAction = null;
            mExpectedNavigation = false;
        }
        mObservationSerial++;
        mOperationId++;
        mBusy = false;
        mSettleStarted = SystemClock.elapsedRealtime();
    }

    private void clearHumanPanel() {
        mHumanSerial++;
        dismissSecretDialog();
        if (mHumanAnswer != null) mHumanAnswer.setText("");
        for (EditText field : mHumanFields) field.setText("");
        mHumanFields.clear();
        mHumanAnswer = null;
        mSubmitHumanAnswer = null;
        mHumanQuestion = null;
        mHumanMode = "";
        if (mHumanPanel != null) { mHumanPanel.removeAllViews(); mHumanPanel.setVisibility(View.GONE); }
        if (mGoalInput != null) {
            mGoalInput.setVisibility(View.VISIBLE);
            mGoalInput.setHint(mBrowserEnabled ? "웹에서 할 일을 입력하세요" : "메시지를 입력하세요");
            mGoalInput.setFilters(new InputFilter[]{new InputFilter.LengthFilter(4000)});
        }
        if (mRunControls != null) mRunControls.setVisibility(View.VISIBLE);
    }

    private void showHumanPanel(String mode, String question) { showHumanPanel(mode, question, true); }

    private void showHumanPanel(String mode, String question, boolean attachCurrentPage) {
        clearHumanPanel();
        mHumanMode = mode;
        if (mHumanPanel == null) mHumanPanel = column();
        mHumanPanel.setVisibility(View.VISIBLE);
        mHumanQuestion = label(question, 14, INK);
        mHumanQuestion.setTextIsSelectable(true);
        if (mTimeline != null) {
            // The question remains in the conversation when its one-use controls are cleared.
            if (attachCurrentPage) addPageMessage(question, mLastObservation);
            else addAssistantMessage(question);
            mTimeline.addInteraction(mHumanPanel);
            setPageVisible(false);
            mTimeline.scrollToBottom();
        } else {
            mHumanPanel.addView(mHumanQuestion);
        }
        if (mGoalInput != null) {
            mGoalInput.setText("");
            mGoalInput.setHint("answer".equals(mode) ? "답변을 입력하세요" : "위 메시지에서 다음 단계를 선택하세요");
            if ("answer".equals(mode)) {
                mGoalInput.setFocusableInTouchMode(true);
                mGoalInput.postDelayed(() -> {
                    if (mDestroyed || !"answer".equals(mHumanMode) || !mGoalInput.isEnabled()) return;
                    mGoalInput.requestFocus();
                    InputMethodManager keyboard = (InputMethodManager) getSystemService(INPUT_METHOD_SERVICE);
                    if (keyboard != null) keyboard.showSoftInput(mGoalInput, InputMethodManager.SHOW_IMPLICIT);
                }, 120);
            }
        }
        updateControls();
    }

    private void requestManualInput(String question) {
        requestManualInput(question, "", "", "");
    }

    private void requestManualInput(String question, String personalKind, String purpose, String appliedKey) {
        if (mDestroyed) return;
        stopRun("사용자 입력을 기다리고 있어요.");
        showHumanPanel("manual", clean(question, 1200) + "\n보안정보는 AI에 보내지 않습니다. 기기 보관소로 채우거나 열린 사이트에 직접 입력하세요. 입력이 끝나면 AI가 자동으로 이어갑니다.");
        BrowserTaskService.update(this, "브라우저에서 입력이 필요합니다", clean(question, 300), false);
        LinearLayout actions = row();
        addEqual(actions, button("입력 완료·계속", () -> {
            if (mLoading || mRestoringSettings) { status("페이지와 설정 로딩이 끝난 뒤 계속해 주세요."); return; }
            if (!personalKind.isEmpty() && !appliedKey.isEmpty())
                markPersonalDataApplied(personalKind, purpose, appliedKey,
                        "사용자가 사이트 입력란에 직접 입력을 완료함");
            clearHumanPanel();
            addUserMessage("입력을 완료했어요. 현재 페이지에서 계속해 주세요.");
            setPageVisible(false);
            record("inspect", "unknown", "사용자가 직접 처리한 뒤 현재 페이지 재확인을 요청함.");
            beginRun(true);
        }), 44);
        mHumanPanel.addView(actions);
        mHumanPanel.addView(button("요청 취소", () -> stopRun("입력 요청을 닫았어요. 작업 기록은 유지합니다.")));
        final long serial = mHumanSerial;
        setPageVisible(true);
        pollManualCompletion(serial, false, 0);
    }

    private void requestSecretInput(String question) {
        requestManualInput(clean(question, 900));
        if (mDestroyed || mPrivateData == null || mWeb == null || mLoading || !"manual".equals(mHumanMode)) return;
        final long serial = mHumanSerial, epoch = mDocumentEpoch;
        final String url = mWeb.getUrl();
        if (!allowedUrl(url)) return;
        mWeb.evaluateJavascript(BrowserPageScript.secretFields(), value -> {
            if (mDestroyed || mPrivateData == null || mWeb == null || serial != mHumanSerial
                    || !"manual".equals(mHumanMode) || epoch != mDocumentEpoch || !url.equals(mWeb.getUrl())) return;
            try {
                JSONObject form = new JSONObject(value);
                JSONArray fields = form.getJSONArray("fields");
                String documentId = form.getString("documentId");
                if (!url.equals(form.optString("url")) || documentId.isEmpty() || documentId.length() > 100
                        || form.optBoolean("blocked", true) || fields.length() == 0 || fields.length() > 8) return;
                for (int i = 0; i < fields.length(); i++) {
                    JSONObject field = fields.getJSONObject(i);
                    if (field.optString("id").isEmpty() || field.optString("id").length() > 100)
                        throw new IllegalArgumentException();
                    BrowserPrivateDataStore.requireSecretKind(field.optString("kind"));
                }
                mWorker.execute(() -> {
                    JSONArray saved = new JSONArray();
                    for (int i = 0; i < fields.length(); i++) {
                        try {
                            JSONObject field = fields.getJSONObject(i);
                            BrowserPrivateDataStore.SavedSecret item = mPrivateData.getSecret(field.getString("kind"), url);
                            if (item != null) saved.put(new JSONObject().put("index", i).put("id", item.id).put("value", item.value));
                        } catch (Exception ignored) { }
                    }
                    mUi.post(() -> {
                        if (mDestroyed || mWeb == null || serial != mHumanSerial || !"manual".equals(mHumanMode)
                                || epoch != mDocumentEpoch || !url.equals(mWeb.getUrl())) { clearSecretArray(saved); return; }
                        showSecretDialog(url, documentId, epoch, serial, fields, saved);
                    });
                });
            } catch (Exception ignored) { /* CAPTCHA or unsupported security fields remain manual-only. */ }
        });
    }

    private void showSecretDialog(String url, String documentId, long epoch, long serial,
            JSONArray fields, JSONArray saved) {
        dismissSecretDialog();
        LinearLayout form = column();
        form.setPadding(dp(20), dp(8), dp(20), dp(6));
        TextView explanation = label(displayOrigin(url) + "의 보안 입력란입니다. 값은 Android Keystore로 암호화해 기기에만 저장하며 AI·대화 기록·작업 기록에는 보내지 않습니다.", 13, MUTED);
        explanation.setPadding(0, 0, 0, dp(8));
        form.addView(explanation);
        List<EditText> inputs = new ArrayList<>();
        try {
            for (int i = 0; i < fields.length(); i++) {
                JSONObject field = fields.getJSONObject(i);
                String kind = BrowserPrivateDataStore.requireSecretKind(field.getString("kind"));
                String title = clean(field.optString("label"), 120);
                if (title.isEmpty()) title = secretKindLabel(kind);
                TextView titleView = label(title, 12, INK);
                titleView.setPadding(0, dp(7), 0, dp(3));
                form.addView(titleView);
                EditText input = input(secretKindLabel(kind));
                input.setSingleLine(true);
                input.setSaveEnabled(false);
                input.setFilters(new InputFilter[]{new InputFilter.LengthFilter(512)});
                int type = "password".equals(kind)
                        ? InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD | InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
                        : InputType.TYPE_CLASS_NUMBER | InputType.TYPE_NUMBER_VARIATION_PASSWORD;
                if ("card_expiry".equals(kind)) type = InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD;
                input.setInputType(type);
                if (Build.VERSION.SDK_INT >= 26) input.setImportantForAutofill(View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS);
                JSONObject stored = savedSecretAt(saved, i);
                if (stored != null) input.setText(stored.optString("value"));
                form.addView(input, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(46)));
                inputs.add(input);
            }
        } catch (Exception invalid) {
            clearSecretInputs(inputs, saved);
            return;
        }
        TextView error = label("", 12, 0xffa33131);
        error.setPadding(0, dp(6), 0, 0);
        form.addView(error);
        ScrollView scroll = new ScrollView(this);
        scroll.setFillViewport(true);
        scroll.addView(form);
        AlertDialog dialog = new AlertDialog.Builder(this).setTitle("기기 보안정보 보관소")
                .setView(scroll).setPositiveButton("저장하고 입력", null)
                .setNeutralButton("이번만 입력", null).setNegativeButton("직접 입력", null).create();
        mSecretDialog = dialog;
        dialog.setOnDismissListener(ignored -> {
            clearSecretInputs(inputs, saved);
            if (mSecretDialog == dialog) mSecretDialog = null;
        });
        dialog.show();
        if (dialog.getWindow() != null) {
            dialog.getWindow().addFlags(WindowManager.LayoutParams.FLAG_SECURE);
            dialog.getWindow().setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
        }
        dialog.getButton(AlertDialog.BUTTON_POSITIVE).setOnClickListener(ignored ->
                submitSecretDialog(dialog, inputs, fields, saved, url, documentId, epoch, serial, true, error));
        dialog.getButton(AlertDialog.BUTTON_NEUTRAL).setOnClickListener(ignored ->
                submitSecretDialog(dialog, inputs, fields, saved, url, documentId, epoch, serial, false, error));
    }

    private void submitSecretDialog(AlertDialog dialog, List<EditText> inputs, JSONArray fields, JSONArray saved,
            String url, String documentId, long epoch, long serial, boolean persist, TextView error) {
        if (mDestroyed || dialog != mSecretDialog || mWeb == null || serial != mHumanSerial
                || !"manual".equals(mHumanMode) || mLoading || epoch != mDocumentEpoch || !url.equals(mWeb.getUrl())) {
            error.setText("페이지가 바뀌었습니다. 현재 화면에서 다시 시도해 주세요."); return;
        }
        JSONArray entries = new JSONArray();
        try {
            for (int i = 0; i < inputs.size(); i++) {
                String raw = inputs.get(i).getText().toString();
                if (raw.isEmpty()) continue;
                JSONObject field = fields.getJSONObject(i);
                String kind = field.getString("kind");
                String value = BrowserPrivateDataStore.requireSecretValue(kind, raw);
                JSONObject entry = new JSONObject().put("id", field.getString("id")).put("kind", kind)
                        .put("label", clean(field.optString("label"), 160)).put("value", value);
                JSONObject stored = savedSecretAt(saved, i);
                if (stored != null && value.equals(stored.optString("value"))) entry.put("vaultId", stored.optLong("id"));
                entries.put(entry);
            }
            if (entries.length() == 0) { error.setText("입력할 보안정보를 적어 주세요."); return; }
        } catch (Exception invalid) {
            error.setText("입력 형식을 확인해 주세요."); return;
        }
        for (EditText input : inputs) input.setText("");
        dialog.dismiss();
        status(persist ? "보안정보를 기기에 암호화해 저장하고 있어요." : "보안정보를 현재 사이트에 입력하고 있어요.");
        if (!persist) { fillSecretEntries(entries, url, documentId, epoch, serial); return; }
        mWorker.execute(() -> {
            boolean ok = true;
            try {
                for (int i = 0; i < entries.length(); i++) {
                    JSONObject entry = entries.getJSONObject(i);
                    BrowserPrivateDataStore.SavedSecret item = mPrivateData.saveSecret(entry.getString("kind"), url,
                            entry.optString("label"), entry.getString("value"));
                    entry.put("vaultId", item.id);
                }
            } catch (Exception failure) { ok = false; }
            final boolean stored = ok;
            mUi.post(() -> {
                if (mDestroyed || serial != mHumanSerial) { clearSecretArray(entries); return; }
                if (!stored) { clearSecretArray(entries); status("보안정보를 저장하지 못했어요. 입력 형식을 확인해 주세요."); return; }
                fillSecretEntries(entries, url, documentId, epoch, serial);
            });
        });
    }

    private void fillSecretEntries(JSONArray entries, String url, String documentId, long epoch, long serial) {
        if (mDestroyed || mWeb == null || serial != mHumanSerial || !"manual".equals(mHumanMode)
                || mLoading || epoch != mDocumentEpoch || !url.equals(mWeb.getUrl())) { clearSecretArray(entries); return; }
        JSONObject command = new JSONObject();
        try { command.put("values", entries); }
        catch (Exception impossible) { clearSecretArray(entries); return; }
        String script = BrowserPageScript.fillSecretFields(command, url, documentId);
        mWeb.evaluateJavascript(script, result -> {
            if (mDestroyed || mWeb == null || serial != mHumanSerial || epoch != mDocumentEpoch || !url.equals(mWeb.getUrl())) {
                clearSecretArray(entries); return;
            }
            try {
                JSONObject outcome = new JSONObject(result);
                if (!outcome.optBoolean("ok")) {
                    clearSecretArray(entries);
                    requestManualInput("보안 입력란이 바뀌었습니다. 현재 사이트에서 직접 확인해 주세요.");
                    return;
                }
                rememberSecretRedactions(entries);
                JSONArray used = new JSONArray();
                for (int i = 0; i < entries.length(); i++) {
                    long id = entries.getJSONObject(i).optLong("vaultId");
                    boolean duplicate = false;
                    for (int j = 0; j < used.length(); j++) if (used.optLong(j) == id) duplicate = true;
                    if (id > 0 && !duplicate) used.put(id);
                }
                if (used.length() > 0) mWorker.execute(() -> {
                    for (int i = 0; i < used.length(); i++) try {
                        mPrivateData.recordSecretUse(used.optLong(i), url, "사이트 보안 입력란 채우기");
                    } catch (Exception ignored) { }
                });
                clearSecretArray(entries);
                if (mTimeline != null) mTimeline.addAssistant("기기 보관소의 보안정보를 사이트에 입력했습니다. 로그인·인증 또는 결제 내용을 확인해 계속하세요.");
                status("AI에 공개하지 않고 사이트 입력란에만 채웠어요.");
            } catch (Exception invalid) {
                clearSecretArray(entries);
                requestManualInput("보안정보 입력 결과를 현재 사이트에서 직접 확인해 주세요.");
            }
        });
    }

    private static JSONObject savedSecretAt(JSONArray saved, int index) {
        if (saved == null) return null;
        for (int i = 0; i < saved.length(); i++) {
            JSONObject item = saved.optJSONObject(i);
            if (item != null && item.optInt("index", -1) == index) return item;
        }
        return null;
    }

    private static void clearSecretArray(JSONArray values) {
        if (values == null) return;
        for (int i = 0; i < values.length(); i++) {
            JSONObject item = values.optJSONObject(i);
            if (item != null) try { item.put("value", ""); } catch (Exception ignored) { }
        }
    }

    private static void clearSecretInputs(List<EditText> inputs, JSONArray saved) {
        for (EditText input : inputs) input.setText("");
        clearSecretArray(saved);
    }

    private void rememberSecretRedactions(JSONArray entries) {
        for (int i = 0; i < entries.length(); i++) {
            JSONObject entry = entries.optJSONObject(i);
            if (entry == null) continue;
            String value = entry.optString("value");
            if (!value.isEmpty() && !mSecretRedactions.contains(value)) mSecretRedactions.add(value);
        }
    }

    private boolean containsRememberedSecret(String value) {
        if (value == null || value.isEmpty()) return false;
        for (String secret : mSecretRedactions) if (!secret.isEmpty() && value.contains(secret)) return true;
        return false;
    }

    private String redactRememberedText(String before) {
        String after = before;
        for (String secret : mSecretRedactions) {
            if (!secret.isEmpty()) after = after.replace(secret, "[보안정보 숨김]");
        }
        return after;
    }

    private boolean redactObservationText(JSONObject object, String key) throws Exception {
        Object value = object.opt(key);
        if (!(value instanceof String)) return false;
        String before = (String)value;
        String after = redactRememberedText(before);
        if (after.equals(before)) return false;
        object.put(key, after);
        return true;
    }

    private boolean removeRememberedSecretUrl(JSONObject object, String key) {
        Object value = object.opt(key);
        if (!(value instanceof String) || !containsRememberedSecret((String)value)) return false;
        object.remove(key);
        return true;
    }

    /**
     * Last native boundary before an observation can enter task memory or a model request.
     * Only redact model-visible prose. Element IDs, document identity and revision fields are
     * protocol data: changing them makes an otherwise valid page impossible to validate or act on.
     */
    private boolean redactRememberedSecrets(JSONObject observation) throws Exception {
        boolean changed = false;
        for (String key : new String[] {"title", "text", "html"})
            changed |= redactObservationText(observation, key);

        JSONArray elements = observation.optJSONArray("elements");
        if (elements != null) for (int i = 0; i < elements.length(); i++) {
            JSONObject element = elements.optJSONObject(i);
            if (element == null) continue;
            for (String key : new String[] {"label", "group", "context"})
                changed |= redactObservationText(element, key);
            changed |= removeRememberedSecretUrl(element, "href");
            JSONArray options = element.optJSONArray("options");
            if (options != null) for (int j = 0; j < options.length(); j++) {
                JSONObject option = options.optJSONObject(j);
                if (option == null) continue;
                changed |= redactObservationText(option, "value");
                changed |= redactObservationText(option, "label");
            }
        }

        JSONArray headings = observation.optJSONArray("headings");
        if (headings != null) for (int i = 0; i < headings.length(); i++) {
            JSONObject heading = headings.optJSONObject(i);
            if (heading != null) changed |= redactObservationText(heading, "text");
        }

        JSONArray forms = observation.optJSONArray("forms");
        if (forms != null) for (int i = 0; i < forms.length(); i++) {
            JSONObject form = forms.optJSONObject(i);
            if (form == null) continue;
            changed |= redactObservationText(form, "label");
            changed |= removeRememberedSecretUrl(form, "action");
        }

        JSONArray personalFields = observation.optJSONArray("personalFields");
        if (personalFields != null) for (int i = 0; i < personalFields.length(); i++) {
            JSONObject field = personalFields.optJSONObject(i);
            if (field != null) changed |= redactObservationText(field, "label");
        }

        JSONObject detail = observation.optJSONObject("detail");
        if (detail != null) {
            changed |= redactObservationText(detail, "text");
            changed |= redactObservationText(detail, "html");
        }
        if (changed) {
            observation.put("screenshotSafe", false);
            observation.put("localPreviewSafe", false);
        }
        return changed;
    }

    private void dismissSecretDialog() {
        if (mSecretDialog != null) {
            AlertDialog dialog = mSecretDialog;
            mSecretDialog = null;
            dialog.dismiss();
        }
    }

    private void pollManualCompletion(long serial, boolean sawSecret, int stable) {
        if (mDestroyed || serial != mHumanSerial || !"manual".equals(mHumanMode) || mWeb == null) return;
        if (mLoading) { mUi.postDelayed(() -> pollManualCompletion(serial, sawSecret, stable), 500); return; }
        mWeb.evaluateJavascript(BrowserPageScript.manualState(), value -> {
            if (mDestroyed || serial != mHumanSerial || !"manual".equals(mHumanMode)) return;
            boolean nextSaw = sawSecret;
            int nextStable = 0;
            try {
                JSONObject state = new JSONObject(value);
                boolean secret = state.optBoolean("secretRequired");
                nextSaw = sawSecret || secret;
                nextStable = nextSaw && !secret && state.optBoolean("ready") ? stable + 1 : 0;
            } catch (Exception ignored) { }
            if (nextStable >= 2) {
                clearHumanPanel();
                addUserMessage("로그인을 완료했어요. 현재 페이지에서 계속해 주세요.");
                setPageVisible(false);
                record("inspect", "applied", "사용자가 보안 입력을 완료해 자동으로 작업을 재개함.");
                beginRun(true);
                return;
            }
            final boolean keepSaw = nextSaw;
            final int keepStable = nextStable;
            mUi.postDelayed(() -> pollManualCompletion(serial, keepSaw, keepStable), 500);
        });
    }

    private void requestUserAnswer(JSONObject action, JSONObject observation) throws Exception {
        String question = action.getString("question");
        if (BrowserAgent.isSecretUserInput(question)) { requestSecretInput(question); return; }
        String fieldKind = action.optString("fieldKind");
        if (!fieldKind.isEmpty()) {
            requestPersonalData(fieldKind, action.optString("purpose", question), question);
            return;
        }
        if (BrowserAgent.isPrivateUserInput(question)) { requestManualInput(question); return; }
        stopRun("AI가 작업에 필요한 답변을 기다리고 있어요.");
        showHumanPanel("answer", question + "\n답변은 이 작업을 이어가기 위해 AI에 전달됩니다.");
        BrowserTaskService.update(this, "Vitlane AI가 답변을 기다립니다", question, true);
        final long serial = mHumanSerial, epoch = mDocumentEpoch;
        final String url = mWeb.getUrl(), documentId = observation.optString("documentId");
        final JSONObject target = action.has("targetId") ? findTarget(observation, action.getString("targetId")) : null;
        if (target != null) mHumanPanel.addView(label("입력 대상: " + clean(target.optString("label"), 200)
                + " · " + displayOrigin(url), 12, MUTED));
        EditText answer = mTimeline == null ? input("선호 조건·날짜·수량 등 필요한 답변") : mGoalInput;
        answer.setSaveEnabled(false);
        answer.setFilters(new InputFilter[]{new InputFilter.LengthFilter(2000)});
        answer.setMaxLines(3);
        mHumanAnswer = answer;
        if (mTimeline == null) mHumanPanel.addView(answer);
        LinearLayout actions = row();
        mSubmitHumanAnswer = () -> {
            if (serial != mHumanSerial || !"answer".equals(mHumanMode)) return;
            if (mLoading || mRestoringSettings) { status("페이지와 설정 로딩이 끝난 뒤 답변을 보내 주세요."); return; }
            String value = answer.getText().toString().trim();
            if (value.isEmpty()) { status("필요한 답변을 입력해 주세요."); return; }
            if (BrowserAgent.isPrivateUserInput(value)) {
                answer.setText("");
                requestManualInput("개인정보는 AI 답변란 대신 사이트에서 입력하거나 배송 정보 입력 도우미를 사용해 주세요."); return;
            }
            try {
                mUserReplies.put(new JSONObject().put("question", question).put("answer", value));
                if (mUserReplies.length() > 8) mUserReplies.remove(0);
                if (target != null && epoch == mDocumentEpoch && url.equals(mWeb.getUrl())) {
                    mAnswerGrant = new JSONObject().put("url", url).put("documentId", documentId)
                            .put("targetId", target.getString("id")).put("targetKey", answerTargetKey(target))
                            .put("revision", observation.optString("revision"))
                            .put("forms", observation.optJSONArray("forms") == null ? "[]" : observation.optJSONArray("forms").toString())
                            .put("text", value);
                }
                addUserMessage(value);
                clearHumanPanel();
                record("ask_user", "applied", "사용자 답변을 받아 현재 페이지에서 다시 판단합니다.");
                beginRun(true);
            } catch (Exception error) { status("답변을 반영하지 못했어요. 다시 시도해 주세요."); }
        };
        addEqual(actions, button("답변하고 계속", mSubmitHumanAnswer), 44);
        addEqual(actions, button("사이트에서 직접 입력", () -> requestManualInput("사이트에서 직접 처리한 뒤 입력 완료·계속을 눌러 주세요.")), 44);
        mHumanPanel.addView(actions);
        mHumanPanel.addView(button("요청 취소", () -> stopRun("입력 요청을 닫았어요.")));
        updateControls();
    }

    private void requestPersonalData(String kind, String purpose, String question) {
        if (mPrivateData == null) { requestManualInput(question); return; }
        final long attemptEpoch = mDocumentEpoch;
        final String attemptUrl = mWeb == null ? "" : mWeb.getUrl();
        final String attemptKey = personalDataAttemptKey(kind, attemptEpoch, attemptUrl);
        final String appliedKey = personalDataAppliedKey(kind, attemptUrl);
        if (mPersonalDataApplied.contains(appliedKey)) {
            try {
                JSONObject action = new JSONObject().put("type", "ask_user").put("fieldKind", kind)
                        .put("purpose", clean(purpose, 300));
                rejectAction(mGeneration, action,
                        kindLabel(kind) + " 정보는 이 페이지에 이미 한 번 입력했습니다. 값을 다시 쓰지 않고 다음 단계로 진행해야 합니다.",
                        "PERSONAL_FIELD_APPLIED");
            } catch (Exception ignored) {
                mBusy = false;
                recoverObservation(mGeneration, "이미 입력한 개인정보를 유지하고 다음 단계를 다시 확인합니다.");
            }
            return;
        }
        if (mPersonalDataFailures.contains(attemptKey)) {
            requestManualInput("저장된 " + kindLabel(kind) + " 정보는 이 화면에 자동으로 적용할 수 없었습니다. "
                            + "같은 자동 입력을 반복하지 않습니다. 사이트에서 직접 입력한 뒤 계속해 주세요.",
                    kind, purpose, appliedKey);
            return;
        }
        stopRun("저장된 개인정보를 확인하고 있어요.");
        final long serial = ++mHumanSerial;
        mWorker.execute(() -> {
            BrowserPrivateDataStore.SavedValue saved = null;
            try { saved = mPrivateData.get(kind); } catch (Exception ignored) { }
            BrowserPrivateDataStore.SavedValue result = saved;
            mUi.post(() -> {
                if (mDestroyed || serial != mHumanSerial) return;
                if (mWeb == null || attemptEpoch != mDocumentEpoch || !attemptUrl.equals(mWeb.getUrl())) {
                    if (result != null) personalDataFillFailed(result, purpose, "저장 정보를 확인하는 동안 페이지가 바뀜", attemptKey);
                    else requestManualInput("개인정보를 확인하는 동안 페이지가 바뀌었습니다. 현재 사이트의 입력란을 직접 확인해 주세요.");
                    return;
                }
                if (result != null) {
                    if (mTimeline != null) mTimeline.addAssistant(kindLabel(kind) + " 저장 정보를 이 사이트에 사용합니다.");
                    fillPersonalData(result, purpose);
                } else askPersonalData(kind, purpose, question);
            });
        });
    }

    private void askPersonalData(String kind, String purpose, String question) {
        showHumanPanel("answer", clean(question, 600) + "\n이 값은 기기 안에 암호화해 저장하고, 사용한 사이트와 목적도 함께 기록합니다. AI에는 보내지 않습니다.");
        BrowserTaskService.update(this, kindLabel(kind) + " 정보가 필요합니다", clean(question, 300), true);
        final long serial = mHumanSerial;
        EditText answer = mTimeline == null ? input(kindLabel(kind) + " 입력") : mGoalInput;
        answer.setSaveEnabled(false);
        answer.setFilters(new InputFilter[]{new InputFilter.LengthFilter("address".equals(kind) ? 500 : 254)});
        mHumanAnswer = answer;
        if (mTimeline == null) mHumanPanel.addView(answer);
        mSubmitHumanAnswer = () -> {
            if (serial != mHumanSerial || !"answer".equals(mHumanMode)) return;
            String value = answer.getText().toString().trim();
            if (value.isEmpty()) { status(kindLabel(kind) + " 정보를 입력해 주세요."); return; }
            if (BrowserAgent.isSecretUserInput(value)) { answer.setText(""); status("비밀번호·인증번호·카드 정보는 채팅에 입력할 수 없습니다."); return; }
            answer.setText("");
            status("기기에 안전하게 저장하고 있어요.");
            mWorker.execute(() -> {
                try {
                    BrowserPrivateDataStore.SavedValue saved = mPrivateData.save(kind, value);
                    mUi.post(() -> {
                        if (mDestroyed || serial != mHumanSerial) return;
                        if (mTimeline != null) mTimeline.addUser(kindLabel(kind) + " 정보를 제공했어요.");
                        addChatHistory("user", kindLabel(kind) + " 정보를 로컬 입력용으로 제공했습니다.");
                        fillPersonalData(saved, purpose);
                    });
                } catch (Exception error) {
                    mUi.post(() -> { if (!mDestroyed && serial == mHumanSerial) status("정보를 저장하지 못했어요. 입력값을 확인해 주세요."); });
                }
            });
        };
        LinearLayout actions = row();
        addEqual(actions, button("저장하고 계속", mSubmitHumanAnswer), 44);
        addEqual(actions, button("취소", () -> stopRun("정보 요청을 닫았어요.")), 44);
        mHumanPanel.addView(actions);
        updateControls();
    }

    private void fillPersonalData(BrowserPrivateDataStore.SavedValue saved, String purpose) {
        final long attemptEpoch = mDocumentEpoch;
        final String attemptUrl = mWeb == null ? "" : mWeb.getUrl();
        final String attemptKey = personalDataAttemptKey(saved == null ? "" : saved.kind, attemptEpoch, attemptUrl);
        final String appliedKey = personalDataAppliedKey(saved == null ? "" : saved.kind, attemptUrl);
        if (mWeb == null || mLoading || !allowedUrl(mWeb.getUrl())) {
            personalDataFillFailed(saved, purpose, "현재 페이지가 입력 가능한 상태가 아님", attemptKey);
            return;
        }
        final long serial = mHumanSerial;
        final String url = mWeb.getUrl();
        final long epoch = mDocumentEpoch;
        mWeb.evaluateJavascript(BrowserPageScript.humanFields(), value -> {
            if (mDestroyed || serial != mHumanSerial) return;
            if (mWeb == null || epoch != mDocumentEpoch || !url.equals(mWeb.getUrl())) {
                personalDataFillFailed(saved, purpose, "입력 전에 페이지가 바뀜", attemptKey);
                return;
            }
            try {
                JSONObject form = new JSONObject(value);
                JSONArray fields = form.getJSONArray("fields"), values = new JSONArray();
                String documentId = form.getString("documentId");
                if (!url.equals(form.optString("url")) || documentId.isEmpty() || documentId.length() > 100
                        || form.optBoolean("blocked", true) || fields.length() > 8) {
                    personalDataFillFailed(saved, purpose, "입력란 정보를 안전하게 확인하지 못함", attemptKey);
                    return;
                }
                for (int i = 0; i < fields.length(); i++) {
                    JSONObject field = fields.getJSONObject(i);
                    if (saved.kind.equals(field.optString("kind")))
                        values.put(new JSONObject().put("id", field.getString("id")).put("value", saved.value));
                }
                if (values.length() == 0) {
                    personalDataFillFailed(saved, purpose, "호환되는 입력란을 찾지 못함", attemptKey);
                    return;
                }
                JSONObject command = new JSONObject().put("values", values);
                mWeb.evaluateJavascript(BrowserPageScript.fillHumanFields(command, url, documentId), result -> {
                    if (mDestroyed || serial != mHumanSerial) return;
                    if (mWeb == null || epoch != mDocumentEpoch || !url.equals(mWeb.getUrl())) {
                        personalDataFillFailed(saved, purpose, "입력 중 페이지가 바뀜", attemptKey);
                        return;
                    }
                    try {
                        JSONObject outcome = new JSONObject(result);
                        if (!outcome.optBoolean("ok")) {
                            personalDataFillFailed(saved, purpose, personalDataFailure(outcome.optString("code")), attemptKey);
                            return;
                        }
                        mWorker.execute(() -> {
                            try { mPrivateData.recordUse(saved.id, displayOrigin(url), clean(purpose, 300)); }
                            catch (Exception ignored) { }
                        });
                        markPersonalDataApplied(saved.kind, purpose, appliedKey,
                                "기기 보관소의 정보를 현재 사이트 입력란에 한 번 입력하고 반영을 확인함");
                        if (mTimeline != null) mTimeline.addAssistant(kindLabel(saved.kind) + " 정보를 사이트에 입력하고 작업을 이어갑니다.");
                        clearHumanPanel();
                        beginRun(true);
                    } catch (Exception error) {
                        personalDataFillFailed(saved, purpose, "입력 결과를 확인하지 못함", attemptKey);
                    }
                });
            } catch (Exception error) {
                personalDataFillFailed(saved, purpose, "입력란 정보를 안전하게 읽지 못함", attemptKey);
            }
        });
    }

    private void personalDataFillFailed(BrowserPrivateDataStore.SavedValue saved, String purpose, String reason, String attemptKey) {
        if (mDestroyed || saved == null) return;
        mPersonalDataFailures.add(attemptKey);
        String label = kindLabel(saved.kind);
        String message = "저장된 " + label + " 정보를 현재 사이트에 자동 입력하지 못함: "
                + clean(reason, 140) + ". 같은 페이지에서 같은 개인정보 요청을 자동 반복하지 않고 사용자 입력으로 인계함.";
        try {
            JSONObject attempt = new JSONObject().put("type", "ask_user").put("fieldKind", saved.kind)
                    .put("purpose", clean(purpose, 300));
            record("ask_user", "rejected", message);
            mMemory.record(attempt, "rejected", message);
            mMemory.feedback("PERSONAL_FIELD");
        } catch (Exception ignored) { }
        requestManualInput("저장된 " + label + " 정보를 이 사이트의 입력란에 자동으로 적용하지 못했습니다. "
                        + "저장 내용은 변경하지 않았고 사용 기록도 남기지 않았습니다. 열린 사이트에서 직접 입력한 뒤 계속해 주세요.",
                saved.kind, purpose, personalDataAppliedKey(saved.kind, mWeb == null ? "" : mWeb.getUrl()));
    }

    private void markPersonalDataApplied(String kind, String purpose, String appliedKey, String outcome) {
        if (kind == null || kind.isEmpty() || appliedKey == null || appliedKey.isEmpty()) return;
        mPersonalDataApplied.add(appliedKey);
        mPersonalDataFailures.remove(personalDataAttemptKey(kind, mDocumentEpoch, mWeb == null ? "" : mWeb.getUrl()));
        String message = kindLabel(kind) + " 정보를 현재 페이지에 한 번 입력함: " + clean(outcome, 180)
                + ". 같은 페이지에서는 다시 입력하지 않고 다음 동작을 판단할 것.";
        try {
            JSONObject action = new JSONObject().put("type", "ask_user").put("fieldKind", kind)
                    .put("purpose", clean(purpose, 300));
            record("ask_user", "applied", message);
            mMemory.record(action, "applied", message);
        } catch (Exception ignored) { }
        mNoProgress = 0;
        mRecoveries = 0;
        mLoopRepairs = 0;
    }

    private static String personalDataAttemptKey(String kind, long epoch, String url) {
        return epoch + "\n" + (url == null ? "" : url) + "\n" + (kind == null ? "" : kind);
    }

    private static String personalDataAppliedKey(String kind, String url) {
        return (url == null ? "" : url) + "\n" + (kind == null ? "" : kind);
    }

    private void addAppliedPersonalData(JSONObject observation, String url) {
        if (observation == null || url == null || url.isEmpty()) return;
        JSONArray applied = new JSONArray();
        for (String kind : new String[] {"name", "recipient", "address", "postcode", "phone", "email"})
            if (mPersonalDataApplied.contains(personalDataAppliedKey(kind, url))) applied.put(kind);
        if (applied.length() > 0) try { observation.put("personalDataApplied", applied); }
        catch (Exception ignored) { }
    }

    private static String personalDataFailure(String code) {
        switch (code == null ? "" : code) {
            case "STALE_DOCUMENT": return "입력 전에 문서가 바뀜";
            case "STALE_FIELD": return "입력란이 동적으로 바뀜";
            case "INVALID_FIELDS": return "저장 정보가 사이트 입력 형식과 맞지 않음";
            case "NO_EFFECT": return "사이트가 입력값 반영을 거절함";
            case "UNSUPPORTED": return "사이트 입력 방식이 지원되지 않음";
            default: return "사이트에서 입력을 완료하지 못함";
        }
    }

    private static String kindLabel(String kind) {
        switch (kind) {
            case "name": return "이름";
            case "recipient": return "수령인";
            case "address": return "주소";
            case "postcode": return "우편번호";
            case "phone": return "전화번호";
            case "email": return "이메일";
            default: return "개인";
        }
    }

    private static String secretKindLabel(String kind) {
        switch (kind) {
            case "password": return "비밀번호";
            case "otp": return "OTP·인증번호";
            case "card_number": return "카드번호";
            case "card_expiry": return "카드 유효기간";
            case "card_exp_month": return "카드 유효기간 월";
            case "card_exp_year": return "카드 유효기간 연도";
            case "card_cvc": return "카드 보안코드";
            default: return "보안정보";
        }
    }

    private boolean consumeAnswerGrant(JSONObject action, JSONObject observation, JSONObject target) {
        JSONObject grant = mAnswerGrant;
        if (grant == null || !grant.optString("url").equals(observation.optString("url"))
                || !grant.optString("documentId").equals(observation.optString("documentId"))
                || !grant.optString("targetId").equals(target.optString("id"))
                || !grant.optString("targetKey").equals(answerTargetKey(target))
                || !grant.optString("revision").equals(observation.optString("revision"))
                || !grant.optString("forms").equals(observation.optJSONArray("forms") == null ? "[]" : observation.optJSONArray("forms").toString())
                || !grant.optString("text").equals(action.optString("text"))) return false;
        mAnswerGrant = null;
        return true;
    }

    private static String answerTargetKey(JSONObject target) {
        JSONArray key = new JSONArray();
        for (String field : new String[]{"id", "tag", "role", "inputType", "label", "group", "context",
                "search", "editable", "disabled", "readOnly"}) key.put(target.optString(field));
        return key.toString();
    }

    private void loadHumanFields() {
        if (mWeb == null || mLoading || mDestroyed) { status("페이지 로딩이 끝난 뒤 다시 눌러 주세요."); return; }
        final String url = mWeb.getUrl();
        final long serial = mHumanSerial, epoch = mDocumentEpoch;
        mWeb.evaluateJavascript(BrowserPageScript.humanFields(), value -> {
            if (mDestroyed || mWeb == null || serial != mHumanSerial || epoch != mDocumentEpoch || !url.equals(mWeb.getUrl())) return;
            try {
                JSONObject form = new JSONObject(value);
                JSONArray fields = form.getJSONArray("fields");
                String documentId = form.getString("documentId");
                if (!url.equals(form.optString("url")) || documentId.isEmpty() || documentId.length() > 100
                        || form.optBoolean("blocked", true) || fields.length() == 0 || fields.length() > 8) {
                    status("현재 화면에 도우미로 입력할 배송·연락처 입력란이 없습니다. 사이트에서 직접 입력해 주세요."); return;
                }
                showHumanFields(url, documentId, epoch, fields);
            } catch (Exception error) { status("입력란을 확인하지 못했어요. 사이트에서 직접 입력해 주세요."); }
        });
    }

    private void showHumanFields(String url, String documentId, long epoch, JSONArray fields) throws Exception {
        showHumanPanel("form", displayOrigin(url) + "에 입력할 배송·연락처 정보\n이 내용은 AI에 보내거나 저장하지 않습니다. 로그인·인증·카드 정보는 사이트에서 직접 입력하세요.");
        long serial = mHumanSerial;
        LinearLayout inputs = column();
        JSONArray descriptors = new JSONArray();
        for (int i = 0; i < fields.length(); i++) {
            JSONObject field = fields.getJSONObject(i);
            String id = field.getString("id"), kind = field.getString("kind");
            if (id.isEmpty() || id.length() > 100 || !kind.matches("name|recipient|address|postcode|phone|email")) throw new IllegalArgumentException();
            EditText input = input(clean(field.optString("label"), 120));
            input.setSaveEnabled(false);
            input.setInputType("phone".equals(kind) ? InputType.TYPE_CLASS_PHONE : InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS);
            int limit = "address".equals(kind) ? 500 : "email".equals(kind) ? 254
                    : "postcode".equals(kind) ? 32 : "phone".equals(kind) ? 40 : 160;
            input.setFilters(new InputFilter[]{new InputFilter.LengthFilter(limit)});
            if (Build.VERSION.SDK_INT >= 26) input.setImportantForAutofill(View.IMPORTANT_FOR_AUTOFILL_NO);
            inputs.addView(label(clean(field.optString("label"), 120), 12, INK));
            inputs.addView(input);
            mHumanFields.add(input);
            descriptors.put(new JSONObject().put("id", id));
        }
        ScrollView scroll = new ScrollView(this);
        scroll.addView(inputs);
        mHumanPanel.addView(scroll, new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(160)));
        LinearLayout actions = row();
        addEqual(actions, button("이 사이트에 입력", () -> {
            if (serial != mHumanSerial || mDestroyed || mWeb == null || mLoading
                    || epoch != mDocumentEpoch || !url.equals(mWeb.getUrl())) return;
            try {
                JSONArray values = new JSONArray();
                for (int i = 0; i < mHumanFields.size(); i++) {
                    String text = mHumanFields.get(i).getText().toString().trim();
                    if (!text.isEmpty()) values.put(new JSONObject().put("id", descriptors.getJSONObject(i).getString("id")).put("value", text));
                }
                if (values.length() == 0) { status("입력할 내용을 적어 주세요."); return; }
                JSONObject command = new JSONObject().put("values", values);
                showHumanPanel("form_fill", "현재 사이트에 입력하고 있어요.");
                long operation = mHumanSerial;
                mHumanPanel.addView(button("닫기", () -> stopRun("입력 결과는 사이트에서 확인해 주세요.")));
                mWeb.evaluateJavascript(BrowserPageScript.fillHumanFields(command, url, documentId), result -> {
                    if (mDestroyed || mWeb == null || operation != mHumanSerial || epoch != mDocumentEpoch || !url.equals(mWeb.getUrl())) return;
                    try {
                        JSONObject outcome = new JSONObject(result);
                        requestManualInput(outcome.optBoolean("ok")
                                ? "사이트에 정보를 입력했습니다. 내용을 확인하고 사이트의 다음 단계·최종 주문은 직접 진행해 주세요."
                                : "입력란이 바뀌었거나 일부 입력을 확인하지 못했습니다. 사이트의 현재 내용을 직접 확인해 주세요.");
                    } catch (Exception invalid) { requestManualInput("사이트 입력 결과를 직접 확인해 주세요."); }
                });
            } catch (Exception invalid) { requestManualInput("입력란을 다시 확인해 주세요."); }
        }), 44);
        addEqual(actions, button("돌아가기", () -> requestManualInput("사이트에서 직접 입력하거나 배송 정보 입력 도우미를 사용할 수 있습니다.")), 44);
        mHumanPanel.addView(actions);
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
        if (!mBrowserEnabled || mWeb == null || mDestroyed) return;
        if (!automated) stopRun("페이지를 열고 있어요.");
        final String url;
        try { url = BrowserUrlPolicy.requirePublicHttpsNavigation(raw); }
        catch (RuntimeException error) {
            if (automated) recoverBlockedNavigation(raw,
                    "이동 주소가 공개 웹 정책을 통과하지 못해 현재 페이지를 유지했습니다.");
            else stopRun("이 주소를 열 수 없어요. 주소가 공개 웹사이트이고 HTTPS를 지원하는지 확인해 주세요.");
            return;
        }
        final long request = ++mNavigationRequest;
        mOperationId++;
        final int generation = mGeneration;
        if (automated) { mExpectedNavigation = true; mBusy = true; }
        mWorker.execute(() -> {
            if (mDestroyed || !mBrowserEnabled || request != mNavigationRequest) return;
            boolean allowed = BrowserUrlPolicy.isPublicNetworkUrl(url);
            mUi.post(() -> {
                if (mDestroyed || !mBrowserEnabled || mWeb == null || request != mNavigationRequest || (automated && !valid(generation))) return;
                if (!allowed) {
                    if (automated) recoverBlockedNavigation(url,
                            "이 주소의 공개 네트워크 연결을 확인할 수 없어 현재 페이지를 유지했습니다.");
                    else stopRun("이 주소는 브라우저에서 열 수 없어요.");
                    return;
                }
                trackPageLoad(mWeb);
                mWeb.loadUrl(url);
                updateControls();
                if (automated) navigationWatchdog(generation);
            });
        });
    }

    private void startRun() { beginRun(false); }

    private void beginRun(boolean resume) {
        if (mRunning || mChatBusy || mDestroyed) return;
        if (!mBrowserEnabled) { status("웹 작업은 브라우저를 켠 뒤 시작할 수 있어요."); return; }
        if (!mHumanMode.isEmpty()) { status("아래 사용자 입력을 마치거나 취소한 뒤 계속해 주세요."); return; }
        if (mRestoringSettings) { status("저장된 설정을 불러오는 중이에요."); return; }
        if (mApiKey.isEmpty()) { status("설정에서 API 키와 모델을 입력해 주세요."); showSettings(); return; }
        if (!mForeground || mLoading || mWeb == null || !allowedUrl(mWeb.getUrl())) {
            status("웹페이지 로딩이 끝난 뒤 AI 실행을 눌러 주세요."); return;
        }
        boolean routed = !resume && !mQueuedGoal.isEmpty();
        String goal = resume ? mGoal : routed ? mQueuedGoal : mGoalInput.getText().toString().trim();
        if (goal.isEmpty() || goal.length() > 4000) { status("할 일을 4,000자 이내로 입력해 주세요."); return; }
        if (resume && (!mHasTask || mCompleted || !goal.equals(mGoal))) {
            status("같은 목표의 중지된 작업만 이어갈 수 있어요. 새 목표는 AI 실행을 눌러 주세요."); return;
        }
        if (!resume) {
            mUserReplies = new JSONArray();
            mAnswerGrant = null;
            mMemory.clear();
            mPersonalDataFailures.clear();
            mPersonalDataApplied.clear();
            mContract = new BrowserTaskContract(goal);
            if (!routed) mResearch = BrowserResearchPlanner.empty("");
            mHistory = new JSONArray();
            mLastResult = "";
            mCompleted = false;
            mHasTask = true;
            try { mBrowserConversation = BrowserAgent.sanitizeConversation(mChatMessages); }
            catch (Exception invalid) { mBrowserConversation = new JSONArray(); }
            if (!routed) addUserMessage(goal);
            mQueuedGoal = "";
            mGoalInput.setText("");
        }
        mMemory.resetProgressWatchdog();
        mGoal = goal;
        hideKeyboard();
        mGeneration++;
        mRunning = true;
        mBusy = false;
        mExpectedNavigation = false;
        mPendingAction = null;
        mBeforeAction = null;
        mLastObservation = null;
        mRecoveries = mNoProgress = mRepeatedAction = mLoopRepairs = mPlannerRepairs = 0;
        mLastActionKey = "";
        mSteps = 0;
        mSettleStarted = SystemClock.elapsedRealtime();
        mStableObservations = 0;
        mStableSignature = "";
        long timeoutMs = mTimeoutSeconds * 1000L;
        mDeadline = SystemClock.elapsedRealtime() + timeoutMs;
        updateBrowserVisibility();
        updateControls();
        status(resume ? "수집한 정보와 계획을 유지하고 현재 페이지에서 이어갑니다."
                : "페이지 구조를 읽고 작업을 시작합니다. 스크롤은 가능하며 탭하면 직접 조작으로 전환합니다.");
        browserActivity("관찰 중 · DOM, 화면 상태와 조작 가능한 요소를 확인합니다");
        BrowserTaskService.update(this, "Vitlane AI가 웹 작업 중입니다", "앱을 벗어나도 현재 작업을 계속합니다.", false);
        scheduleObservation(mGeneration, 100);
        final int generation = mGeneration;
        mUi.postDelayed(() -> {
            if (mRunning && generation == mGeneration) stopRun("작업 시간이 초과됐어요. 작업 기록을 확인하거나 이어서 실행해 주세요.");
        }, timeoutMs);
    }

    private boolean valid(int generation) {
        if (!mRunning || !mBrowserEnabled || mDestroyed || generation != mGeneration) return false;
        if (SystemClock.elapsedRealtime() >= mDeadline || mSteps > mMaxSteps) {
            stopRun("이번 작업의 실행 한도에 도달했어요. 기록을 확인하거나 이어서 실행해 주세요."); return false;
        }
        return true;
    }

    private void scheduleObservation(int generation, long delay) {
        final long serial = ++mObservationSerial;
        mUi.postDelayed(() -> { if (serial == mObservationSerial) observe(generation); }, delay);
    }

    private void observe(int generation) {
        if (!valid(generation) || mBusy || mLoading || mWeb == null || !mHumanMode.isEmpty()
                || (mTouch != null && mTouch.isActive())) return;
        mUserScrollUntil = 0;
        mOperationId++;
        mBusy = true;
        final long serial = mObservationSerial;
        final long epoch = mDocumentEpoch;
        final String url = mWeb.getUrl();
        if (!allowedUrl(url)) { stopRun("이 페이지에서는 AI를 실행할 수 없어요."); return; }
        mWeb.evaluateJavascript(BrowserPageScript.observe(), value -> {
            if (!valid(generation) || serial != mObservationSerial) return;
            if (mLoading || epoch != mDocumentEpoch || !url.equals(mWeb.getUrl())) {
                recoverObservation(generation, "페이지가 바뀌어 새 화면을 확인합니다."); return;
            }
            try {
                JSONObject observation = new JSONObject(value);
                if (!url.equals(observation.optString("url"))) {
                    recoverObservation(generation, "주소가 바뀌어 새 페이지를 확인합니다."); return;
                }
                if (observation.optBoolean("sensitive", true)) {
                    requestSecretInput("로그인·인증·결제 보안정보가 필요한 화면입니다. 기기 보관소에서 해당 입력란만 안전하게 채울 수 있습니다."); return;
                }
                if (containsRememberedSecret(url)) {
                    requestManualInput("주소에 보안정보가 포함된 것으로 보여 AI 작업을 중지했습니다. 현재 페이지를 직접 확인해 주세요."); return;
                }
                redactRememberedSecrets(observation);
                addAppliedPersonalData(observation, url);
                if (!BrowserObservationPolicy.ready(observation, SystemClock.elapsedRealtime() - mSettleStarted)) {
                    mBusy = false;
                    status("페이지 내용과 입력 요소가 준비되기를 기다리고 있어요.");
                    scheduleObservation(generation, 300); return;
                }
                String stable = BrowserObservationPolicy.signature(observation);
                if (stable.equals(mStableSignature)) mStableObservations++;
                else { mStableSignature = stable; mStableObservations = 1; }
                if (mStableObservations < 2) {
                    mBusy = false;
                    status("페이지가 완전히 반영됐는지 한 번 더 확인하고 있어요.");
                    scheduleObservation(generation, 250); return;
                }
                mExpectedNavigation = false;
                if (mPendingAction != null && !verifyPendingAction(observation)) {
                    mBusy = false;
                    scheduleObservation(generation, 300); return;
                }
                mMemory.observe(observation);
                if (mContract != null && !mContract.toJson().optBoolean("initialized") && mResearch.optBoolean("searched")) {
                    JSONObject researchTask = mResearch.optJSONObject("task");
                    if (researchTask != null && researchTask.optJSONArray("requirements") != null
                            && researchTask.optJSONArray("requirements").length() > 0)
                        mContract.apply(researchTask, observation, mMemory.toJson());
                }
                // A complete, valid observation starts a fresh transient-recovery budget.
                mRecoveries = 0;
                mLastObservation = observation;
                if (observation.optBoolean("paymentRequired")) {
                    requestManualInput("결제가 발생하는 마지막 동작입니다. 금액과 결제수단을 확인한 뒤 사이트에서 직접 진행해 주세요.");
                    return;
                }
                if (mNoProgress >= 3 || mMemory.isStalled()) {
                    if (!prepareLoopDiagnosis(observation, null)) return;
                }
                if (mSteps >= mMaxSteps) {
                    stopRun("이번 작업의 실행 한도에 도달했어요. 기록을 확인하거나 이어서 실행해 주세요."); return;
                }
                mSteps++;
                status(mSteps + "/" + mMaxSteps + " · 수집한 정보와 현재 페이지를 바탕으로 다음 동작을 정합니다.");
                browserActivity(mSteps + "/" + mMaxSteps + " · 다음 조작을 판단하고 있습니다");
                final long planSerial = ++mPlanSerial;
                final String apiKey = mApiKey, model = mModel, goal = mGoal;
                final JSONArray history = new JSONArray(mHistory.toString());
                final JSONObject memory = mMemory.toJson();
                final JSONObject task = mContract == null ? new BrowserTaskContract(goal).toJson() : mContract.toJson();
                final JSONArray replies = new JSONArray(mUserReplies.toString());
                final JSONArray conversation = new JSONArray(mBrowserConversation.toString());
                final String screenshotDataUrl = capturePlannerScreenshot(observation);
                mTask = mWorker.submit(() -> {
                    try {
                        if (generation != mGeneration || planSerial != mPlanSerial) return;
                        JSONObject action = BrowserAgent.planWithScreenshot(apiKey, model, goal, observation, history, memory,
                                task, replies, conversation, mResearch, screenshotDataUrl, connection -> {
                            if (generation != mGeneration || planSerial != mPlanSerial) {
                                if (connection != null) connection.disconnect();
                                throw new IllegalStateException("CANCELLED");
                            }
                            mConnection = connection;
                        });
                        mUi.post(() -> {
                            if (planSerial == mPlanSerial && samePage(generation, epoch, url)) {
                                mPlannerRepairs = 0;
                                handleAction(generation, epoch, url, observation, action);
                            }
                        });
                    } catch (Exception error) {
                        final String failure = error instanceof BrowserAgent.PlannerException
                                ? clean(error.getMessage(), 350) : "AI 연결에 실패했어요. API 키·사용 한도·인터넷 연결을 확인해 주세요.";
                        final String code = error instanceof BrowserAgent.PlannerException
                                ? ((BrowserAgent.PlannerException) error).code : "NETWORK";
                        mUi.post(() -> {
                            if (generation != mGeneration || planSerial != mPlanSerial || !mRunning) return;
                            if (BrowserRecoveryPolicy.canRepairPlanner(code) && ++mPlannerRepairs <= 2) {
                                mMemory.feedback(code);
                                record("inspect", "rejected", "AI 동작 검증 실패: " + code);
                                recoverObservation(generation, "제안된 동작의 문제를 반영해 현재 화면에서 다시 판단합니다.");
                            } else stopRun(failure);
                        });
                    } finally { if (planSerial == mPlanSerial) mConnection = null; }
                });
            } catch (Exception error) {
                recoverObservation(generation, "페이지 구조를 다시 읽고 있어요.");
            }
        });
    }

    /** Captures only a page the DOM privacy pass marked safe; the bitmap is never persisted. */
    private String capturePlannerScreenshot(JSONObject observation) {
        if (mWeb == null || observation == null || observation.optBoolean("sensitive", true)
                || !observation.optBoolean("screenshotSafe", false)
                || !observation.optString("url").equals(mWeb.getUrl())) return null;
        int width = mWeb.getWidth(), height = mWeb.getHeight();
        if (width < 2 || height < 2) return null;
        Bitmap bitmap = null;
        try {
            float scale = Math.min(1f, Math.min(640f / width, 960f / height));
            bitmap = Bitmap.createBitmap(Math.max(1, Math.round(width * scale)),
                    Math.max(1, Math.round(height * scale)), Bitmap.Config.RGB_565);
            Canvas canvas = new Canvas(bitmap);
            canvas.scale(scale, scale);
            mWeb.draw(canvas);
            ByteArrayOutputStream bytes = new ByteArrayOutputStream();
            if (!bitmap.compress(Bitmap.CompressFormat.JPEG, 48, bytes) || bytes.size() > 200000) return null;
            return "data:image/jpeg;base64," + Base64.encodeToString(bytes.toByteArray(), Base64.NO_WRAP);
        } catch (RuntimeException | OutOfMemoryError ignored) {
            return null;
        } finally {
            if (bitmap != null && !bitmap.isRecycled()) bitmap.recycle();
        }
    }

    private boolean samePage(int generation, long epoch, String url) {
        if (!valid(generation)) return false;
        if (mTouch != null && mTouch.isActive()) return false;
        if (mWeb == null || mLoading || epoch != mDocumentEpoch || url == null || !url.equals(mWeb.getUrl())) {
            recoverObservation(generation, "페이지가 바뀌어 이전 판단을 버리고 다시 확인합니다."); return false;
        }
        return true;
    }

    private void cancelActivePlan() {
        mPlanSerial++;
        HttpURLConnection connection = mConnection;
        mConnection = null;
        if (connection != null) connection.disconnect();
        if (mTask != null) { mTask.cancel(true); mTask = null; }
    }

    private void recoverObservation(int generation, String message) {
        if (!valid(generation)) return;
        cancelActivePlan();
        mBusy = false;
        if (++mRecoveries > 4) { stopRun("페이지가 계속 바뀌거나 요소를 읽을 수 없어요. 잠시 후 이어서 실행해 주세요."); return; }
        status(message);
        mSettleStarted = SystemClock.elapsedRealtime();
        if (mLoading) { mExpectedNavigation = true; navigationWatchdog(generation); }
        else {
            long base = BrowserRecoveryPolicy.delayMillis(mMemory.toJson().optJSONObject("feedback") == null
                    ? "" : mMemory.toJson().optJSONObject("feedback").optString("code"));
            long delay = Math.min(3000, base * (1L << Math.min(3, Math.max(0, mRecoveries - 1))));
            scheduleObservation(generation, delay);
        }
    }

    private void pendingAction(JSONObject action, JSONObject observation) throws Exception {
        mPendingAction = new JSONObject(action.toString());
        mBeforeAction = observation;
        mActionStarted = mSettleStarted = SystemClock.elapsedRealtime();
    }

    private boolean verifyPendingAction(JSONObject observation) {
        String result = BrowserExecutionVerifier.actionOutcome(mPendingAction, mBeforeAction, observation);
        if ("unknown".equals(result) && SystemClock.elapsedRealtime() - mActionStarted < 2500) return false;
        String message = "applied".equals(result)
                ? "요청한 요소 상태 또는 문서 이동을 확인함. 목표 완료 여부는 별도 판단 필요."
                : "페이지 일부 변화와 무관하게 요청한 입력·선택·클릭의 후조건을 확인하지 못함. 같은 변경을 반복하지 말고 현재 상태와 다른 경로를 확인할 것.";
        browserActivity("applied".equals(result) ? "검증 완료 · 조작 결과가 페이지에 반영됐습니다"
                : "재계획 · 조작 결과가 확인되지 않아 다른 경로를 찾습니다");
        record(mPendingAction.optString("type"), result, message);
        mMemory.record(mPendingAction, result, message);
        if ("applied".equals(result) && "search".equals(mPendingAction.optString("type"))) mMemory.rememberSearch(mPendingAction);
        if ("applied".equals(result)) { mNoProgress = 0; mRecoveries = 0; mLoopRepairs = 0; }
        else { mNoProgress++; mMemory.feedback("NO_EFFECT"); }
        mPendingAction = null;
        mBeforeAction = null;
        return true;
    }

    private void handleAction(int generation, long epoch, String url, JSONObject observation, JSONObject action) {
        try {
            String type = action.getString("type");
            browserActivity(actionLabel(type) + " · " + clean(action.optString("message"), 180));
            if (mContract == null) mContract = new BrowserTaskContract(mGoal);
            // Planning annotations help verify the result; they are not execution prerequisites.
            boolean metadataWarning = action.optBoolean("metadataWarning");
            if (action.has("task")) {
                JSONObject task = action.optJSONObject("task");
                if (task == null || !mContract.apply(task, observation, mMemory.toJson())) metadataWarning = true;
            }
            if (metadataWarning) record(type, "unknown", "요청 조건·계획의 일부를 반영하지 못해 원래 요청으로 동작을 계속합니다.");
            if (!BrowserObservationPolicy.actionAllowedByInterrupt(action, observation)) {
                rejectAction(generation, action, "대화상자가 현재 화면을 가리고 있습니다. 대화상자 안의 동작을 처리하거나 페이지를 벗어납니다.", "OBSCURED");
                return;
            }
            if ("click".equals(type) && !BrowserAgent.clickFallbackAllowed(action, observation, mMemory.toJson())) {
                rejectAction(generation, action, "클릭 전환 경로는 같은 요소의 DOM 클릭 결과가 없을 때 한 번만 사용할 수 있습니다.", "NO_EFFECT");
                return;
            }
            if ("finish".equals(type)) {
                if (metadataWarning || !mContract.toJson().optString("lastError").isEmpty()
                        || !mContract.canFinish(action.optJSONArray("candidateIds"))) {
                    showPartialResult(action); return;
                }
                verifyFinish(generation, epoch, url, observation, action); return;
            }
            if ("ask_user".equals(type)) {
                addCatalog(action.optJSONArray("catalog"), observation);
                requestUserAnswer(action, observation); return;
            }
            if ("handoff".equals(type)) {
                mLastResult = action.optString("message");
                addCatalog(action.optJSONArray("catalog"), observation);
                if (BrowserAgent.isSecretUserInput(action.optString("message"))) requestSecretInput(action.optString("message"));
                else requestManualInput(action.optString("message"));
                return;
            }
            mMemory.record(action, "unknown", metadataWarning
                    ? "조건·계획의 일부는 보류하고 원래 요청으로 동작을 계획함. 실행 결과는 아직 확인하지 않음."
                    : "다음 동작 계획. 아직 실행하거나 성공을 확인하지 않음.");
            if ("wait".equals(type)) {
                record(type, "applied", "동적 페이지 내용 대기");
                mBusy = false;
                mSettleStarted = SystemClock.elapsedRealtime();
                scheduleObservation(generation, Math.max(250, Math.min(5000, action.optInt("milliseconds", 1000))));
                return;
            }
            if ("inspect".equals(type) && !action.has("targetId")) {
                record(type, "applied", "페이지 다시 관찰");
                mBusy = false;
                scheduleObservation(generation, 500); return;
            }
            String key = BrowserObservationPolicy.actionKey(action, observation);
            mRepeatedAction = key.equals(mLastActionKey) ? mRepeatedAction + 1 : 1;
            mLastActionKey = key;
            if (mRepeatedAction >= 3) {
                if (prepareLoopDiagnosis(observation, action)) {
                    mBusy = false;
                    mSettleStarted = SystemClock.elapsedRealtime();
                    scheduleObservation(generation, BrowserRecoveryPolicy.delayMillis(
                            BrowserRecoveryPolicy.repeatedActionCode(action, observation, mMemory.toJson())));
                }
                return;
            }
            if ("research".equals(type)) {
                String query = action.getString("query");
                if (mMemory.searched(query)) {
                    rejectAction(generation, action, "이미 시도한 검색어입니다. 제품·성별·상품군·지역을 포함해 검색어 또는 출처를 바꿉니다.", "REPEATED_SEARCH"); return;
                }
                runInlineResearch(generation, epoch, url, observation, action);
                return;
            }
            if ("search".equals(type)) {
                // requirementIds only annotate search intent. The query can run before a ledger exists.
                String query = action.getString("query");
                if (mMemory.searched(query)) {
                    rejectAction(generation, action, "이미 시도한 검색어입니다. 미확인 조건을 중심으로 검색어·출처를 바꿉니다.", "REPEATED_SEARCH"); return;
                }
                String destination = BrowserAgent.searchUrl(query);
                pendingAction(action, observation);
                navigate(destination, true); return;
            }
            if ("navigate".equals(type)) {
                pendingAction(action, observation);
                navigate(action.getString("url"), true); return;
            }
            if ("back".equals(type)) {
                if (!mWeb.canGoBack()) { rejectAction(generation, action, "뒤로 이동할 페이지가 없습니다."); return; }
                pendingAction(action, observation);
                mExpectedNavigation = true;
                mWeb.goBack();
                navigationWatchdog(generation); return;
            }
            if ("click".equals(type) || "type".equals(type) || "select".equals(type) || "check".equals(type)) {
                JSONObject target = findTarget(observation, action.getString("targetId"));
                if (target == null) { rejectAction(generation, action, "대상 요소가 바뀌었습니다."); return; }
                String label = clean(target.optString("label"), 120);
                if (finalCommitment(label)) { requestManualInput("결제가 발생하는 마지막 동작입니다. 금액과 결제수단을 확인한 뒤 사이트에서 직접 진행해 주세요."); return; }
                String description = label;
                if (!target.optString("group").isEmpty()) description += "\n대상: " + clean(target.optString("group"), 200);
                if (!target.optString("context").isEmpty()) description += "\n주변 내용: " + clean(target.optString("context"), 500);
                if ("type".equals(type)) {
                    if (target.optBoolean("search")) action.put("submit", action.optBoolean("submit", true));
                    else {
                        if (!target.optBoolean("editable")) { rejectAction(generation, action, "입력 가능한 공개 필드가 아닙니다."); return; }
                        action.put("submit", false);
                        action.put("approved", true);
                    }
                } else if ("select".equals(type) || "check".equals(type)) {
                    action.put("approved", true);
                } else {
                    String href = target.optString("href");
                    if (!href.isEmpty() && !navigationUrlAllowed(href)) {
                        rejectAction(generation, action,
                                "링크 주소가 공개 웹 정책을 통과하지 못했습니다. 현재 페이지의 다른 경로를 확인합니다.",
                                BrowserUrlPolicy.isExternalAppNavigation(href) ? "EXTERNAL_NAVIGATION" : "BLOCKED_NAVIGATION");
                        return;
                    }
                    String method = action.optString("method", "dom");
                    if (!("dom".equals(method) || "direct".equals(method) || "native".equals(method))) {
                        rejectAction(generation, action, "지원하지 않는 클릭 방식입니다.", "UNSUPPORTED"); return;
                    }
                    if (!"dom".equals(method) && (sideEffectLabel(label) || sideEffectDestination(href))) {
                        rejectAction(generation, action, "결과를 되돌리기 어려운 동작에는 클릭 전환을 사용하지 않습니다.", "UNSUPPORTED"); return;
                    }
                    action.put("approved", true);
                    if ("direct".equals(method)) {
                        if (href.isEmpty()) { rejectAction(generation, action, "직접 열 수 있는 현재 링크가 없습니다.", "STALE_TARGET"); return; }
                        pendingAction(action, observation);
                        navigate(href, true); return;
                    }
                }
            } else if (!"scroll".equals(type) && !"inspect".equals(type)) throw new IllegalStateException();
            executeAction(generation, epoch, url, observation, action);
        } catch (Exception error) {
            rejectAction(generation, action, "실행할 동작을 확인하지 못했습니다.");
        }
    }

    /** Runs hosted web search while keeping the current WebView document and session untouched. */
    private void runInlineResearch(int generation, long epoch, String url, JSONObject observation, JSONObject action) {
        final long researchSerial = ++mPlanSerial;
        final String apiKey = mApiKey, model = mModel;
        final JSONObject page;
        final JSONObject lookup;
        try {
            page = new JSONObject(observation.toString());
            lookup = new JSONObject(action.toString());
        } catch (Exception invalid) {
            rejectAction(generation, action, "현재 페이지의 검색 맥락을 준비하지 못했습니다.");
            return;
        }
        status("현재 상품 페이지를 유지한 채 필요한 정보를 웹에서 확인하고 있어요.");
        browserActivity("페이지 유지 검색 · " + clean(action.optString("purpose"), 160));
        mTask = mWorker.submit(() -> {
            try {
                JSONObject result = BrowserResearchPlanner.inlineResearch(apiKey, model,
                        lookup.getString("query"), lookup.optString("purpose", "현재 선택에 필요한 공개 정보 확인"), page,
                        connection -> {
                            if (generation != mGeneration || researchSerial != mPlanSerial || mDestroyed) {
                                if (connection != null) connection.disconnect();
                                throw new IllegalStateException("CANCELLED");
                            }
                            mConnection = connection;
                        });
                mUi.post(() -> {
                    if (researchSerial != mPlanSerial || !samePage(generation, epoch, url)) return;
                    mMemory.rememberInlineResearch(lookup, result);
                    String outcome = "현재 페이지를 유지한 웹 검색 완료. 출처가 확인된 결과를 다음 판단에 사용하며 현재 페이지 상태는 별도로 검증할 것.";
                    mMemory.record(lookup, "applied", outcome);
                    record("research", "applied", outcome);
                    mNoProgress = 0;
                    mRecoveries = 0;
                    mLoopRepairs = 0;
                    mBusy = false;
                    status(result.optBoolean("conflict") || result.optBoolean("needsUserChoice")
                            ? "검색 출처 사이 차이가 있어 현재 페이지와 함께 다시 판단합니다."
                            : "검색 결과를 현재 상품의 옵션과 대조하고 있어요.");
                    browserActivity("검색 완료 · 상품 페이지를 떠나지 않고 출처를 작업 메모리에 반영했습니다");
                    scheduleObservation(generation, 100);
                });
            } catch (Exception error) {
                final String message = error instanceof BrowserResearchPlanner.ResearchException
                        ? clean(error.getMessage(), 300) : "작업 중 웹 검색에 연결하지 못했습니다.";
                mUi.post(() -> {
                    if (researchSerial != mPlanSerial || !samePage(generation, epoch, url)) return;
                    mMemory.rememberSearch(lookup);
                    mMemory.feedback("RESEARCH_FAILED");
                    mMemory.record(lookup, "rejected", message);
                    record("research", "rejected", message);
                    mNoProgress++;
                    mBusy = false;
                    status(message + " 현재 페이지의 공식 안내나 다른 검색 경로로 이어갑니다.");
                    browserActivity("검색 전환 · 현재 페이지는 유지되었습니다");
                    scheduleObservation(generation, 150);
                });
            } finally {
                if (researchSerial == mPlanSerial) mConnection = null;
            }
        });
    }

    /** Gives the reasoning model bounded diagnostic turns before declaring an actual loop. */
    private boolean prepareLoopDiagnosis(JSONObject observation, JSONObject repeatedAction) {
        String code = repeatedAction == null
                ? BrowserRecoveryPolicy.stalledCode(observation, mMemory.toJson())
                : BrowserRecoveryPolicy.repeatedActionCode(repeatedAction, observation, mMemory.toJson());
        String strategy = BrowserRecoveryPolicy.feedback(code).optString("strategy");
        String diagnostic = "반복 원인 분석(" + code + "): " + strategy;
        record(repeatedAction == null ? "inspect" : repeatedAction.optString("type", "inspect"), "rejected", diagnostic);
        if (repeatedAction != null) mMemory.record(repeatedAction, "rejected", diagnostic);
        // Keep facts and page evidence, but start a fresh bounded progress budget for the repair.
        mMemory.resetProgressWatchdog();
        mMemory.feedback(code);
        mNoProgress = 0;
        if (++mLoopRepairs > 2) {
            stopRun("반복 원인을 두 차례 분석하고 다른 경로를 시도했지만 진전을 확인하지 못했어요. " + strategy);
            return false;
        }
        status("반복 원인을 분석해 다른 경로를 찾고 있어요. " + strategy);
        return true;
    }

    private void showPartialResult(JSONObject action) throws Exception {
        StringBuilder message = new StringBuilder("조건·근거 검증을 끝내지 못한 부분 결과입니다. 요청을 모두 충족한 것으로 확인되지 않았습니다.\n");
        JSONArray issues = mContract.completionIssues(action.optJSONArray("candidateIds"));
        for (int i = 0; i < Math.min(issues.length(), 4); i++) message.append("• ").append(issues.optString(i)).append('\n');
        message.append("\nAI가 정리한 내용 (추가 확인 필요)\n").append(action.optString("message"));
        JSONObject partial = new JSONObject(action.toString()).put("type", "handoff").put("message", message.toString());
        finishTask(partial);
        status("조건 확인이 남은 부분 결과를 표시했어요. 작업 기록에서 확인하고 이어서 실행할 수 있습니다.");
    }

    private void verifyFinish(int generation, long epoch, String url, JSONObject before, JSONObject action) {
        if (!samePage(generation, epoch, url)) return;
        final long operation = ++mOperationId;
        mBusy = true;
        status("결과를 표시하기 전에 현재 페이지가 그대로인지 마지막으로 확인합니다.");
        mWeb.evaluateJavascript(BrowserPageScript.observe(), value -> {
            if (!valid(generation) || operation != mOperationId) return;
            if (!samePage(generation, epoch, url)) return;
            try {
                JSONObject after = new JSONObject(value);
                if (!url.equals(after.optString("url")) || after.optBoolean("sensitive", true)
                        || !BrowserObservationPolicy.ready(after, SystemClock.elapsedRealtime() - mSettleStarted)
                        || !BrowserObservationPolicy.signature(before).equals(BrowserObservationPolicy.signature(after))) {
                    mMemory.feedback("CRITERIA");
                    recoverObservation(generation, "결과 판단 중 페이지가 바뀌어 현재 상태와 조건을 다시 확인합니다.");
                    return;
                }
                mLastObservation = after;
                finishTask(action);
            } catch (Exception invalid) {
                recoverObservation(generation, "마지막 페이지 상태를 다시 확인합니다.");
            }
        });
    }

    private void finishTask(JSONObject action) {
        String type = action.optString("type");
        String message = clean(action.optString("message"), 300);
        mMemory.record(action, "handoff".equals(type) ? "handoff" : "applied", message);
        record(type, "handoff".equals(type) ? "handoff" : "applied", message);
        mLastResult = action.optString("message");
        JSONArray catalog = action.optJSONArray("catalog");
        JSONArray evidence = action.optJSONArray("evidence");
        if (evidence != null && evidence.length() > 0) {
            StringBuilder sources = new StringBuilder("\n\n결과의 페이지 근거\n");
            for (int i = 0; i < evidence.length(); i++) {
                JSONObject source = evidence.optJSONObject(i);
                if (source != null) sources.append(source.optString("url")).append('\n')
                        .append(source.optString("quote")).append("\n\n");
            }
            mLastResult += sources.toString();
        }
        mCompleted = "finish".equals(type);
        stopRun(mCompleted ? "결과를 채팅에 정리했어요. 페이지 보기에서 직접 확인할 수 있습니다."
                : "추가 확인이 필요한 결과를 채팅에 표시했어요. 이어서 실행할 수 있습니다.");
        addCatalog(catalog, mLastObservation);
        addPageMessage(mLastResult, mLastObservation);
        BrowserTaskService.complete(this, mCompleted ? "작업을 완료했습니다" : "확인이 필요합니다", clean(mLastResult, 240));
        setPageVisible(false);
    }

    private void addCatalog(JSONArray catalog, JSONObject observation) {
        if (mTimeline == null || catalog == null || catalog.length() == 0) return;
        List<View> handles = mTimeline.addCatalog(catalog);
        if (!localPreviewSafe(observation)) return;
        JSONArray images = observation.optJSONArray("images");
        if (images == null || images.length() == 0) return;
        for (int i = 0; i < Math.min(handles.size(), catalog.length()); i++) {
            JSONObject item = catalog.optJSONObject(i);
            String imageId = item == null ? "" : item.optString("imageId");
            if (imageId.isEmpty()) continue;
            JSONObject image = null;
            for (int j = 0; j < images.length(); j++) {
                JSONObject candidate = images.optJSONObject(j);
                if (candidate != null && imageId.equals(candidate.optString("id"))) { image = candidate; break; }
            }
            if (image != null) attachCatalogSourceImage(handles.get(i), image, mConversationSerial);
        }
    }

    private void attachCatalogSourceImage(View handle, JSONObject image, long conversation) {
        String source = image.optString("src"), alt = clean(image.optString("alt"), 200);
        try { source = BrowserUrlPolicy.requirePublicHttps(source); }
        catch (RuntimeException invalid) { return; }
        String cookie = null;
        try { cookie = CookieManager.getInstance().getCookie(source); }
        catch (RuntimeException ignored) { }
        final String imageUrl = source, imageCookie = cookie;
        final BrowserChatTimeline timeline = mTimeline;
        try {
            mWorker.execute(() -> {
                Bitmap bitmap = BrowserImageLoader.load(imageUrl, imageCookie);
                mUi.post(() -> {
                    try {
                        if (bitmap != null && !mDestroyed && timeline != null && timeline == mTimeline
                                && conversation == mConversationSerial) timeline.attachCatalogImage(handle, bitmap, alt);
                    } finally { if (bitmap != null && !bitmap.isRecycled()) bitmap.recycle(); }
                });
            });
        } catch (RuntimeException ignored) { }
    }

    private void executeAction(int generation, long epoch, String url, JSONObject observation, JSONObject action) {
        if (!samePage(generation, epoch, url)) return;
        final String type = action.optString("type");
        final boolean mayNavigate = "click".equals(type) || ("type".equals(type) && action.optBoolean("submit"));
        final long operation = ++mOperationId;
        mExpectedNavigation = mayNavigate;
        try { pendingAction(action, observation); }
        catch (Exception error) { stopRun("동작 정보를 준비하지 못했어요."); return; }
        status(mSteps + "/" + mMaxSteps + " · " + clean(action.optString("message"), 200));
        final boolean nativeTap = "click".equals(type) && "native".equals(action.optString("method"));
        if ((mPageVisible || nativeTap) && action.has("targetId")) {
            mWeb.evaluateJavascript(BrowserPageScript.preview(action, url, observation.optString("documentId")), value -> {
                if (!valid(generation) || operation != mOperationId) return;
                try {
                    JSONObject preview = new JSONObject(value);
                    if (!preview.optBoolean("ok")) {
                        mPendingAction = null;
                        mBeforeAction = null;
                        String code = BrowserRecoveryPolicy.code(preview.optString("code"));
                        rejectAction(generation, action, BrowserRecoveryPolicy.feedback(code).optString("strategy"), code);
                        return;
                    }
                    if (nativeTap) {
                        mUi.postDelayed(() -> dispatchNativeTap(generation, epoch, url, observation, action,
                                operation, mayNavigate, preview), 120);
                        return;
                    }
                } catch (Exception invalid) {
                    mPendingAction = null;
                    mBeforeAction = null;
                    rejectAction(generation, action, "조작 대상을 다시 확인합니다.", "STALE_TARGET");
                    return;
                }
                mUi.postDelayed(() -> dispatchPageAction(generation, epoch, url, observation, action, operation, mayNavigate), 320);
            });
            return;
        }
        dispatchPageAction(generation, epoch, url, observation, action, operation, mayNavigate);
    }

    private void dispatchNativeTap(int generation, long epoch, String url, JSONObject observation,
            JSONObject action, long operation, boolean mayNavigate, JSONObject preview) {
        if (!samePage(generation, epoch, url) || operation != mOperationId || mWeb == null) return;
        try {
            double cssX = preview.getDouble("centerX"), cssY = preview.getDouble("centerY");
            double viewportWidth = preview.getDouble("viewportWidth"), viewportHeight = preview.getDouble("viewportHeight");
            int width = mWeb.getWidth() > 0 ? mWeb.getWidth() : mWeb.getMeasuredWidth();
            int height = mWeb.getHeight() > 0 ? mWeb.getHeight() : mWeb.getMeasuredHeight();
            if (!Double.isFinite(cssX) || !Double.isFinite(cssY) || !Double.isFinite(viewportWidth)
                    || !Double.isFinite(viewportHeight) || viewportWidth <= 0 || viewportHeight <= 0 || width <= 0 || height <= 0)
                throw new IllegalArgumentException("Invalid native target");
            float x = (float) Math.max(1, Math.min(width - 1, cssX * width / viewportWidth));
            float y = (float) Math.max(1, Math.min(height - 1, cssY * height / viewportHeight));
            long downTime = SystemClock.uptimeMillis();
            MotionEvent down = MotionEvent.obtain(downTime, downTime, MotionEvent.ACTION_DOWN, x, y, 0);
            MotionEvent up = MotionEvent.obtain(downTime, downTime + 48, MotionEvent.ACTION_UP, x, y, 0);
            down.setSource(InputDevice.SOURCE_TOUCHSCREEN);
            up.setSource(InputDevice.SOURCE_TOUCHSCREEN);
            mNativeTapInProgress = true;
            mAgentGestureUntil = SystemClock.elapsedRealtime() + 1500;
            try {
                mWeb.dispatchTouchEvent(down);
                mWeb.dispatchTouchEvent(up);
            } finally {
                down.recycle(); up.recycle(); mNativeTapInProgress = false;
            }
            if (operation == mOperationId && samePage(generation, epoch, url)) {
                mExpectedNavigation = mayNavigate;
                mBusy = false;
                scheduleObservation(generation, 500);
                if (mExpectedNavigation) navigationWatchdog(generation);
            }
        } catch (Exception invalid) {
            mNativeTapInProgress = false;
            mPendingAction = null;
            mBeforeAction = null;
            rejectAction(generation, action, "현재 요소의 실제 터치 위치를 확인하지 못했습니다.", "STALE_TARGET");
        }
    }

    private boolean isAgentGesture() {
        return mNativeTapInProgress || SystemClock.elapsedRealtime() < mAgentGestureUntil;
    }

    private void dispatchPageAction(int generation, long epoch, String url, JSONObject observation,
            JSONObject action, long operation, boolean mayNavigate) {
        if (!samePage(generation, epoch, url) || operation != mOperationId) return;
        final String type = action.optString("type");
        mWeb.evaluateJavascript(BrowserPageScript.action(action, url, observation.optString("documentId")), value -> {
            if (!valid(generation) || operation != mOperationId) return;
            if (mDocumentEpoch != epoch || mLoading || !url.equals(mWeb.getUrl())) {
                mExpectedNavigation = true;
                if (!mLoading) { mBusy = false; scheduleObservation(generation, 300); }
                else navigationWatchdog(generation);
                return;
            }
            try {
                JSONObject result = new JSONObject(value);
                if (!result.optBoolean("ok")) {
                    mPendingAction = null;
                    mBeforeAction = null;
                    String code = BrowserRecoveryPolicy.code(result.optString("code"));
                    rejectAction(generation, action, BrowserRecoveryPolicy.feedback(code).optString("strategy"), code); return;
                }
                if ("inspect".equals(type)) {
                    mPendingAction = null;
                    mBeforeAction = null;
                    record(type, "applied", "요소 상세 관찰 요청");
                }
                // Dispatch is not success. Re-observe and compare state before recording an outcome.
                mExpectedNavigation = result.optBoolean("navigating", mayNavigate);
                mBusy = false;
                scheduleObservation(generation, 500);
                if (mExpectedNavigation) navigationWatchdog(generation);
            } catch (Exception error) {
                // An unknown callback may follow an applied mutation. Never replay it automatically.
                mBusy = false;
                scheduleObservation(generation, 500);
            }
        });
    }

    private void rejectAction(int generation, JSONObject action, String message) {
        rejectAction(generation, action, message, "UNKNOWN");
    }

    private void rejectAction(int generation, JSONObject action, String message, String code) {
        if (!valid(generation)) return;
        record(action.optString("type", "inspect"), "rejected", message);
        mMemory.record(action, "rejected", message);
        mMemory.feedback(code);
        mNoProgress++;
        mBusy = false;
        mExpectedNavigation = false;
        recoverObservation(generation, "요소를 다시 확인하고 다른 동작을 판단합니다.");
    }

    private void navigationWatchdog(int generation) {
        final long navigation = mNavigationRequest;
        final long epoch = mDocumentEpoch;
        mUi.postDelayed(() -> {
            if (!valid(generation) || navigation != mNavigationRequest || !mExpectedNavigation) return;
            if (!mLoading) {
                mExpectedNavigation = false;
                mBusy = false;
                scheduleObservation(generation, 100);
            } else if (epoch == mDocumentEpoch) {
                mWeb.stopLoading();
                mLoading = false;
                mExpectedNavigation = false;
                mBusy = false;
                mSettleStarted = SystemClock.elapsedRealtime();
                status("느린 리소스 로딩을 멈추고 현재 표시된 페이지를 확인합니다.");
                scheduleObservation(generation, 500);
            } else navigationWatchdog(generation);
        }, mLoading ? 30000 : 1500);
    }

    private void stopRun(String message) {
        boolean hadWork = mRunning || mChatBusy || mRouting;
        mRouteSerial++;
        if (mLocationProvider != null) mLocationProvider.cancel();
        clearPendingLocationRoute();
        mLocationUseAuthorized = false;
        mRouting = false;
        mQueuedGoal = "";
        mChatSerial++;
        mChatBusy = false;
        clearHumanPanel();
        mAnswerGrant = null;
        if (mTouch != null) mTouch.cancel();
        mUserScrollUntil = 0;
        mGeneration++;
        mObservationSerial++;
        mNavigationRequest++;
        mOperationId++;
        mRunning = false;
        mBusy = false;
        mExpectedNavigation = false;
        cancelActivePlan();
        if (mPendingAction != null) {
            mMemory.record(mPendingAction, "unknown", "작업 중지로 결과를 확인하지 못함. 다시 실행하기 전에 현재 페이지를 확인할 것.");
            mPendingAction = null;
            mBeforeAction = null;
        }
        if (!mDestroyed) {
            status(message);
            browserActivity(message.isEmpty() ? "대기 중" : "중지 · " + clean(message, 240));
            if (hadWork && mTimeline != null && !message.isEmpty()) mTimeline.addAssistant(message);
            updateBrowserVisibility();
            updateControls();
        }
    }

    private void showTaskDetails() {
        if (mDestroyed || isFinishing()) return;
        StringBuilder text = new StringBuilder();
        if (!mLastResult.isEmpty()) text.append(mLastResult).append("\n\n");
        if (!mGoal.isEmpty()) text.append("목표\n").append(mGoal).append("\n\n");
        appendTaskConditions(text);
        appendResearchPlan(text);
        JSONObject memory = mMemory.toJson();
        JSONArray plan = memory.optJSONArray("plan");
        if (plan != null && plan.length() > 0) {
            text.append("계획\n");
            for (int i = 0; i < plan.length(); i++) text.append(i + 1).append(". ").append(plan.optString(i)).append('\n');
            text.append('\n');
        }
        JSONArray facts = memory.optJSONArray("facts");
        if (facts != null && facts.length() > 0) {
            text.append("수집한 정보와 출처\n");
            for (int i = 0; i < facts.length(); i++) {
                JSONObject fact = facts.optJSONObject(i);
                if (fact == null) continue;
                text.append("• ").append(fact.optString("text")).append('\n')
                        .append(fact.optString("url")).append('\n')
                        .append("근거: ").append(fact.optString("evidence")).append("\n\n");
            }
        }
        text.append("최근 동작\n");
        for (int i = 0; i < mHistory.length(); i++) {
            JSONObject item = mHistory.optJSONObject(i);
            if (item != null) text.append(item.optString("type")).append(" · ").append(item.optString("status"))
                    .append(" · ").append(item.optString("message")).append('\n');
        }
        if (!mHasTask) text.append("아직 시작한 작업이 없습니다.");
        addPageMessage(text.toString(), mLastObservation);
        addResearchSourceCards();
        setPageVisible(false);
    }

    private void addResearchSourceCards() {
        if (mTimeline == null || mResearch == null) return;
        JSONArray sources = mResearch.optJSONArray("sources");
        if (sources == null || sources.length() == 0) return;
        JSONArray cards = new JSONArray();
        try {
            for (int i = 0; i < Math.min(8, sources.length()); i++) {
                JSONObject source = sources.optJSONObject(i);
                if (source == null) continue;
                cards.put(new JSONObject().put("title", source.optString("title", "검색 출처"))
                        .put("subtitle", source.getString("url"))
                        .put("badges", new JSONArray().put("검색 출처 · 눌러서 열기")));
            }
        } catch (Exception invalid) { return; }
        List<View> handles = mTimeline.addCatalog(cards);
        for (int i = 0; i < Math.min(handles.size(), sources.length()); i++) {
            JSONObject source = sources.optJSONObject(i);
            if (source == null) continue;
            final String url;
            try { url = BrowserUrlPolicy.requirePublicHttps(source.optString("url")); }
            catch (RuntimeException invalid) { continue; }
            View card = handles.get(i);
            card.setClickable(true);
            card.setFocusable(true);
            card.setContentDescription(source.optString("title", "검색 출처") + " 출처 열기");
            card.setOnClickListener(view -> { navigate(url, false); setPageVisible(true); });
        }
    }

    private void appendTaskConditions(StringBuilder text) {
        if (mContract == null) return;
        JSONObject task = mContract.toJson();
        if (!task.optString("lastError").isEmpty()) text.append("조건 확인: ").append(task.optString("lastError")).append("\n\n");
        if (task.optBoolean("unsupported")) text.append(task.optString("unsupportedReason")).append("\n\n");
        JSONArray requirements = task.optJSONArray("requirements");
        if (requirements == null || requirements.length() == 0) return;
        text.append("요청 조건 · 목표 결과 수 ").append(task.optInt("targetCount", 1)).append("\n");
        for (int i = 0; i < requirements.length(); i++) {
            JSONObject requirement = requirements.optJSONObject(i);
            if (requirement != null) text.append("• ").append(requirement.optString("id")).append(" · ")
                    .append("preference".equals(requirement.optString("kind")) ? "선호" : "필수").append(" · ")
                    .append(requirement.optString("quote"))
                    .append("any".equals(requirement.optString("coverage")) ? " (결과 전체에서 확인)" : " (각 후보에 적용)").append('\n');
        }
        JSONArray candidates = task.optJSONArray("candidates");
        if (candidates != null && candidates.length() > 0) {
            text.append("\n후보별 조건 검토 (AI 판단과 관찰 근거)\n");
            for (int i = 0; i < candidates.length(); i++) {
                JSONObject candidate = candidates.optJSONObject(i);
                if (candidate == null) continue;
                text.append(candidate.optString("name")).append('\n');
                JSONArray checks = candidate.optJSONArray("checks");
                if (checks == null) continue;
                for (int j = 0; j < checks.length(); j++) {
                    JSONObject check = checks.optJSONObject(j);
                    if (check == null) continue;
                    String status = check.optString("status");
                    text.append("  ").append(check.optString("requirementId")).append(" · ")
                            .append("supported".equals(status) ? "근거 있음" : "contradicted".equals(status) ? "조건 불충족·충돌"
                            : "unavailable".equals(status) ? "현재 확인 불가" : "미확인").append('\n');
                    JSONArray evidence = check.optJSONArray("evidence");
                    if (evidence != null) for (int k = 0; k < evidence.length(); k++) {
                        JSONObject source = evidence.optJSONObject(k);
                        if (source != null) text.append("    ").append(source.optString("quote")).append('\n')
                                .append("    ").append(source.optString("url")).append('\n');
                    }
                }
                text.append('\n');
            }
        }
        text.append('\n');
    }

    private void appendResearchPlan(StringBuilder text) {
        if (mResearch == null || !mResearch.optBoolean("searched")) return;
        text.append("사전 리서치 경로\n");
        if (!mResearch.optString("summary").isEmpty()) text.append(mResearch.optString("summary")).append('\n');
        if (!mResearch.optString("entryUrl").isEmpty()) text.append("진입 URL: ").append(mResearch.optString("entryUrl")).append('\n');
        JSONArray graph = mResearch.optJSONArray("executionGraph");
        if (graph != null) for (int i = 0; i < graph.length(); i++) {
            JSONObject step = graph.optJSONObject(i);
            if (step != null) text.append(i + 1).append(". ").append(step.optString("expected"))
                    .append(" · 위험 ").append(step.optString("risk")).append('\n');
        }
        JSONArray sources = mResearch.optJSONArray("sources");
        if (sources != null && sources.length() > 0) {
            text.append("검색 출처\n");
            for (int i = 0; i < sources.length(); i++) {
                JSONObject source = sources.optJSONObject(i);
                if (source != null) text.append("• ").append(source.optString("title")).append('\n')
                        .append(source.optString("url")).append('\n');
            }
        }
        text.append("검색 정보는 경로 탐색용이며 현재 가격·재고·완료 여부는 브라우저에서 다시 검증합니다.\n\n");
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
        BrowserTaskService.attach(this);
        if (mWeb != null && mBrowserEnabled) mWeb.onResume();
        if (mRestoreSettings) restoreSettings();
        updateControls();
    }

    private void restoreSettings() {
        if (mSettingsHandler == null) {
            status("브라우저를 닫고 다시 열어 저장된 설정을 불러와 주세요.");
            return;
        }
        mRestoringSettings = true;
        final long requestId = ++mSettingsRequestId;
        status("저장된 설정을 불러오는 중이에요.");
        try {
            mSettingsHandler.request(true, "", "", mMaxSteps, mTimeoutSeconds,
                    (key, model, steps, seconds, failure) -> {
                        if (mDestroyed || !mForeground || requestId != mSettingsRequestId) return;
                        mRestoringSettings = false;
                        if (failure != null) {
                            status(failure);
                        } else if (validKey(key) && validModel(model)
                                && steps >= 1 && steps <= 100 && seconds >= 30 && seconds <= 1800) {
                            mApiKey = key;
                            mModel = model;
                            mMaxSteps = steps;
                            mTimeoutSeconds = seconds;
                            mRestoreSettings = false;
                            status(mModel + " 설정을 불러왔어요. AI 실행을 눌러 시작하세요.");
                        } else {
                            status("설정에서 API 키와 모델을 입력해 주세요.");
                        }
                        updateControls();
                    });
        } catch (RuntimeException failure) {
            mRestoringSettings = false;
            status("저장된 설정을 불러오지 못했어요. 설정에서 다시 저장해 주세요.");
        }
    }

    @Override protected void onPause() {
        mRestoringSettings = false;
        dismissSettings();
        boolean active = mRouting || mRunning || mChatBusy || !mHumanMode.isEmpty();
        mForeground = active;
        if (active) BrowserTaskService.update(this, "answer".equals(mHumanMode) ? "Vitlane AI가 답변을 기다립니다" : "Vitlane AI가 백그라운드에서 실행 중입니다",
                "answer".equals(mHumanMode) && mHumanQuestion != null ? clean(mHumanQuestion.getText().toString(), 500)
                        : !mHumanMode.isEmpty() ? "브라우저에서 보안 입력이 필요합니다." : "작업이 끝나거나 답변이 필요하면 알려드릴게요.",
                "answer".equals(mHumanMode));
        else if (mWeb != null) mWeb.onPause();
        super.onPause();
    }

    @Override protected void onStop() {
        dismissSettings();
        boolean active = mRouting || mRunning || mChatBusy || !mHumanMode.isEmpty();
        if (!active) {
            mApiKey = "";
            mRestoreSettings = true;
            status("설정을 다시 불러온 뒤 새 요청을 보낼 수 있습니다.");
        }
        updateControls();
        super.onStop();
    }

    @Override protected void onNewIntent(Intent intent) {
        super.onNewIntent(intent);
        setIntent(intent);
        if (BrowserTaskService.ACTION_OPEN.equals(intent.getAction()))
            setPageVisible("manual".equals(mHumanMode));
    }

    void onNotificationReply(String reply) {
        mUi.post(() -> {
            if (mDestroyed || reply == null || reply.trim().isEmpty()) return;
            if ("answer".equals(mHumanMode) && mSubmitHumanAnswer != null) {
                mGoalInput.setText(reply.trim());
                mSubmitHumanAnswer.run();
            } else if (!mRunning && !mChatBusy && !mRouting && mHumanMode.isEmpty()) {
                mGoalInput.setText(reply.trim());
                sendMessage();
            }
        });
    }

    void onNotificationStop() {
        mUi.post(() -> { if (!mDestroyed) stopRun("알림에서 작업을 중지했어요."); });
    }

    private void requestNotificationPermission() {
        if (Build.VERSION.SDK_INT >= 33 && checkSelfPermission(Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED)
            requestPermissions(new String[]{Manifest.permission.POST_NOTIFICATIONS}, 7201);
    }

    @Override public void onRequestPermissionsResult(int requestCode, String[] permissions, int[] grantResults) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults);
        if (requestCode != LOCATION_PERMISSION_REQUEST) return;
        if (mDestroyed || mLocationRouteSerial != mRouteSerial || !mRouting) { clearPendingLocationRoute(); return; }
        if (BrowserLocationProvider.hasPermission(this)) resolveLocationForRoute();
        else {
            long serial = mLocationRouteSerial;
            String text = mLocationRouteText;
            JSONArray conversation = mLocationRouteConversation == null ? new JSONArray() : mLocationRouteConversation;
            clearPendingLocationRoute();
            status("위치 권한 없이 웹 검색을 계속합니다. 필요한 경우 지역을 질문할 수 있어요.");
            startBrowserResearch(serial, text, conversation, BrowserLocationContext.unavailable("permission_denied"));
        }
    }

    @Override public void onBackPressed() {
        if (mPageVisible) { setPageVisible(false); return; }
        super.onBackPressed();
    }

    @Override protected void onDestroy() {
        stopRun("");
        mDestroyed = true;
        if (mStatusPulse != null) { mStatusPulse.cancel(); mStatusPulse = null; }
        if (mLocationProvider != null) { mLocationProvider.cancel(); mLocationProvider = null; }
        BrowserTaskService.detach(this);
        BrowserTaskService.stop(this);
        mApiKey = "";
        dismissSettings();
        if (mSitePromptDialog != null) { mSitePromptDialog.dismiss(); mSitePromptDialog = null; }
        if (mSitePromptResult != null) { mSitePromptResult.cancel(); mSitePromptResult = null; }
        mSettingsHandler = null;
        mGoal = "";
        mUserReplies = new JSONArray();
        mAnswerGrant = null;
        mHistory = new JSONArray();
        mMemory.clear();
        mPersonalDataFailures.clear();
        mPersonalDataApplied.clear();
        mContract = null;
        mResearch = BrowserResearchPlanner.empty("");
        mLastObservation = null;
        mLastResult = "";
        mChatMessages = new JSONArray();
        mBrowserConversation = new JSONArray();
        mSecretRedactions.clear();
        mConversationSerial++;
        if (mTimeline != null) { mTimeline.release(); mTimeline = null; }
        if (mGoalInput != null) mGoalInput.setText("");
        mUi.removeCallbacksAndMessages(null);
        mWorker.shutdownNow();
        mSuspendedUrl = "";
        if (mWeb != null) {
            releaseWebView(mWeb);
            mWeb = null;
        }
        if (mPrivateData != null) { mPrivateData.close(); mPrivateData = null; }
        super.onDestroy();
    }

    private void updateControls() {
        if (mStart == null) return;
        boolean answering = "answer".equals(mHumanMode);
        boolean blocked = !mHumanMode.isEmpty() && !answering;
        boolean canAnswer = answering && !mRestoringSettings;
        boolean active = !answering && (mRunning || mChatBusy || mRouting || mLoading || mRestoringSettings);
        mStart.setEnabled(canAnswer
                || !mRunning && !mChatBusy && !mRouting && !blocked && !mRestoringSettings);
        String startText = answering ? "답변 보내기" : mRunning || mChatBusy || mRouting ? "응답 중" : "보내기";
        if (!startText.equals(mRenderedStartText)) {
            mRenderedStartText = startText;
            mStart.setText(startText);
            if (motionEnabled() && mStart.isAttachedToWindow()) {
                mStart.animate().cancel();
                mStart.setScaleX(0.92f);
                mStart.setScaleY(0.92f);
                mStart.setAlpha(0.7f);
                mStart.animate().scaleX(1f).scaleY(1f).alpha(1f).setDuration(220)
                        .setInterpolator(new OvershootInterpolator(1.05f)).start();
            }
        }
        mStart.setTextColor(active ? BRAND : Color.WHITE);
        mStart.setBackground(background(active ? ACCENT : BRAND, 18));
        mStop.setEnabled(mRunning || mChatBusy || mRouting || !mHumanMode.isEmpty());
        if (mResume != null) mResume.setEnabled(mHasTask && !mCompleted && !mRunning && !mChatBusy && !mRouting && mHumanMode.isEmpty() && !mLoading && !mRestoringSettings);
        // The intent router deliberately stays active while a pre-search question is pending.
        // Answer mode owns the composer in that state, so routing must not make it read-only.
        mGoalInput.setEnabled(canAnswer || !mRunning && !mChatBusy && !mRouting && !blocked);
        setTaskControlsVisible(mHasTask || mChatBusy || mRouting);
        if (mResume != null) mResume.setVisibility(mBrowserEnabled ? View.VISIBLE : View.GONE);
        if (mDetails != null) mDetails.setVisibility(mBrowserEnabled ? View.VISIBLE : View.GONE);
        if (mManual != null) mManual.setVisibility(mBrowserEnabled ? View.VISIBLE : View.GONE);
        if (mWeb != null) {
            if (mBack != null) mBack.setEnabled(mWeb.canGoBack());
            if (mForward != null) mForward.setEnabled(mWeb.canGoForward());
        }
        updateStatusPulse(active);
        updateBrowserVisibility();
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
        return value.matches("(?is).*(?:결제\\s*(?:하기|확정|완료)|구매\\s*확정|pay\\s*now|confirm\\s*(?:purchase|payment)|complete\\s*(?:purchase|payment)).*");
    }

    private static boolean sameOrigin(String first, String second) {
        try {
            URI a = new URI(first), b = new URI(second);
            int ap = a.getPort() >= 0 ? a.getPort() : "https".equalsIgnoreCase(a.getScheme()) ? 443 : 80;
            int bp = b.getPort() >= 0 ? b.getPort() : "https".equalsIgnoreCase(b.getScheme()) ? 443 : 80;
            return "https".equalsIgnoreCase(a.getScheme()) && "https".equalsIgnoreCase(b.getScheme())
                    && a.getHost() != null && a.getHost().equalsIgnoreCase(b.getHost()) && ap == bp;
        } catch (Exception ignored) { return false; }
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

    /** A blocked child navigation is an action failure, not the end of the browser task. */
    private void recoverBlockedNavigation(String raw, String message) {
        if (!mRunning) {
            status(message);
            browserActivity("이동 차단 · 현재 웹페이지를 유지했습니다");
            return;
        }
        JSONObject rejected = mPendingAction;
        mPendingAction = null;
        mBeforeAction = null;
        mExpectedNavigation = false;
        mBusy = false;
        mNavigationRequest++;
        mOperationId++; // Ignore a late JavaScript callback from the click that requested this URL.
        if (rejected == null) {
            rejected = new JSONObject();
            try {
                rejected.put("type", "inspect");
                rejected.put("message", "차단된 하위 이동 뒤 현재 페이지 재확인");
            } catch (Exception ignored) { /* Fixed local values. */ }
        }
        String code = BrowserUrlPolicy.isExternalAppNavigation(raw)
                ? "EXTERNAL_NAVIGATION" : "BLOCKED_NAVIGATION";
        rejectAction(mGeneration, rejected, message, code);
    }

    private boolean restoreLastSafePage(WebView view) {
        String previous = mLastObservation == null ? "" : mLastObservation.optString("url");
        if (view == null || !allowedUrl(previous) || previous.equals(view.getUrl())) return false;
        browserActivity("페이지 복구 · 마지막으로 확인한 공개 웹페이지로 돌아갑니다");
        trackPageLoad(view);
        view.loadUrl(previous);
        return true;
    }

    private static boolean navigationUrlAllowed(String url) {
        try { BrowserUrlPolicy.requirePublicHttpsNavigation(url); return true; }
        catch (RuntimeException error) { return false; }
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

    private void status(String message) {
        if (mStatus == null) return;
        String next = clean(message, 420);
        if (next.contentEquals(mStatus.getText())) return;
        mStatus.setText(next);
        if (motionEnabled() && mStatus.isAttachedToWindow()) {
            mStatus.animate().cancel();
            mStatus.setAlpha(0.45f);
            mStatus.setTranslationY(dp(2));
            mStatus.animate().alpha(1f).translationY(0f).setDuration(180)
                    .setInterpolator(new DecelerateInterpolator()).start();
        }
    }
    private void browserActivity(String message) {
        if (mBrowserActivity == null) return;
        String next = clean(message, 320);
        if (next.contentEquals(mBrowserActivity.getText())) return;
        mBrowserActivity.setText(next);
        if (motionEnabled() && mBrowserActivity.isAttachedToWindow()) {
            mBrowserActivity.animate().cancel();
            mBrowserActivity.setAlpha(0.5f);
            mBrowserActivity.animate().alpha(1f).setDuration(180).start();
        }
    }
    private static String actionLabel(String type) {
        switch (type) {
            case "research": return "페이지 유지 검색";
            case "search": return "검색";
            case "navigate": case "back": return "페이지 이동";
            case "click": return "클릭";
            case "type": return "입력";
            case "select": return "옵션 선택";
            case "check": return "선택 상태 변경";
            case "scroll": return "스크롤";
            case "wait": return "페이지 대기";
            case "finish": return "최종 검증";
            default: return "페이지 확인";
        }
    }
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
        view.setTypeface(Typeface.create("sans-serif-medium", Typeface.NORMAL));
        view.setMinWidth(0); view.setMinimumWidth(0); view.setPadding(dp(5), 0, dp(5), 0); view.setBackground(background(SURFACE, 16));
        view.setOnClickListener(ignored -> action.run());
        pressable(view);
        return view;
    }
    private GradientDrawable background(int color) {
        return background(color, 10);
    }
    private GradientDrawable background(int color, int radius) {
        GradientDrawable drawable = new GradientDrawable(); drawable.setColor(color); drawable.setCornerRadius(dp(radius)); return drawable;
    }
    private GradientDrawable strokeBackground(int color, int radius, int stroke) {
        GradientDrawable drawable = background(color, radius);
        drawable.setStroke(dp(1), stroke);
        return drawable;
    }
    private GradientDrawable circleBackground(int color) {
        GradientDrawable drawable = new GradientDrawable();
        drawable.setShape(GradientDrawable.OVAL);
        drawable.setColor(color);
        return drawable;
    }
    private boolean motionEnabled() {
        return Build.VERSION.SDK_INT < 26 || ValueAnimator.areAnimatorsEnabled();
    }
    private void animateEntrance(View view, long delay) {
        if (!motionEnabled()) return;
        view.setAlpha(0f);
        view.setTranslationY(dp(8));
        view.post(() -> {
            if (mDestroyed || !view.isAttachedToWindow()) {
                view.setAlpha(1f);
                view.setTranslationY(0f);
                return;
            }
            view.animate().alpha(1f).translationY(0f).setStartDelay(delay).setDuration(280)
                    .setInterpolator(new DecelerateInterpolator(1.6f)).start();
        });
    }
    private void pressable(View view) {
        view.setOnTouchListener((pressed, event) -> {
            if (!pressed.isEnabled() || !motionEnabled()) return false;
            int action = event.getActionMasked();
            if (action == MotionEvent.ACTION_DOWN) {
                pressed.animate().cancel();
                pressed.animate().scaleX(0.96f).scaleY(0.96f).setDuration(80).start();
            } else if (action == MotionEvent.ACTION_UP || action == MotionEvent.ACTION_CANCEL) {
                pressed.animate().cancel();
                pressed.animate().scaleX(1f).scaleY(1f).setDuration(210)
                        .setInterpolator(new OvershootInterpolator(1.15f)).start();
            }
            return false;
        });
    }
    private void updateStatusPulse(boolean active) {
        if (mStatusDot == null) return;
        mStatusDot.setBackground(circleBackground(active ? BRAND : mCompleted ? POSITIVE : 0xffa9acb8));
        if (!active || !motionEnabled() || !mStatusDot.isAttachedToWindow()) {
            if (mStatusPulse != null) { mStatusPulse.cancel(); mStatusPulse = null; }
            mStatusDot.setAlpha(1f);
            return;
        }
        if (mStatusPulse != null && mStatusPulse.isRunning()) return;
        mStatusPulse = ObjectAnimator.ofFloat(mStatusDot, View.ALPHA, 1f, 0.28f, 1f);
        mStatusPulse.setDuration(1300);
        mStatusPulse.setRepeatCount(ValueAnimator.INFINITE);
        mStatusPulse.setInterpolator(new DecelerateInterpolator());
        mStatusPulse.start();
    }
    private void setTaskControlsVisible(boolean visible) {
        if (mTaskControls == null || mTaskControlsShown == visible) return;
        mTaskControlsShown = visible;
        mTaskControls.animate().cancel();
        if (!motionEnabled() || !mTaskControls.isAttachedToWindow()) {
            mTaskControls.setVisibility(visible ? View.VISIBLE : View.GONE);
            mTaskControls.setAlpha(1f);
            mTaskControls.setTranslationY(0f);
            return;
        }
        if (visible) {
            mTaskControls.setVisibility(View.VISIBLE);
            mTaskControls.setAlpha(0f);
            mTaskControls.setTranslationY(dp(6));
            mTaskControls.animate().alpha(1f).translationY(0f).setDuration(200)
                    .setInterpolator(new DecelerateInterpolator()).start();
        } else {
            mTaskControls.animate().alpha(0f).translationY(dp(4)).setDuration(150).withEndAction(() -> {
                if (!mTaskControlsShown) mTaskControls.setVisibility(View.GONE);
                mTaskControls.setAlpha(1f);
                mTaskControls.setTranslationY(0f);
            }).start();
        }
    }
}
