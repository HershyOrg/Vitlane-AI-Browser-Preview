// Copyright 2026 Vitlane Browser contributors. BSD-3-Clause.
package org.chromium.chrome.browser.lane;

import android.app.Activity;
import android.app.Dialog;
import android.graphics.Color;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.os.Handler;
import android.os.Looper;
import android.os.SystemClock;
import android.text.InputType;
import android.util.Base64;
import android.view.Gravity;
import android.view.View;
import android.view.ViewGroup;
import android.view.Window;
import android.view.WindowManager;
import android.widget.Button;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

import org.chromium.chrome.browser.tab.Tab;
import org.chromium.chrome.browser.tab.TabObserver;
import org.chromium.content_public.browser.WebContents;

import org.json.JSONArray;
import org.json.JSONObject;

import java.net.HttpURLConnection;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.text.Normalizer;
import java.text.SimpleDateFormat;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.Collections;
import java.util.Date;
import java.util.HashSet;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Set;
import java.util.TimeZone;
import java.util.UUID;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.function.Consumer;
import java.util.function.Supplier;

import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;

/**
 * Foreground-only native owner for Vitlane's constrained browser agent.
 *
 * <p>The model can only propose an action. This class verifies a native-issued HMAC, local run binding,
 * page identity, sequence, lease and control generation before the fixed isolated-world agent may
 * inspect or execute it. The OpenAI key remains in native process memory and is never sent
 * to page JavaScript or persisted. Run permits use a separate random key, never the API key.
 */
public final class LaneAgentCoordinator {
    private static final int PROTOCOL_VERSION = 1;
    private static final int MAX_STEPS = 20;
    private static final long RUN_TIMEOUT_MS = 300_000;
    private static final long SAFE_INTEGER_MAX = 9_007_199_254_740_991L;
    private static final long MAX_WON = 1_000_000_000_000L;
    private static final long PURCHASE_APPROVAL_TTL_MS = 10 * 60_000L;
    private static final String FRAME_ID = "frame_main";
    private static final String COUPANG_ADAPTER = "builtin.coupang.purchase-preparation";
    private static final Set<String> ACTION_KINDS =
            setOf("inspect_page", "open_candidate", "scroll", "run_preparation_step",
                    "request_human", "finish");
    private static final Set<String> HUMAN_REASON_CODES =
            setOf("AUTHENTICATION_REQUIRED", "SENSITIVE_INPUT_REQUIRED",
                    "FORM_SUBMISSION_REQUIRED", "PAYMENT_OR_COMMITMENT",
                    "UNSUPPORTED_INTERACTION", "CROSS_ORIGIN_FRAME",
                    "PRIVATE_NETWORK_BLOCKED", "PAGE_CHANGED", "USER_DECISION_REQUIRED");
    private static final Set<String> COUPANG_PRODUCT_ORIGINS =
            setOf("https://coupang.com", "https://www.coupang.com");
    private static final Set<String> COUPANG_PREPARATION_STEPS =
            setOf("select_option", "verify_options", "set_quantity", "buy_now",
                    "add_to_cart", "start_checkout");
    private static final Set<String> COUPANG_CONTINUING_STEPS =
            setOf("select_option", "verify_options", "set_quantity");
    private static final Set<String> COUPANG_ACTIVATION_HANDOFF_STEPS =
            setOf("buy_now", "add_to_cart", "start_checkout");

    private static final class CoupangProductIdentity {
        final String origin;
        final String path;
        final String productId;
        final String itemId;
        final String vendorItemId;

        CoupangProductIdentity(String origin, String path, String productId, String itemId,
                String vendorItemId) {
            this.origin = origin;
            this.path = path;
            this.productId = productId;
            this.itemId = itemId;
            this.vendorItemId = vendorItemId;
        }

        String canonicalProductUrl() {
            return "https://www.coupang.com/vp/products/" + productId
                    + "?itemId=" + itemId + "&vendorItemId=" + vendorItemId;
        }
    }

    private static final class JournalEntry {
        final String commandId;
        final String actionHash;
        final long sequence;
        final String actionKind;
        final String preparationAdapterId;
        final String preparationStepId;
        String state = "RECEIVED";
        String resultStatus;
        String resultCode;
        String displayMessage = "";

        JournalEntry(String commandId, String actionHash, long sequence, String actionKind,
                String preparationAdapterId, String preparationStepId) {
            this.commandId = commandId;
            this.actionHash = actionHash;
            this.sequence = sequence;
            this.actionKind = actionKind;
            this.preparationAdapterId = preparationAdapterId;
            this.preparationStepId = preparationStepId;
        }

        boolean isTerminal() {
            return "APPLIED".equals(state) || "OUTCOME_UNKNOWN".equals(state);
        }
    }

    private final Activity mActivity;
    private final Supplier<Tab> mCurrentTab;
    private final Handler mUi = new Handler(Looper.getMainLooper());
    private final ExecutorService mNetwork = Executors.newSingleThreadExecutor();
    private final String mDeviceId = "device_" + UUID.randomUUID();
    // This binding identifies the same persistent regular Chromium profile used
    // for user sign-in. Cookies and storage stay inside Chromium and are never
    // included in observations. The sanitizer removes account/profile chrome
    // from public product pages and fails closed on account/session/cart/payment
    // pages; checkout and payment always remain under direct user control.
    private final String mProfileRef = "profile_regular_unisolated";
    private final Button mLauncher;
    private final LinearLayout mControlBar;
    private final TextView mTrustedOrigin;
    private final TextView mCurrentAction;
    private final TextView mDataScope;
    private final LinkedHashMap<String, JournalEntry> mJournal =
            new LinkedHashMap<String, JournalEntry>() {
                @Override
                protected boolean removeEldestEntry(Map.Entry<String, JournalEntry> eldest) {
                    return size() > 64;
                }
            };

    private Dialog mPanel;
    private TextView mStatus;
    private EditText mGoalInput;
    private EditText mModelInput;
    private EditText mApiKeyInput;
    private EditText mCoupangQuantityInput;
    private EditText mCoupangUnitCeilingInput;
    private EditText mCoupangTotalCeilingInput;
    private EditText mCoupangOptionsInput;
    private Button mStartButton;
    private String mModel = "gpt-5-mini";
    private String mApiKey = "";
    private final byte[] mPermitKey = new byte[32];
    private String mGoal = "";
    private String mLog;
    private String mRunId = "";
    private String mLastObservationId = "";
    private String mLastObservationOrigin = "";
    private String mDocumentUrl = "";
    private Tab mTaskTab;
    private WebContents mTaskContents;
    private JSONArray mHistory = new JSONArray();
    private JSONObject mApprovedPreparation;
    private boolean mRunning;
    private boolean mDestroyed;
    private volatile int mGeneration;
    private long mControlGeneration;
    private long mDocumentEpoch;
    private long mSequence;
    private long mLeaseEpoch;
    private int mSteps;
    private long mDeadline;
    private volatile HttpURLConnection mConnection;

    private final TabObserver mTabObserver = new TabObserver() {
        @Override
        public void onHidden(Tab tab, int type) {
            if (mRunning) stop(tr("탭이 숨겨져 작업을 중지했습니다.",
                    "The tab was hidden, so the run stopped."));
        }

        @Override
        public void onDestroyed(Tab tab) {
            if (mRunning) stop(tr("탭이 닫혀 작업을 중지했습니다.",
                    "The tab was closed, so the run stopped."));
        }

        @Override
        public void onContentChanged(Tab tab) {
            if (mRunning && tab.getWebContents() != mTaskContents) {
                stop(tr("페이지 실행 환경이 변경되었습니다.",
                        "The page execution context changed."));
            }
        }

        @Override
        public void onLoadStarted(Tab tab, boolean toDifferentDocument) {
            if (mRunning && toDifferentDocument) {
                takeOver(navigationTakeoverMessage());
            }
        }

        @Override
        public void onUrlUpdated(Tab tab) {
            if (!mRunning) return;
            String url = tab.getUrl().getSpec();
            if (!url.equals(mDocumentUrl)) {
                takeOver(navigationTakeoverMessage());
            }
        }
    };

    public LaneAgentCoordinator(Activity activity, Supplier<Tab> currentTab) {
        mActivity = activity;
        mCurrentTab = currentTab;
        mModel = activity.getPreferences(0).getString(
                "lane.openai.model", "gpt-5-mini");
        mLog = tr("현재 브라우저의 로그인 세션은 그대로 유지됩니다. 공개 상품 페이지에서는 "
                        + "계정 영역을 제외한 상품 내용으로 검색과 비교를 돕습니다. 계정·장바구니·"
                        + "결제 화면은 수집하지 않습니다.",
                "Your sign-in stays in this browser session. On public product pages, I can "
                        + "search and compare using product content after account areas are removed. "
                        + "Account, cart, and payment pages are not collected.");

        mLauncher = button("AI", 0xffbaff68, 0xff17200f);
        mLauncher.setContentDescription(tr("Vitlane AI 브라우저 열기",
                "Open Vitlane AI Browser"));
        FrameLayout.LayoutParams launcherParams = new FrameLayout.LayoutParams(dp(64), dp(48));
        launcherParams.gravity = Gravity.BOTTOM | Gravity.END;
        launcherParams.setMargins(dp(16), dp(16), dp(16), dp(80));
        activity.addContentView(mLauncher, launcherParams);
        mLauncher.setOnClickListener(v -> showPanel());

        mControlBar = new LinearLayout(activity);
        mControlBar.setOrientation(LinearLayout.VERTICAL);
        mControlBar.setPadding(dp(14), dp(10), dp(14), dp(10));
        mControlBar.setBackground(background(0xf2182016));
        mTrustedOrigin = label("", 13);
        mTrustedOrigin.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
        mCurrentAction = label("", 12);
        mDataScope = label("", 11);
        mControlBar.addView(mTrustedOrigin);
        mControlBar.addView(mCurrentAction);
        mControlBar.addView(mDataScope);
        LinearLayout controls = new LinearLayout(activity);
        controls.setOrientation(LinearLayout.HORIZONTAL);
        Button stopButton = button(tr("중지", "Stop"), 0xff34412d, Color.WHITE);
        Button directControl = button(tr("직접 조작", "Take control"), 0xffbaff68, 0xff17200f);
        controls.addView(stopButton, weighted());
        controls.addView(directControl, weighted());
        stopButton.setOnClickListener(v -> stop(tr("작업을 중지했습니다.", "Run stopped.")));
        directControl.setOnClickListener(v -> takeOver());
        mControlBar.addView(controls);
        mControlBar.setVisibility(View.GONE);
        FrameLayout.LayoutParams barParams = new FrameLayout.LayoutParams(
                ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT);
        barParams.gravity = Gravity.TOP;
        barParams.setMargins(dp(8), dp(8), dp(8), dp(8));
        activity.addContentView(mControlBar, barParams);
    }

    private static Set<String> setOf(String... values) {
        return Collections.unmodifiableSet(new HashSet<>(Arrays.asList(values)));
    }

    private String tr(String korean, String english) {
        return "ko".equalsIgnoreCase(Locale.getDefault().getLanguage()) ? korean : english;
    }

    private String navigationTakeoverMessage() {
        return mApprovedPreparation == null
                ? tr("페이지 이동이 시작되어 자동 실행을 중지했습니다. 새 페이지를 확인한 뒤 "
                                + "다시 시작해 주세요.",
                        "Navigation started, so automation stopped. Review the new page and "
                                + "start again.")
                : tr("쿠팡 페이지 이동이 시작되어 구매 준비 자동화를 중지했습니다. 주문서와 최종 "
                                + "가격을 확인하고 로그인·최종 주문·결제는 직접 진행해 주세요.",
                        "Coupang navigation started, so preparation automation stopped. Review "
                                + "checkout and the final price, then complete sign-in, the final "
                                + "order, and payment yourself.");
    }

    private int dp(int value) {
        return Math.round(value * mActivity.getResources().getDisplayMetrics().density);
    }

    private LinearLayout.LayoutParams weighted() {
        LinearLayout.LayoutParams params =
                new LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1);
        params.setMargins(dp(4), dp(2), dp(4), dp(2));
        return params;
    }

    private GradientDrawable background(int color) {
        GradientDrawable drawable = new GradientDrawable();
        drawable.setColor(color);
        drawable.setCornerRadius(dp(20));
        return drawable;
    }

    private Button button(String title, int bg, int fg) {
        Button button = new Button(mActivity);
        button.setText(title);
        button.setTextColor(fg);
        button.setAllCaps(false);
        button.setBackground(background(bg));
        return button;
    }

    private TextView label(String value, int size) {
        TextView label = new TextView(mActivity);
        label.setText(value);
        label.setTextColor(0xffe9eee3);
        label.setTextSize(size);
        label.setPadding(0, dp(6), 0, dp(6));
        return label;
    }

    private EditText input(String hint, String value, int type) {
        EditText input = new EditText(mActivity);
        input.setHint(hint);
        input.setText(value);
        input.setTextColor(Color.WHITE);
        input.setHintTextColor(0xffa3ae99);
        input.setInputType(type);
        input.setPadding(dp(12), dp(10), dp(12), dp(10));
        input.setBackground(background(0xff283025));
        input.setEnabled(!mRunning);
        return input;
    }

    private void showPanel() {
        if (mDestroyed || mActivity.isFinishing()) return;
        if (mPanel != null && mPanel.isShowing()) return;
        mPanel = new Dialog(mActivity);
        mPanel.requestWindowFeature(Window.FEATURE_NO_TITLE);
        LinearLayout body = new LinearLayout(mActivity);
        body.setOrientation(LinearLayout.VERTICAL);
        body.setPadding(dp(22), dp(12), dp(22), dp(22));
        body.setBackground(background(0xff182016));
        TextView title = label(tr("Vitlane · AI 브라우저", "Vitlane · AI Browser"), 22);
        title.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
        body.addView(title);
        body.addView(label(tr("브라우저가 확인한 주소", "Browser-verified origin"), 12));
        body.addView(label(displayOrigin(), 14));
        body.addView(label(tr("무엇을 할까요?", "What should I do?"), 14));
        mGoalInput = input(tr("예: 이 페이지에서 가벼운 운동화를 찾아줘",
                        "Example: Find lightweight shoes on this site"),
                mGoal, InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_MULTI_LINE);
        mGoalInput.setMinLines(2);
        body.addView(mGoalInput);
        body.addView(label(tr("OpenAI 모델", "OpenAI model"), 13));
        mModelInput = input("gpt-5-mini", mModel,
                InputType.TYPE_CLASS_TEXT);
        body.addView(mModelInput);
        body.addView(label(tr("OpenAI API 키", "OpenAI API key"), 13));
        mApiKeyInput = input("sk-…", mApiKey,
                InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_VARIATION_PASSWORD);
        mApiKeyInput.setSaveEnabled(false);
        mApiKeyInput.setImportantForAutofill(View.IMPORTANT_FOR_AUTOFILL_NO_EXCLUDE_DESCENDANTS);
        body.addView(mApiKeyInput);
        body.addView(label(tr(
                "별도 서버 없이 OpenAI에 직접 연결합니다. API 키는 이 실행 중에만 보관하며 "
                        + "브라우저를 나가면 지웁니다. API 사용 요금은 본인 계정에 청구됩니다.",
                "Connects directly to OpenAI. Your API key is held for this foreground session "
                        + "and cleared when you leave the browser. Usage is billed to your account."), 12));
        body.addView(label(tr(
                "로그인 쿠키는 현재 브라우저 안에 유지되고 전송되지 않습니다. 로그인된 공개 상품 "
                        + "페이지에서는 계정·프로필 영역을 제외한 상품 텍스트와 검색 후보만 "
                        + "전송합니다. 계정·장바구니·결제 화면, 입력값, 비밀번호, OTP, 결제정보와 "
                        + "스크린샷은 제외합니다.",
                "Sign-in cookies stay in this browser and are not sent. On signed-in public "
                        + "product pages, only product text and search candidates are shared after "
                        + "account and profile areas are removed. Account, cart, and payment pages, "
                        + "input values, passwords, OTPs, payment data, and screenshots are excluded."), 12));

        CoupangProductIdentity coupangProduct = currentCoupangProduct();
        if (!mRunning && coupangProduct != null) {
            TextView preparationTitle = label(tr(
                    "현재 쿠팡 상품 구매 준비 승인",
                    "Approve preparation for this Coupang product"), 16);
            preparationTitle.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
            body.addView(preparationTitle);
            body.addView(label(tr(
                    "브라우저가 확인한 상품 · productId " + coupangProduct.productId
                            + " · itemId " + coupangProduct.itemId
                            + " · vendorItemId " + coupangProduct.vendorItemId,
                    "Browser-verified offer · productId " + coupangProduct.productId
                            + " · itemId " + coupangProduct.itemId
                            + " · vendorItemId " + coupangProduct.vendorItemId), 11));
            body.addView(label(tr("수량", "Quantity"), 12));
            mCoupangQuantityInput = input("1", "1", InputType.TYPE_CLASS_NUMBER);
            body.addView(mCoupangQuantityInput);
            body.addView(label(tr("개당 최대 가격 (KRW)", "Maximum unit price (KRW)"), 12));
            mCoupangUnitCeilingInput = input("15000", "",
                    InputType.TYPE_CLASS_NUMBER);
            body.addView(mCoupangUnitCeilingInput);
            body.addView(label(tr("전체 최대 가격 (KRW)", "Maximum total price (KRW)"), 12));
            mCoupangTotalCeilingInput = input("15000", "",
                    InputType.TYPE_CLASS_NUMBER);
            body.addView(mCoupangTotalCeilingInput);
            body.addView(label(tr(
                    "선택 옵션 (선택 사항, 줄마다 정확한 그룹=값)",
                    "Options (optional, exact group=value on each line)"), 12));
            mCoupangOptionsInput = input(tr("예: 색상=블랙\n사이즈=270",
                            "Example: Color=Black\nSize=270"), "",
                    InputType.TYPE_CLASS_TEXT | InputType.TYPE_TEXT_FLAG_MULTI_LINE);
            mCoupangOptionsInput.setMinLines(2);
            body.addView(mCoupangOptionsInput);
            body.addView(label(tr(
                    "승인하면 위 상품·옵션·수량·가격 상한만 준비하고 바로구매를 한 번 활성화한 뒤 "
                            + "직접 조작으로 전환합니다. 주문서 검토, 로그인, 최종 주문과 결제는 직접 "
                            + "진행해야 합니다.",
                    "Approval covers only this offer, exact options, quantity, and price ceilings. "
                            + "The browser activates Buy now once, then returns control. You must "
                            + "review checkout and complete sign-in, the final order, and payment."), 12));
            Button approveCoupang = button(tr("이 범위로 승인하고 준비", "Approve and prepare"),
                    0xffffd76a, 0xff17200f);
            body.addView(approveCoupang);
            approveCoupang.setOnClickListener(v -> startApprovedCoupangPreparation());
        }

        mStartButton = button(mRunning ? tr("실행 중…", "Running…")
                        : tr("현재 탭에서 시작", "Start in current tab"),
                0xffbaff68, 0xff17200f);
        mStartButton.setEnabled(!mRunning);
        body.addView(mStartButton);
        mStartButton.setOnClickListener(v -> start(null));
        Button stop = button(tr("중지", "Stop"), 0xff34412d, Color.WHITE);
        body.addView(stop);
        stop.setOnClickListener(v -> stop(tr("작업을 중지했습니다.", "Run stopped.")));
        Button direct = button(tr("내가 직접 조작", "I’ll take control"),
                0xff283025, Color.WHITE);
        body.addView(direct);
        direct.setOnClickListener(v -> takeOver());
        mStatus = label(mLog, 14);
        mStatus.setTextIsSelectable(true);
        body.addView(mStatus);
        ScrollView scroll = new ScrollView(mActivity);
        scroll.addView(body);
        mPanel.setContentView(scroll);
        Window window = mPanel.getWindow();
        if (window != null) {
            window.setBackgroundDrawableResource(android.R.color.transparent);
            window.setGravity(Gravity.BOTTOM);
            window.addFlags(WindowManager.LayoutParams.FLAG_SECURE);
            window.setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE);
        }
        mPanel.show();
        if (window != null) {
            window.setLayout(ViewGroup.LayoutParams.MATCH_PARENT,
                    Math.round(mActivity.getResources().getDisplayMetrics().heightPixels * 0.86f));
        }
    }

    private void startApprovedCoupangPreparation() {
        if (mRunning || mDestroyed) return;
        try {
            start(buildApprovedCoupangPreparation());
        } catch (Exception e) {
            if (!mRunning) {
                mApprovedPreparation = null;
                mTaskContents = null;
                mTaskTab = null;
            }
            append(e.getMessage());
        }
    }

    private void start(JSONObject approvedPreparation) {
        if (mRunning || mDestroyed) return;
        try {
            mGoal = mGoalInput.getText().toString().trim();
            if (approvedPreparation != null && mGoal.isEmpty()) {
                mGoal = "Prepare the explicitly approved Coupang offer for checkout review";
            }
            mModel = mModelInput.getText().toString().trim();
            mApiKey = mApiKeyInput.getText().toString().trim();
            if (!mModel.matches("^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$")) {
                throw new Exception(tr("OpenAI 모델 ID를 확인하세요.",
                        "Check the OpenAI model ID."));
            }
            if (mGoal.isEmpty() || mGoal.length() > 4000) {
                throw new Exception(tr("할 일을 4,000자 이내로 입력하세요.",
                        "Describe the task in 4,000 characters or fewer."));
            }
            if (approvedPreparation == null && (!mApiKey.startsWith("sk-")
                    || mApiKey.length() > 1024 || mApiKey.matches(".*\\s.*"))) {
                throw new Exception(tr("OpenAI API 키를 확인하세요.",
                        "Check your OpenAI API key."));
            }
            new SecureRandom().nextBytes(mPermitKey);
            mTaskTab = mCurrentTab.get();
            if (mTaskTab == null || mTaskTab.isOffTheRecord()) {
                throw new Exception(tr("일반 탭에서 웹사이트를 먼저 여세요.",
                        "Open a website in a regular tab first."));
            }
            mTaskContents = mTaskTab.getWebContents();
            if (mTaskContents == null || currentOrigin() == null) {
                throw new Exception(tr("공개 HTTP(S) 웹사이트에서 시작하세요.",
                        "Start on a public HTTP(S) website."));
            }
            if (mTaskTab.isLoading()) {
                throw new Exception(tr("페이지 로딩이 끝난 뒤 다시 시작해 주세요.",
                        "Wait for the page to finish loading, then start again."));
            }
            if (approvedPreparation != null) {
                verifyLocalApprovedPreparation(approvedPreparation, true);
                mApprovedPreparation = new JSONObject(approvedPreparation.toString());
            } else {
                mApprovedPreparation = null;
            }
            mActivity.getPreferences(0).edit().putString("lane.openai.model", mModel).apply();
            // The API key deliberately remains only in process memory.
            mHistory = new JSONArray();
            mJournal.clear();
            mSteps = 0;
            mSequence = 1;
            mLeaseEpoch = 1;
            mDocumentEpoch = 1;
            mRunId = "run_" + UUID.randomUUID();
            mDocumentUrl = mTaskTab.getUrl().getSpec();
            mLastObservationId = "";
            mLastObservationOrigin = "";
            mGeneration++;
            mControlGeneration++;
            mRunning = true;
            mTaskTab.addObserver(mTabObserver);
            mDeadline = SystemClock.elapsedRealtime() + RUN_TIMEOUT_MS;
            mLog = approvedPreparation == null
                    ? tr("필터를 적용해 페이지 텍스트와 검색 후보를 확인하고 있습니다…",
                            "Applying the filter to page text and search candidates…")
                    : tr("승인한 쿠팡 상품·옵션·수량·가격 범위를 다시 확인하고 있습니다…",
                            "Rechecking the approved Coupang offer, options, quantity, and price ceilings…");
            mPanel.dismiss();
            updateNativeSurface();
            int generation = mGeneration;
            mUi.postDelayed(() -> step(generation), 120);
        } catch (Exception e) {
            if (!mRunning) {
                mApprovedPreparation = null;
                mTaskContents = null;
                mTaskTab = null;
            }
            append(e.getMessage());
        }
    }

    private boolean valid(int generation) {
        if (!mRunning || mDestroyed || generation != mGeneration) return false;
        if (SystemClock.elapsedRealtime() > mDeadline) {
            stop(tr("시간 제한에 도달했습니다. 결과를 확인하고 다시 시작하세요.",
                    "The run reached its time limit. Review the result and start again."));
            return false;
        }
        if (!mActivity.hasWindowFocus() && (mPanel == null || !mPanel.isShowing())) {
            stop(tr("브라우저가 전면에 있지 않아 작업을 중지했습니다.",
                    "The browser is not in the foreground, so the run stopped."));
            return false;
        }
        if (!mDocumentUrl.equals(mTaskTab.getUrl().getSpec())) {
            takeOver(tr("페이지 주소가 바뀌어 자동 실행을 중지했습니다. 새 주소를 확인한 뒤 "
                            + "다시 시작해 주세요.",
                    "The page URL changed, so automation stopped. Review the new URL and "
                            + "start again."));
            return false;
        }
        if (mCurrentTab.get() != mTaskTab || mTaskTab.getWebContents() != mTaskContents
                || mTaskTab.isOffTheRecord() || currentOrigin() == null) {
            stop(tr("탭 또는 페이지가 변경되어 작업을 중지했습니다.",
                    "The tab or page changed, so the run stopped."));
            return false;
        }
        return true;
    }

    private void append(String text) {
        mLog += "\n" + (text == null ? tr("작업을 진행할 수 없습니다.",
                "The task cannot continue.") : text);
        if (mLog.length() > 6000) mLog = mLog.substring(mLog.length() - 6000);
        if (mStatus != null) mStatus.setText(mLog);
    }

    private void updateNativeSurface() {
        if (!mRunning) {
            mControlBar.setVisibility(View.GONE);
            return;
        }
        mTrustedOrigin.setText(tr("브라우저 확인 주소 · ", "Browser-verified origin · ")
                + displayOrigin());
        if (mCurrentAction.getText().length() == 0) {
            mCurrentAction.setText(tr("현재 작업 · 페이지 확인", "Current action · Inspect page"));
        }
        mDataScope.setText(mApprovedPreparation == null
                ? tr("데이터 범위 · 공개 상품 텍스트/검색 후보 · 계정·장바구니·결제 화면 제외",
                        "Data scope · Public product text/search candidates · Account, cart, and "
                                + "payment pages excluded")
                : tr("승인 범위 · 현재 쿠팡 상품·정확한 옵션·수량·가격 상한 · 최종 주문/결제 제외",
                        "Approved scope · Current Coupang offer, exact options, quantity, and price "
                                + "ceilings · Final order/payment excluded"));
        mControlBar.setVisibility(View.VISIBLE);
    }

    private String displayOrigin() {
        String origin = currentOrigin();
        return origin == null ? tr("확인할 수 없음", "Unavailable") : origin;
    }

    private String currentOrigin() {
        Tab tab = mRunning ? mTaskTab : mCurrentTab.get();
        if (tab == null || tab.getWebContents() == null) return null;
        return publicOrigin(tab.getWebContents().getLastCommittedUrl().getSpec());
    }

    private CoupangProductIdentity currentCoupangProduct() {
        Tab tab = mTaskTab != null ? mTaskTab : mCurrentTab.get();
        if (tab == null || tab.getWebContents() == null) return null;
        return coupangProductIdentity(tab.getWebContents().getLastCommittedUrl().getSpec());
    }

    private static CoupangProductIdentity coupangProductIdentity(String value) {
        try {
            URI uri = new URI(value);
            if (!"https".equals(uri.getScheme()) || uri.getUserInfo() != null
                    || uri.getPort() != -1 || uri.getFragment() != null) {
                return null;
            }
            String origin = publicOrigin(value);
            String host = uri.getHost();
            if (origin == null || host == null || !COUPANG_PRODUCT_ORIGINS.contains(origin)) {
                return null;
            }
            String path = uri.getRawPath();
            if (path == null || !path.matches("^/vp/products/[1-9][0-9]{0,19}/?$")) {
                return null;
            }
            String productId = path.replaceFirst("^/vp/products/", "").replaceFirst("/$", "");
            String itemId = uniqueNumericQueryParameter(uri.getRawQuery(), "itemId");
            String vendorItemId = uniqueNumericQueryParameter(uri.getRawQuery(), "vendorItemId");
            if (itemId == null || vendorItemId == null) return null;
            return new CoupangProductIdentity(origin, path, productId, itemId, vendorItemId);
        } catch (Exception e) {
            return null;
        }
    }

    private static String uniqueNumericQueryParameter(String rawQuery, String name) {
        if (rawQuery == null || rawQuery.isEmpty()) return null;
        String result = null;
        for (String part : rawQuery.split("&", -1)) {
            int separator = part.indexOf('=');
            if (separator <= 0 || !name.equals(part.substring(0, separator))) continue;
            String value = part.substring(separator + 1);
            if (result != null || !value.matches("^[1-9][0-9]{0,19}$")) return null;
            result = value;
        }
        return result;
    }

    private JSONObject buildApprovedCoupangPreparation() throws Exception {
        CoupangProductIdentity identity = currentCoupangProduct();
        if (identity == null) {
            throw new Exception(tr(
                    "itemId와 vendorItemId가 있는 정확한 쿠팡 상품 페이지에서만 승인할 수 있습니다.",
                    "Approval is available only on an exact Coupang product URL with itemId and vendorItemId."));
        }
        long quantity = boundedDecimalInput(mCoupangQuantityInput, 1, 99,
                tr("수량은 1~99의 정수여야 합니다.", "Quantity must be an integer from 1 to 99."));
        long unitCeiling = boundedDecimalInput(mCoupangUnitCeilingInput, 1, MAX_WON,
                tr("개당 최대 가격을 1~1조 KRW로 입력하세요.",
                        "Enter a maximum unit price from 1 to 1 trillion KRW."));
        long totalCeiling = boundedDecimalInput(mCoupangTotalCeilingInput, 1, MAX_WON,
                tr("전체 최대 가격을 1~1조 KRW로 입력하세요.",
                        "Enter a maximum total price from 1 to 1 trillion KRW."));
        if (Math.multiplyExact(unitCeiling, quantity) > totalCeiling) {
            throw new Exception(tr("전체 최대 가격은 개당 최대 가격 × 수량 이상이어야 합니다.",
                    "The maximum total must be at least the maximum unit price times quantity."));
        }

        JSONObject options = approvedCoupangOptions(mCoupangOptionsInput.getText().toString());
        JSONObject offerIdentity = new JSONObject()
                .put("productId", identity.productId)
                .put("itemId", identity.itemId)
                .put("vendorItemId", identity.vendorItemId);
        JSONObject item = new JSONObject()
                .put("query", "Current Coupang product")
                .put("offerIdentity", offerIdentity)
                .put("quantity", quantity)
                .put("unitPriceCeilingKrw", unitCeiling)
                .put("linePriceCeilingKrw", totalCeiling)
                .put("productUrl", identity.canonicalProductUrl())
                .put("options", options);
        JSONArray items = new JSONArray().put(item);
        String expiry = formatTimestamp(System.currentTimeMillis() + 5 * 60_000L);
        JSONObject approvalWithoutDigest = new JSONObject()
                .put("approvalId", "approval_" + UUID.randomUUID().toString().replace("-", ""))
                .put("currency", "KRW")
                .put("revision", 1)
                .put("expiresAt", expiry)
                .put("totalPriceCeilingKrw", totalCeiling);
        JSONObject digestMaterial = new JSONObject()
                .put("merchantId", "COUPANG")
                .put("recipeVersion", "1")
                .put("mode", "single")
                .put("route", "single_buy_now")
                .put("approval", approvalWithoutDigest)
                .put("items", items);
        JSONObject approval = new JSONObject(approvalWithoutDigest.toString())
                .put("approvalDigest", "sha256:" + hex(sha256(canonicalize(digestMaterial))));
        return new JSONObject()
                .put("merchantId", "COUPANG")
                .put("recipeVersion", "1")
                .put("mode", "single")
                .put("approval", approval)
                .put("items", new JSONArray(items.toString()))
                .put("cursor", 2)
                .put("expectedPage", new JSONObject()
                        .put("origin", identity.origin)
                        .put("pathname", identity.path));
    }

    private long boundedDecimalInput(EditText input, long minimum, long maximum, String message)
            throws Exception {
        if (input == null) throw new Exception(message);
        String value = input.getText().toString().trim();
        if (!value.matches("^[0-9]{1,13}$")) throw new Exception(message);
        long result;
        try {
            result = Long.parseLong(value);
        } catch (NumberFormatException e) {
            throw new Exception(message);
        }
        if (result < minimum || result > maximum) throw new Exception(message);
        return result;
    }

    private JSONObject approvedCoupangOptions(String raw) throws Exception {
        String value = raw == null ? "" : raw.trim();
        if (value.isEmpty()) return new JSONObject().put("kind", "none");
        JSONArray choices = new JSONArray();
        Set<String> seenGroups = new HashSet<>();
        for (String line : value.split("\\r?\\n", -1)) {
            if (choices.length() >= 10 || line.indexOf('=') <= 0
                    || line.indexOf('=') != line.lastIndexOf('=')) {
                throw new Exception(tr("옵션은 최대 10개이며 줄마다 정확히 그룹=값으로 입력하세요.",
                        "Enter at most 10 options, with exactly one group=value pair per line."));
            }
            int separator = line.indexOf('=');
            String groupName = normalizedPublicText(line.substring(0, separator), 120);
            String valueName = normalizedPublicText(line.substring(separator + 1), 120);
            if (groupName == null || valueName == null || likelySensitive(groupName)
                    || likelySensitive(valueName) || !seenGroups.add(groupName)) {
                throw new Exception(tr("각 옵션 그룹에는 공개 텍스트 값 하나만 입력하세요.",
                        "Enter exactly one public-text value for each option group."));
            }
            choices.put(new JSONObject().put("groupName", groupName).put("valueName", valueName));
        }
        return new JSONObject().put("kind", "choices").put("choices", choices);
    }

    private static String formatTimestamp(long milliseconds) {
        SimpleDateFormat format = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.US);
        format.setTimeZone(TimeZone.getTimeZone("UTC"));
        return format.format(new Date(milliseconds));
    }

    private static String publicOrigin(String value) {
        try {
            URI uri = new URI(value);
            String scheme = uri.getScheme() == null ? "" : uri.getScheme().toLowerCase(Locale.US);
            String host = uri.getHost();
            if (!("https".equals(scheme) || "http".equals(scheme)) || host == null
                    || uri.getUserInfo() != null || uri.getPort() != -1 || !isPublicHost(host)) {
                return null;
            }
            String normalizedHost = host.toLowerCase(Locale.US);
            if (normalizedHost.indexOf(':') >= 0) normalizedHost = "[" + normalizedHost + "]";
            return scheme + "://" + normalizedHost;
        } catch (Exception e) {
            return null;
        }
    }

    private static boolean isPublicHost(String value) {
        String host = value.toLowerCase(Locale.US);
        if (host.isEmpty() || "localhost".equals(host) || host.endsWith(".localhost")
                || host.endsWith(".local") || host.endsWith(".internal")
                || host.endsWith(".lan") || host.endsWith(".home.arpa")
                || host.endsWith(".test") || host.endsWith(".invalid")
                || host.endsWith(".example")) {
            return false;
        }
        if (host.indexOf(':') >= 0) {
            return !("::".equals(host) || "::1".equals(host) || host.startsWith("fc")
                    || host.startsWith("fd") || host.startsWith("::ffff:")
                    || host.matches("^fe[89ab].*"));
        }
        String[] parts = host.split("\\.");
        if (parts.length == 4) {
            try {
                int[] bytes = new int[4];
                for (int i = 0; i < 4; i++) {
                    bytes[i] = Integer.parseInt(parts[i]);
                    if (bytes[i] < 0 || bytes[i] > 255) return false;
                }
                int a = bytes[0];
                int b = bytes[1];
                return !(a == 0 || a == 10 || a == 127 || a >= 224
                        || (a == 100 && b >= 64 && b <= 127)
                        || (a == 169 && b == 254)
                        || (a == 172 && b >= 16 && b <= 31)
                        || (a == 192 && (b == 0 || b == 168))
                        || (a == 198 && (b == 18 || b == 19 || b == 51))
                        || (a == 203 && b == 0));
            } catch (NumberFormatException e) {
                return false;
            }
        }
        return host.indexOf('.') > 0;
    }

    private JSONObject nativeMetadata() throws Exception {
        String origin = currentOrigin();
        if (origin == null) throw new Exception("ORIGIN_POLICY_DENIED");
        URI current = new URI(mTaskTab.getUrl().getSpec());
        String pathname = current.getRawPath();
        if (pathname == null || pathname.isEmpty()) pathname = "/";
        if (!pathname.startsWith("/") || pathname.indexOf('?') >= 0
                || pathname.indexOf('#') >= 0 || pathname.length() > 1024) {
            throw new Exception("URL_POLICY_DENIED");
        }
        return new JSONObject()
                .put("tabId", "tab_" + mTaskTab.getId())
                .put("frameId", FRAME_ID)
                .put("documentEpoch", mDocumentEpoch)
                .put("topOrigin", origin)
                .put("frameOrigin", origin)
                .put("pathname", pathname)
                .put("queryOrFragmentPresent",
                        current.getRawQuery() != null || current.getRawFragment() != null)
                .put("foreground", true);
    }

    private void evaluateAgentCall(
            int generation, String fixedExpression, Consumer<JSONObject> callback) {
        if (!valid(generation)) return;
        final boolean[] completed = {false};
        Runnable timeout = () -> {
            if (!completed[0] && valid(generation)) {
                stop(tr("페이지 응답이 늦어 작업을 중지했습니다.",
                        "The page response timed out, so the run stopped."));
            }
        };
        mUi.postDelayed(timeout, 8000);
        String script = LanePageScript.SOURCE + "\n(() => { try { return {ok:true,value:("
                + fixedExpression
                + ")}; } catch(e) { return {ok:false,error:String(e.message).slice(0,120)}; } })()";
        mTaskContents.evaluateVitlaneAgentJavaScript(script, json -> {
                    completed[0] = true;
                    mUi.removeCallbacks(timeout);
                    if (!valid(generation)) return;
                    try {
                        JSONObject envelope = new JSONObject(json);
                        if (!envelope.optBoolean("ok")) {
                            stop(tr("페이지가 바뀌었거나 조작할 수 없습니다.",
                                    "The page changed or cannot be controlled."));
                            return;
                        }
                        callback.accept(envelope.getJSONObject("value"));
                    } catch (Exception e) {
                        stop(tr("페이지 응답을 읽을 수 없습니다.",
                                "The page response could not be read."));
                    }
                });
    }

    private void step(int generation) {
        if (!valid(generation)) return;
        if (mSteps >= MAX_STEPS) {
            stop(tr("20단계까지 실행했습니다. 결과를 확인하고 이어서 요청하세요.",
                    "The run reached 20 steps. Review the result before continuing."));
            return;
        }
        if (mTaskTab.isLoading()) {
            mUi.postDelayed(() -> step(generation), 400);
            return;
        }
        try {
            JSONObject metadata = nativeMetadata();
            String expression = "globalThis.__laneAgent.snapshot(" + metadata + ")";
            evaluateAgentCall(generation, expression, observation -> sendStep(generation, observation));
        } catch (Exception e) {
            stop(tr("현재 페이지를 안전하게 확인할 수 없습니다.",
                    "The current page cannot be inspected safely."));
        }
    }

    private void sendStep(int generation, JSONObject observation) {
        try {
            JSONObject metadata = observation.getJSONObject("nativeMetadata");
            if (!metadataMatchesCurrent(metadata)) {
                stop(tr("페이지가 바뀌어 새로 확인해야 합니다.",
                        "The page changed and must be observed again."));
                return;
            }
            mLastObservationId = requiredId(observation, "observationId");
            mLastObservationOrigin = metadata.getString("topOrigin");
            JSONObject context = new JSONObject()
                    .put("runId", mRunId)
                    .put("deviceId", mDeviceId)
                    .put("profileRef", mProfileRef)
                    .put("sequence", mSequence)
                    .put("leaseEpoch", mLeaseEpoch)
                    .put("controlGeneration", mControlGeneration);
            JSONObject request = new JSONObject()
                    .put("protocolVersion", PROTOCOL_VERSION)
                    .put("goal", mGoal)
                    .put("observation", observation)
                    .put("history", mHistory)
                    .put("commandContext", context);
            if (mApprovedPreparation != null) {
                verifyLocalApprovedPreparation(mApprovedPreparation, true);
                request.put("approvedPreparation",
                        new JSONObject(mApprovedPreparation.toString()));
            }
            final JSONObject stepRequest = new JSONObject(request.toString());
            final String model = mModel;
            final String apiKey = mApiKey;
            mSteps++;
            mCurrentAction.setText(tr("현재 작업 · 다음 안전한 동작 결정",
                    "Current action · Choosing the next safe action"));
            append(mSteps + " · " + tr("다음 동작을 확인하고 있습니다…",
                    "Checking the next action…"));
            if (mApprovedPreparation != null) {
                // The purchase recipe comes exclusively from the user's native approval.
                // It is neither proposed by nor sent to the model.
                handle(generation, authorizeLocalAction(approvedCoupangAction(observation)));
                return;
            }
            mNetwork.execute(() -> {
                try {
                    if (generation != mGeneration) return;
                    JSONObject action = LaneOpenAiPlanner.plan(apiKey, model, stepRequest,
                            connection -> {
                                mConnection = connection;
                                if (connection != null && generation != mGeneration) {
                                    connection.disconnect();
                                    throw new IllegalStateException("Cancelled");
                                }
                            });
                    mUi.post(() -> {
                        if (!valid(generation)) return;
                        try {
                            handle(generation, authorizeLocalAction(action));
                        } catch (Exception e) {
                            stop(tr("현재 페이지와 승인 범위에 맞지 않는 동작입니다.",
                                    "The action does not match this page and the approved scope."));
                        }
                    });
                } catch (Exception e) {
                    mUi.post(() -> {
                        if (valid(generation)) {
                            stop(tr("OpenAI 연결에 실패했습니다. 인터넷, API 키, 사용량과 모델을 확인하세요.",
                                    "OpenAI connection failed. Check your network, API key, usage, and model."));
                        }
                    });
                }
            });
        } catch (Exception e) {
            stop(tr("요청을 만들 수 없습니다.", "The request could not be created."));
        }
    }

    private JSONObject authorizeLocalAction(JSONObject action) throws Exception {
        // Validate before signing. The model never supplies authority or a page identity.
        verifyAllowedAction(action, true);
        JSONObject command = new JSONObject()
                .put("protocolVersion", PROTOCOL_VERSION)
                .put("commandId", "command_" + UUID.randomUUID())
                .put("runId", mRunId).put("deviceId", mDeviceId)
                .put("profileRef", mProfileRef).put("sequence", mSequence)
                .put("leaseEpoch", mLeaseEpoch).put("controlGeneration", mControlGeneration)
                .put("expiresAt", formatTimestamp(System.currentTimeMillis() + 30_000))
                .put("actionHash", hex(sha256(canonicalize(action)))).put("action", action);
        Mac mac = Mac.getInstance("HmacSHA256");
        mac.init(new SecretKeySpec(mPermitKey, "HmacSHA256"));
        String permit = "hmac-sha256:" + Base64.encodeToString(
                mac.doFinal(canonicalize(command).getBytes(StandardCharsets.UTF_8)),
                Base64.URL_SAFE | Base64.NO_WRAP | Base64.NO_PADDING);
        command.put("serverPermit", permit); // Protocol v1 name; issuer is native in this mode.
        return new JSONObject().put("authorizedCommand", command);
    }

    private JSONObject approvedCoupangAction(JSONObject observation) throws Exception {
        verifyLocalApprovedPreparation(mApprovedPreparation, true);
        if (!"sanitized".equals(observation.getJSONObject("privacy")
                .getString("collectionStatus"))) {
            throw new Exception("SENSITIVE_PAGE_REQUIRES_HANDOFF");
        }
        JSONObject snapshot = approvedSnapshotFromLocal(mApprovedPreparation);
        JSONObject item = snapshot.getJSONArray("items").getJSONObject(0);
        JSONObject expectedPage = mApprovedPreparation.getJSONObject("expectedPage");
        JSONObject bindings = lineFromItem(item)
                .put("approvedPreparation", snapshot)
                .put("approval", new JSONObject(snapshot.getJSONObject("approval").toString()))
                .put("targetLineIndex", 0)
                .put("expectedOrigin", expectedPage.getString("origin"))
                .put("expectedPath", expectedPage.getString("pathname"));
        String step = expectedLocalCoupangStep(mApprovedPreparation);
        if ("select_option".equals(step)) {
            int optionIndex = mApprovedPreparation.getInt("cursor") - 2;
            JSONObject choice = item.getJSONObject("options")
                    .getJSONArray("choices").getJSONObject(optionIndex);
            bindings.put("targetOptionIndex", optionIndex)
                    .put("groupName", choice.getString("groupName"))
                    .put("valueName", choice.getString("valueName"));
        }
        JSONObject page = new JSONObject(observation.getJSONObject("nativeMetadata").toString());
        page.remove("foreground");
        page.put("observationId", observation.getString("observationId"));
        return new JSONObject().put("kind", "run_preparation_step").put("page", page)
                .put("adapterId", COUPANG_ADAPTER).put("recipeVersion", "1")
                .put("stepId", step).put("bindings", bindings)
                .put("reason", "Execute only the native user-approved purchase preparation step");
    }

    private void handle(int generation, JSONObject response) {
        try {
            if (!hasOnlyKeys(response, "authorizedCommand")) throw new Exception("INVALID_RESPONSE");
            JSONObject command = response.getJSONObject("authorizedCommand");
            JSONObject action = command.getJSONObject("action");
            String commandId = requiredId(command, "commandId");
            String actionHash = requiredHash(command, "actionHash");
            long sequence = requiredInteger(command, "sequence", 1);
            String kind = requiredId(action, "kind");
            String preparationAdapterId = "run_preparation_step".equals(kind)
                    ? requiredString(action, "adapterId", 80) : "";
            String preparationStepId = "run_preparation_step".equals(kind)
                    ? requiredString(action, "stepId", 80) : "";
            JournalEntry existing = mJournal.get(commandId);
            if (existing != null && !existing.actionHash.equals(actionHash)) {
                stop(tr("같은 명령 ID에 다른 내용이 도착해 실행을 차단했습니다.",
                        "A command ID was reused with different content, so execution was blocked."));
                return;
            }
            JournalEntry received = existing;
            if (received == null) {
                received = new JournalEntry(commandId, actionHash, sequence, kind,
                        preparationAdapterId, preparationStepId);
                mJournal.put(commandId, received);
                appendJournal(received, "RECEIVED");
            }
            long expectedSequence = existing == null ? mSequence : received.sequence;
            verifyAuthorizedCommand(command, expectedSequence, existing != null);
            if (existing != null) {
                if (existing.isTerminal()) {
                    append(tr("이미 처리한 명령의 기존 결과를 사용했습니다.",
                            "Used the recorded outcome for an already processed command."));
                    return;
                }
                if ("STARTED".equals(existing.state)) {
                    finishJournal(existing, "OUTCOME_UNKNOWN", "outcome_unknown",
                            "REPLAY_AFTER_START");
                    stop(tr("실행 도중 같은 명령이 다시 도착해 결과를 알 수 없습니다.",
                            "The command was replayed after execution started, so its outcome is unknown."));
                    return;
                }
            }
            JournalEntry entry = received;
            entry.state = "VALIDATED";
            if ("finish".equals(kind) || "request_human".equals(kind)) {
                entry.displayMessage = sanitizeTranscript(action.getString("message"));
            }
            appendJournal(entry, "VALIDATED");
            mCurrentAction.setText(tr("현재 작업 · ", "Current action · ")
                    + actionLabel(kind));
            String commandJson = command.toString();
            evaluateAgentCall(generation,
                    "globalThis.__laneAgent.inspect(" + commandJson + ")", check -> {
                        if (!commandId.equals(check.optString("commandId"))
                                || !check.optBoolean("allowed")) {
                            finishJournal(entry, "OUTCOME_UNKNOWN", "rejected",
                                    check.optString("code", "LOCAL_POLICY_DENIED"));
                            stop(tr("현재 페이지 상태와 명령이 달라 실행을 차단했습니다.",
                                    "The command no longer matches the page, so execution was blocked."));
                            return;
                        }
                        if (!valid(generation)) return;
                        entry.state = "STARTED";
                        appendJournal(entry, "STARTED");
                        execute(generation, commandJson, entry);
                    });
        } catch (Exception e) {
            stop(tr("서명되었거나 현재 실행에 연결된 올바른 명령이 아닙니다.",
                    "The command is not validly signed or bound to this run."));
        }
    }

    private void execute(int generation, String commandJson, JournalEntry entry) {
        evaluateAgentCall(generation,
                "globalThis.__laneAgent.execute(" + commandJson + ")", result -> {
                    try {
                        if (!entry.commandId.equals(result.getString("commandId"))) {
                            throw new Exception("COMMAND_RESULT_MISMATCH");
                        }
                        String status = result.getString("status");
                        String code = requiredId(result, "code");
                        if (!setOf("applied", "outcome_unknown", "handoff", "rejected")
                                .contains(status)) {
                            throw new Exception("INVALID_RESULT");
                        }
                        String journalState = ("outcome_unknown".equals(status)
                                || "rejected".equals(status)) ? "OUTCOME_UNKNOWN" : "APPLIED";
                        finishJournal(entry, journalState, status, code);
                        scheduleNextFrom(entry, generation);
                    } catch (Exception e) {
                        finishJournal(entry, "OUTCOME_UNKNOWN", "outcome_unknown",
                                "INVALID_EXECUTION_RESULT");
                        stop(tr("동작 결과를 확인할 수 없어 자동 실행을 중지했습니다.",
                                "The action outcome could not be verified, so automation stopped."));
                    }
                });
    }

    private void scheduleNextFrom(JournalEntry entry, int generation) {
        if (!entry.isTerminal()) return;
        if (entry.sequence >= mSequence) mSequence = entry.sequence + 1;
        if ("outcome_unknown".equals(entry.resultStatus)) {
            stop(tr("동작 결과를 알 수 없습니다. 페이지를 직접 확인해 주세요.",
                    "The action outcome is unknown. Please inspect the page directly."));
            return;
        }
        if ("handoff".equals(entry.resultStatus)) {
            appendUntrustedMessage(entry);
            if (COUPANG_ADAPTER.equals(entry.preparationAdapterId)
                    && COUPANG_ACTIVATION_HANDOFF_STEPS.contains(entry.preparationStepId)) {
                takeOver(tr(
                        "승인한 구매 준비 동작을 한 번 실행했습니다. 주문서와 최종 가격을 확인하고 "
                                + "로그인·최종 주문·결제는 직접 진행해 주세요.",
                        "The approved preparation action ran once. Review checkout and the final "
                                + "price, then complete sign-in, the final order, and payment yourself."));
            } else if ("open_candidate".equals(entry.actionKind)) {
                takeOver(tr("보안을 위해 링크 자동 열기를 중지했습니다. 링크를 직접 열어 주세요.",
                        "Automatic link opening is disabled for safety. Please open the link directly."));
            } else {
                takeOver(tr("로그인과 인증은 직접 완료하고 공개 상품 페이지로 돌아온 뒤 다시 "
                                + "시작해 주세요. 장바구니·결제·최종 결정은 계속 직접 진행해야 합니다.",
                        "Complete sign-in and verification yourself, return to the public product "
                                + "page, and start again. Cart, payment, and final decisions remain "
                                + "under your direct control."));
            }
            return;
        }
        if ("finish".equals(entry.actionKind)) {
            appendUntrustedMessage(entry);
            stop(tr("AI 응답을 받았습니다. 완료 여부와 근거를 직접 확인해 주세요.",
                    "An AI response was received. Review the evidence and completion status yourself."));
            return;
        }
        if ("rejected".equals(entry.resultStatus)) {
            stop(tr("현재 페이지에서 이 동작은 허용되지 않습니다.",
                    "This action is not allowed on the current page."));
            return;
        }
        if (COUPANG_ADAPTER.equals(entry.preparationAdapterId)
                && COUPANG_ACTIVATION_HANDOFF_STEPS.contains(entry.preparationStepId)) {
            takeOver(tr(
                    "승인한 구매 준비 동작을 한 번 실행했습니다. 주문서와 최종 가격을 확인하고 "
                            + "로그인·최종 주문·결제는 직접 진행해 주세요.",
                    "The approved preparation action ran once. Review checkout and the final price, "
                            + "then complete sign-in, the final order, and payment yourself."));
            return;
        }
        if (COUPANG_ADAPTER.equals(entry.preparationAdapterId)
                && COUPANG_CONTINUING_STEPS.contains(entry.preparationStepId)) {
            try {
                advanceApprovedPreparationCursor();
            } catch (Exception e) {
                stop(tr("승인된 구매 준비 순서를 이어갈 수 없어 중지했습니다.",
                        "The approved preparation cursor could not advance, so the run stopped."));
                return;
            }
        }
        mUi.postDelayed(() -> step(generation), 700);
    }

    private String actionLabel(String kind) {
        switch (kind) {
            case "inspect_page":
                return tr("필터 적용 페이지 확인", "Inspecting filtered page data");
            case "open_candidate":
                return tr("링크 직접 열기 요청", "Requesting direct link open");
            case "scroll":
                return tr("페이지 스크롤", "Scrolling page");
            case "run_preparation_step":
                return mApprovedPreparation == null
                        ? tr("공개 검색어 준비", "Preparing public search text")
                        : tr("승인된 쿠팡 구매 준비", "Preparing the approved Coupang offer");
            case "request_human":
                return tr("직접 조작 요청", "Requesting direct control");
            case "finish":
                return tr("AI 응답 검토", "Reviewing AI response");
            default:
                return tr("안전 정책 확인", "Checking safety policy");
        }
    }

    private void appendUntrustedMessage(JournalEntry entry) {
        if (entry.displayMessage.isEmpty()) return;
        append(tr("AI 응답 · 신뢰하지 않는 한 줄 텍스트 · 결과 증거 아님 · ",
                       "AI response · untrusted single-line text · not result evidence · ")
                + entry.displayMessage);
    }

    private static String sanitizeTranscript(String value) {
        StringBuilder output = new StringBuilder();
        boolean pendingSpace = false;
        for (int offset = 0; offset < value.length() && output.length() < 320; ) {
            int codePoint = value.codePointAt(offset);
            offset += Character.charCount(codePoint);
            boolean unsafe = Character.isISOControl(codePoint)
                    || Character.getType(codePoint) == Character.FORMAT
                    || (codePoint >= 0x202a && codePoint <= 0x202e)
                    || (codePoint >= 0x2066 && codePoint <= 0x2069);
            if (unsafe || Character.isWhitespace(codePoint)) {
                pendingSpace = output.length() > 0;
                continue;
            }
            if (pendingSpace && output.length() < 320) output.append(' ');
            pendingSpace = false;
            if (output.length() + Character.charCount(codePoint) > 320) break;
            output.appendCodePoint(codePoint);
        }
        return output.toString().trim();
    }

    private void appendJournal(JournalEntry entry, String state) {
        append("#" + entry.sequence + " · " + entry.actionKind + " · " + state);
    }

    private void finishJournal(
            JournalEntry entry, String state, String resultStatus, String resultCode) {
        if (entry.isTerminal()) return;
        entry.state = state;
        entry.resultStatus = resultStatus;
        entry.resultCode = resultCode;
        appendJournal(entry, state);
        try {
            mHistory.put(new JSONObject()
                    .put("sequence", entry.sequence)
                    .put("actionKind", entry.actionKind)
                    .put("status", resultStatus)
                    .put("code", resultCode));
        } catch (Exception e) {
            entry.state = "OUTCOME_UNKNOWN";
            entry.resultStatus = "outcome_unknown";
            entry.resultCode = "JOURNAL_WRITE_FAILED";
        }
        while (mHistory.length() > 20) {
            JSONArray trimmed = new JSONArray();
            for (int i = 1; i < mHistory.length(); i++) trimmed.put(mHistory.opt(i));
            mHistory = trimmed;
        }
    }

    private void verifyAuthorizedCommand(
            JSONObject command, long expectedSequence, boolean replay) throws Exception {
        if (!hasOnlyKeys(command, "protocolVersion", "commandId", "runId", "deviceId",
                    "profileRef", "sequence", "leaseEpoch", "controlGeneration", "expiresAt",
                    "actionHash", "action", "serverPermit")
                || requiredInteger(command, "protocolVersion", 1) != PROTOCOL_VERSION) {
            throw new Exception("INVALID_COMMAND");
        }
        String commandId = requiredId(command, "commandId");
        if (!mRunId.equals(requiredId(command, "runId"))
                || !mDeviceId.equals(requiredId(command, "deviceId"))
                || !mProfileRef.equals(requiredId(command, "profileRef"))
                || requiredInteger(command, "sequence", 1) != expectedSequence
                || requiredInteger(command, "leaseEpoch", 1) != mLeaseEpoch
                || requiredInteger(command, "controlGeneration", 0) != mControlGeneration) {
            throw new Exception("COMMAND_BINDING_MISMATCH");
        }
        long expiration = parseExpiry(requiredString(command, "expiresAt", 80));
        long now = System.currentTimeMillis();
        if (expiration <= now || expiration - now > 60_000) {
            throw new Exception(expiration <= now ? "COMMAND_EXPIRED" : "INVALID_COMMAND_EXPIRY");
        }
        JSONObject action = command.getJSONObject("action");
        verifyAllowedAction(action, !replay);
        String actionHash = requiredHash(command, "actionHash");
        if (!constantTimeEquals(hex(sha256(canonicalize(action))), actionHash)) {
            throw new Exception("ACTION_HASH_MISMATCH");
        }
        String permit = requiredString(command, "serverPermit", 256);
        if (!permit.matches("^hmac-sha256:[A-Za-z0-9_-]{43}$")) {
            throw new Exception("INVALID_SERVER_PERMIT");
        }
        JSONObject unsigned = new JSONObject()
                .put("protocolVersion", PROTOCOL_VERSION)
                .put("commandId", commandId)
                .put("runId", command.getString("runId"))
                .put("deviceId", command.getString("deviceId"))
                .put("profileRef", command.getString("profileRef"))
                .put("sequence", command.getLong("sequence"))
                .put("leaseEpoch", command.getLong("leaseEpoch"))
                .put("controlGeneration", command.getLong("controlGeneration"))
                .put("expiresAt", command.getString("expiresAt"))
                .put("actionHash", actionHash)
                .put("action", action);
        Mac mac = Mac.getInstance("HmacSHA256");
        mac.init(new SecretKeySpec(mPermitKey, "HmacSHA256"));
        String expected = "hmac-sha256:" + Base64.encodeToString(
                mac.doFinal(canonicalize(unsigned).getBytes(StandardCharsets.UTF_8)),
                Base64.URL_SAFE | Base64.NO_WRAP | Base64.NO_PADDING);
        if (!constantTimeEquals(expected, permit)) throw new Exception("INVALID_SERVER_PERMIT");
    }

    private void verifyAllowedAction(JSONObject action, boolean requireCurrentPage) throws Exception {
        String kind = requiredId(action, "kind");
        if (!ACTION_KINDS.contains(kind)) throw new Exception("POLICY_DENIED");
        JSONObject page = action.getJSONObject("page");
        if (!hasOnlyKeys(page, "tabId", "frameId", "documentEpoch", "observationId",
                    "topOrigin", "frameOrigin", "pathname", "queryOrFragmentPresent")) {
            throw new Exception("REOBSERVE_REQUIRED");
        }
        String tabId = requiredId(page, "tabId");
        String frameId = requiredId(page, "frameId");
        long documentEpoch = requiredInteger(page, "documentEpoch", 0);
        String observationId = requiredId(page, "observationId");
        String topOrigin = requiredString(page, "topOrigin", 512);
        String frameOrigin = requiredString(page, "frameOrigin", 512);
        String pathname = requiredString(page, "pathname", 1024);
        Object queryOrFragmentRaw = page.get("queryOrFragmentPresent");
        if (!(queryOrFragmentRaw instanceof Boolean) || !pathname.startsWith("/")
                || pathname.indexOf('?') >= 0 || pathname.indexOf('#') >= 0) {
            throw new Exception("URL_POLICY_DENIED");
        }
        boolean queryOrFragmentPresent = (Boolean) queryOrFragmentRaw;
        if (!topOrigin.equals(publicOrigin(topOrigin + "/"))
                || !frameOrigin.equals(publicOrigin(frameOrigin + "/"))) {
            throw new Exception("ORIGIN_POLICY_DENIED");
        }
        JSONObject currentMetadata = requireCurrentPage ? nativeMetadata() : null;
        if (requireCurrentPage
                && (!("tab_" + mTaskTab.getId()).equals(tabId)
                        || !FRAME_ID.equals(frameId)
                        || documentEpoch != mDocumentEpoch
                        || !mLastObservationId.equals(observationId)
                        || !mLastObservationOrigin.equals(topOrigin)
                        || !mLastObservationOrigin.equals(frameOrigin)
                        || !mLastObservationOrigin.equals(currentOrigin())
                        || !pathname.equals(currentMetadata.getString("pathname"))
                        || queryOrFragmentPresent
                                != currentMetadata.getBoolean("queryOrFragmentPresent"))) {
            throw new Exception("REOBSERVE_REQUIRED");
        }
        requiredString(action, "reason", 500);
        switch (kind) {
            case "inspect_page":
                requireOnlyActionKeys(action, "kind", "page", "reason");
                return;
            case "open_candidate":
                requireOnlyActionKeys(action, "kind", "page", "candidateRef", "reason");
                requiredId(action, "candidateRef");
                return;
            case "scroll":
                requireOnlyActionKeys(action, "kind", "page", "direction", "reason");
                String direction = requiredString(action, "direction", 8);
                if (!("up".equals(direction) || "down".equals(direction))) {
                    throw new Exception("POLICY_DENIED");
                }
                return;
            case "run_preparation_step":
                requireOnlyActionKeys(action, "kind", "page", "adapterId", "recipeVersion",
                        "stepId", "bindings", "reason");
                String adapterId = requiredString(action, "adapterId", 80);
                if (COUPANG_ADAPTER.equals(adapterId)) {
                    verifyCoupangPreparationAction(action);
                    return;
                }
                if (!"builtin.public-search".equals(adapterId)
                        || !"1".equals(requiredString(action, "recipeVersion", 16))
                        || !"prepare_query".equals(requiredString(action, "stepId", 80))) {
                    throw new Exception("POLICY_DENIED");
                }
                JSONObject bindings = action.getJSONObject("bindings");
                if (!hasOnlyKeys(bindings, "candidateRef", "query")) {
                    throw new Exception("POLICY_DENIED");
                }
                requiredId(bindings, "candidateRef");
                String query = requiredString(bindings, "query", 160);
                if (!query.equals(query.trim()) || likelySensitive(query)) {
                    throw new Exception("POLICY_DENIED");
                }
                return;
            case "request_human":
                requireOnlyActionKeys(action, "kind", "page", "reasonCode", "message", "reason");
                if (!HUMAN_REASON_CODES.contains(requiredId(action, "reasonCode"))) {
                    throw new Exception("POLICY_DENIED");
                }
                requiredString(action, "message", 1000);
                return;
            case "finish":
                requireOnlyActionKeys(action, "kind", "page", "message", "reason");
                requiredString(action, "message", 4000);
                return;
            default:
                throw new Exception("POLICY_DENIED");
        }
    }

    private void verifyCoupangPreparationAction(JSONObject action) throws Exception {
        if (mApprovedPreparation == null
                || !"1".equals(requiredString(action, "recipeVersion", 16))) {
            throw new Exception("APPROVAL_REQUIRED");
        }
        String stepId = requiredString(action, "stepId", 80);
        if (!COUPANG_PREPARATION_STEPS.contains(stepId)) {
            throw new Exception("POLICY_DENIED");
        }
        verifyLocalApprovedPreparation(mApprovedPreparation, true);
        String expectedStep = expectedLocalCoupangStep(mApprovedPreparation);
        if (!stepId.equals(expectedStep)) throw new Exception("APPROVAL_STEP_MISMATCH");

        JSONObject bindings = action.getJSONObject("bindings");
        JSONObject actionApproval = bindings.getJSONObject("approval");
        JSONObject snapshot = bindings.getJSONObject("approvedPreparation");
        long now = System.currentTimeMillis();
        verifyCoupangSnapshot(snapshot, actionApproval, now);
        JSONObject expectedSnapshot = approvedSnapshotFromLocal(mApprovedPreparation);
        if (!sameCanonicalJson(snapshot, expectedSnapshot)
                || !sameCanonicalJson(actionApproval,
                        mApprovedPreparation.getJSONObject("approval"))) {
            throw new Exception("APPROVAL_BINDING_MISMATCH");
        }

        JSONObject approvedPage = mApprovedPreparation.getJSONObject("expectedPage");
        String expectedOrigin = requiredString(bindings, "expectedOrigin", 512);
        String expectedPath = requiredString(bindings, "expectedPath", 1024);
        if (!expectedOrigin.equals(approvedPage.getString("origin"))
                || !expectedPath.equals(approvedPage.getString("pathname"))) {
            throw new Exception("APPROVAL_PAGE_MISMATCH");
        }

        if ("start_checkout".equals(stepId)) {
            if (!hasOnlyKeys(bindings, "approvedPreparation", "approval", "approvedLines",
                        "expectedOrigin", "expectedPath")
                    || !"multi".equals(snapshot.getString("mode"))) {
                throw new Exception("POLICY_DENIED");
            }
            if (!"https://cart.coupang.com".equals(expectedOrigin)
                    || !"/cartView.pang".equals(expectedPath)) {
                throw new Exception("APPROVAL_PAGE_MISMATCH");
            }
            JSONArray approvedLines = bindings.getJSONArray("approvedLines");
            JSONArray items = snapshot.getJSONArray("items");
            if (approvedLines.length() != items.length() || approvedLines.length() < 2
                    || approvedLines.length() > 20) {
                throw new Exception("APPROVAL_BINDING_MISMATCH");
            }
            for (int i = 0; i < approvedLines.length(); i++) {
                JSONObject line = verifiedCoupangLine(approvedLines.getJSONObject(i), true);
                if (!sameCanonicalJson(line, lineFromItem(items.getJSONObject(i)))) {
                    throw new Exception("APPROVAL_BINDING_MISMATCH");
                }
            }
            return;
        }

        String[] baseKeys = {"approvedPreparation", "approval", "targetLineIndex",
                "productId", "itemId", "vendorItemId", "quantity", "unitPriceCeilingKrw",
                "linePriceCeilingKrw", "expectedOrigin", "expectedPath"};
        if ("select_option".equals(stepId)) {
            if (!hasOnlyKeys(bindings, "approvedPreparation", "approval", "targetLineIndex",
                        "productId", "itemId", "vendorItemId", "quantity",
                        "unitPriceCeilingKrw", "linePriceCeilingKrw", "expectedOrigin",
                        "expectedPath", "targetOptionIndex", "groupName", "valueName")) {
                throw new Exception("POLICY_DENIED");
            }
        } else if (!hasOnlyKeys(bindings, baseKeys)) {
            throw new Exception("POLICY_DENIED");
        }
        if (!COUPANG_PRODUCT_ORIGINS.contains(expectedOrigin)
                || !expectedPath.matches("^/vp/products/[1-9][0-9]{0,19}/?$")) {
            throw new Exception("APPROVAL_PAGE_MISMATCH");
        }
        JSONArray items = snapshot.getJSONArray("items");
        long targetLineIndex = requiredIntegerRange(bindings, "targetLineIndex", 0,
                items.length() - 1);
        JSONObject item = items.getJSONObject((int) targetLineIndex);
        JSONObject line = verifiedCoupangLine(bindings, false);
        if (!sameCanonicalJson(line, lineFromItem(item))
                || !expectedPath.replaceFirst("/$", "").endsWith("/" + line.getString("productId"))
                || ("buy_now".equals(stepId) && !"single".equals(snapshot.getString("mode")))
                || ("add_to_cart".equals(stepId) && !"multi".equals(snapshot.getString("mode")))) {
            throw new Exception("APPROVAL_BINDING_MISMATCH");
        }
        if ("select_option".equals(stepId)) {
            JSONObject options = item.getJSONObject("options");
            if (!"choices".equals(options.getString("kind"))) {
                throw new Exception("APPROVAL_BINDING_MISMATCH");
            }
            JSONArray choices = options.getJSONArray("choices");
            long targetOptionIndex = requiredIntegerRange(bindings, "targetOptionIndex", 0,
                    choices.length() - 1);
            JSONObject choice = choices.getJSONObject((int) targetOptionIndex);
            String groupName = requiredNormalizedPublicText(bindings, "groupName", 120);
            String valueName = requiredNormalizedPublicText(bindings, "valueName", 120);
            long cursor = requiredInteger(mApprovedPreparation, "cursor", 0);
            if (targetOptionIndex != cursor - 2
                    || !groupName.equals(choice.getString("groupName"))
                    || !valueName.equals(choice.getString("valueName"))) {
                throw new Exception("APPROVAL_BINDING_MISMATCH");
            }
        }
    }

    private void verifyLocalApprovedPreparation(JSONObject value, boolean requireCurrentPage)
            throws Exception {
        if (!hasOnlyKeys(value, "merchantId", "recipeVersion", "mode", "approval", "items",
                    "cursor", "expectedPage")
                || !"COUPANG".equals(requiredString(value, "merchantId", 16))
                || !"1".equals(requiredString(value, "recipeVersion", 16))
                || !"single".equals(requiredString(value, "mode", 16))) {
            throw new Exception("INVALID_LOCAL_APPROVAL");
        }
        JSONObject approval = value.getJSONObject("approval");
        JSONArray items = value.getJSONArray("items");
        verifyCoupangPlan("single", "single_buy_now", approval, items,
                System.currentTimeMillis());
        expectedLocalCoupangStep(value);

        JSONObject expectedPage = value.getJSONObject("expectedPage");
        if (!hasOnlyKeys(expectedPage, "origin", "pathname")) {
            throw new Exception("APPROVAL_PAGE_MISMATCH");
        }
        String origin = requiredString(expectedPage, "origin", 512);
        String path = requiredString(expectedPage, "pathname", 1024);
        JSONObject line = lineFromItem(items.getJSONObject(0));
        if (!COUPANG_PRODUCT_ORIGINS.contains(origin)
                || !path.matches("^/vp/products/" + line.getString("productId") + "/?$")) {
            throw new Exception("APPROVAL_PAGE_MISMATCH");
        }
        if (requireCurrentPage) {
            CoupangProductIdentity current = currentCoupangProduct();
            if (current == null || !origin.equals(current.origin) || !path.equals(current.path)
                    || !line.getString("productId").equals(current.productId)
                    || !line.getString("itemId").equals(current.itemId)
                    || !line.getString("vendorItemId").equals(current.vendorItemId)) {
                throw new Exception("APPROVAL_PAGE_MISMATCH");
            }
        }
    }

    private static JSONObject approvedSnapshotFromLocal(JSONObject local) throws Exception {
        String mode = local.getString("mode");
        return new JSONObject()
                .put("merchantId", local.getString("merchantId"))
                .put("recipeVersion", local.getString("recipeVersion"))
                .put("mode", mode)
                .put("route", "single".equals(mode) ? "single_buy_now" : "multi_cart_checkout")
                .put("approval", new JSONObject(local.getJSONObject("approval").toString()))
                .put("items", new JSONArray(local.getJSONArray("items").toString()));
    }

    private static void verifyCoupangSnapshot(
            JSONObject snapshot, JSONObject actionApproval, long now) throws Exception {
        if (!hasOnlyKeys(snapshot, "merchantId", "recipeVersion", "mode", "route", "approval",
                    "items")
                || !"COUPANG".equals(requiredString(snapshot, "merchantId", 16))
                || !"1".equals(requiredString(snapshot, "recipeVersion", 16))) {
            throw new Exception("APPROVAL_BINDING_MISMATCH");
        }
        String mode = requiredString(snapshot, "mode", 16);
        String route = requiredString(snapshot, "route", 32);
        JSONObject approval = snapshot.getJSONObject("approval");
        if (!sameCanonicalJson(approval, actionApproval)) {
            throw new Exception("APPROVAL_BINDING_MISMATCH");
        }
        verifyCoupangPlan(mode, route, approval, snapshot.getJSONArray("items"), now);
    }

    private static void verifyCoupangPlan(String mode, String route, JSONObject approval,
            JSONArray items, long now) throws Exception {
        if (!("single".equals(mode) && "single_buy_now".equals(route))
                && !("multi".equals(mode) && "multi_cart_checkout".equals(route))) {
            throw new Exception("APPROVAL_BINDING_MISMATCH");
        }
        verifyCoupangApproval(approval, now);
        if (items.length() < 1 || items.length() > 20
                || ("single".equals(mode) && items.length() != 1)
                || ("multi".equals(mode) && items.length() < 2)) {
            throw new Exception("APPROVAL_BINDING_MISMATCH");
        }
        long total = 0;
        Set<String> lineKeys = new HashSet<>();
        for (int i = 0; i < items.length(); i++) {
            JSONObject item = items.getJSONObject(i);
            verifyCoupangItem(item);
            JSONObject line = lineFromItem(item);
            total = Math.addExact(total, line.getLong("linePriceCeilingKrw"));
            String key = line.getString("productId") + ":" + line.getString("itemId") + ":"
                    + line.getString("vendorItemId");
            if (!lineKeys.add(key)) throw new Exception("APPROVAL_BINDING_MISMATCH");
        }
        if (total > approval.getLong("totalPriceCeilingKrw")) {
            throw new Exception("APPROVAL_BINDING_MISMATCH");
        }
        JSONObject unsignedApproval = new JSONObject()
                .put("approvalId", approval.getString("approvalId"))
                .put("currency", approval.getString("currency"))
                .put("revision", approval.getLong("revision"))
                .put("expiresAt", approval.getString("expiresAt"))
                .put("totalPriceCeilingKrw", approval.getLong("totalPriceCeilingKrw"));
        JSONObject material = new JSONObject()
                .put("merchantId", "COUPANG")
                .put("recipeVersion", "1")
                .put("mode", mode)
                .put("route", route)
                .put("approval", unsignedApproval)
                .put("items", items);
        String expectedDigest = "sha256:" + hex(sha256(canonicalize(material)));
        if (!constantTimeEquals(expectedDigest, approval.getString("approvalDigest"))) {
            throw new Exception("APPROVAL_DIGEST_MISMATCH");
        }
    }

    private static void verifyCoupangApproval(JSONObject approval, long now) throws Exception {
        if (!hasOnlyKeys(approval, "approvalId", "approvalDigest", "currency", "revision",
                    "expiresAt", "totalPriceCeilingKrw")) {
            throw new Exception("APPROVAL_INVALID");
        }
        String approvalId = requiredString(approval, "approvalId", 100);
        if (!approvalId.matches("^[A-Za-z0-9][A-Za-z0-9._:-]{0,99}$")
                || !requiredString(approval, "approvalDigest", 71)
                        .matches("^sha256:[a-f0-9]{64}$")
                || !"KRW".equals(requiredString(approval, "currency", 3))) {
            throw new Exception("APPROVAL_INVALID");
        }
        requiredIntegerRange(approval, "revision", 1, SAFE_INTEGER_MAX);
        requiredIntegerRange(approval, "totalPriceCeilingKrw", 1, MAX_WON);
        long expiry = parseExpiry(requiredString(approval, "expiresAt", 80));
        if (expiry <= now || expiry - now > PURCHASE_APPROVAL_TTL_MS) {
            throw new Exception("APPROVAL_EXPIRED_OR_TOO_LONG");
        }
    }

    private static void verifyCoupangItem(JSONObject item) throws Exception {
        if (!hasOnlyKeys(item, "query", "offerIdentity", "quantity", "unitPriceCeilingKrw",
                    "linePriceCeilingKrw", "productUrl", "options")) {
            throw new Exception("APPROVAL_BINDING_MISMATCH");
        }
        String query = requiredNormalizedPublicText(item, "query", 160);
        if (likelySensitive(query)) throw new Exception("QUERY_POLICY_DENIED");
        JSONObject identity = item.getJSONObject("offerIdentity");
        if (!hasOnlyKeys(identity, "productId", "itemId", "vendorItemId")) {
            throw new Exception("OFFER_IDENTITY_INVALID");
        }
        String productId = requiredCoupangId(identity, "productId");
        String itemId = requiredCoupangId(identity, "itemId");
        String vendorItemId = requiredCoupangId(identity, "vendorItemId");
        JSONObject line = lineFromItem(item);
        String productUrl = requiredString(item, "productUrl", 4096);
        String expectedUrl = "https://www.coupang.com/vp/products/" + productId
                + "?itemId=" + itemId + "&vendorItemId=" + vendorItemId;
        if (!expectedUrl.equals(productUrl)) throw new Exception("PRODUCT_URL_DENIED");
        verifyCoupangOptions(item.getJSONObject("options"));
        // Force the bounded multiplication check even though lineFromItem already validates it.
        Math.multiplyExact(line.getLong("unitPriceCeilingKrw"), line.getLong("quantity"));
    }

    private static void verifyCoupangOptions(JSONObject options) throws Exception {
        String kind = requiredString(options, "kind", 16);
        if ("none".equals(kind)) {
            if (!hasOnlyKeys(options, "kind")) throw new Exception("OPTION_POLICY_DENIED");
            return;
        }
        if (!"choices".equals(kind) || !hasOnlyKeys(options, "kind", "choices")) {
            throw new Exception("OPTION_POLICY_DENIED");
        }
        JSONArray choices = options.getJSONArray("choices");
        if (choices.length() < 1 || choices.length() > 10) {
            throw new Exception("OPTION_POLICY_DENIED");
        }
        Set<String> seenGroups = new HashSet<>();
        for (int i = 0; i < choices.length(); i++) {
            JSONObject choice = choices.getJSONObject(i);
            if (!hasOnlyKeys(choice, "groupName", "valueName")) {
                throw new Exception("OPTION_POLICY_DENIED");
            }
            String groupName = requiredNormalizedPublicText(choice, "groupName", 120);
            String valueName = requiredNormalizedPublicText(choice, "valueName", 120);
            if (likelySensitive(groupName) || likelySensitive(valueName)
                    || !seenGroups.add(groupName)) {
                throw new Exception("OPTION_POLICY_DENIED");
            }
        }
    }

    private static JSONObject lineFromItem(JSONObject item) throws Exception {
        JSONObject identity = item.getJSONObject("offerIdentity");
        JSONObject line = new JSONObject()
                .put("productId", identity.get("productId"))
                .put("itemId", identity.get("itemId"))
                .put("vendorItemId", identity.get("vendorItemId"))
                .put("quantity", item.get("quantity"))
                .put("unitPriceCeilingKrw", item.get("unitPriceCeilingKrw"))
                .put("linePriceCeilingKrw", item.get("linePriceCeilingKrw"));
        return verifiedCoupangLine(line, true);
    }

    private static JSONObject verifiedCoupangLine(JSONObject value, boolean exactKeys)
            throws Exception {
        if (exactKeys && !hasOnlyKeys(value, "productId", "itemId", "vendorItemId", "quantity",
                    "unitPriceCeilingKrw", "linePriceCeilingKrw")) {
            throw new Exception("OFFER_IDENTITY_INVALID");
        }
        String productId = requiredCoupangId(value, "productId");
        String itemId = requiredCoupangId(value, "itemId");
        String vendorItemId = requiredCoupangId(value, "vendorItemId");
        long quantity = requiredIntegerRange(value, "quantity", 1, 99);
        long unit = requiredIntegerRange(value, "unitPriceCeilingKrw", 1, MAX_WON);
        long line = requiredIntegerRange(value, "linePriceCeilingKrw", 1, MAX_WON);
        if (Math.multiplyExact(unit, quantity) > line) {
            throw new Exception("PRICE_CEILING_INVALID");
        }
        return new JSONObject()
                .put("productId", productId)
                .put("itemId", itemId)
                .put("vendorItemId", vendorItemId)
                .put("quantity", quantity)
                .put("unitPriceCeilingKrw", unit)
                .put("linePriceCeilingKrw", line);
    }

    private static String requiredCoupangId(JSONObject value, String key) throws Exception {
        String result = requiredString(value, key, 20);
        if (!result.matches("^[1-9][0-9]{0,19}$")) {
            throw new Exception("OFFER_IDENTITY_INVALID");
        }
        return result;
    }

    private static long requiredIntegerRange(
            JSONObject value, String key, long minimum, long maximum) throws Exception {
        long result = requiredInteger(value, key, minimum);
        if (result > maximum) throw new Exception("INVALID_" + key);
        return result;
    }

    private static String requiredNormalizedPublicText(JSONObject value, String key, int max)
            throws Exception {
        String result = requiredString(value, key, max);
        String normalized = normalizedPublicText(result, max);
        if (normalized == null || !result.equals(normalized)) throw new Exception("INVALID_" + key);
        return result;
    }

    private static String normalizedPublicText(String value, int max) {
        if (value == null) return null;
        String normalized = Normalizer.normalize(value, Normalizer.Form.NFKC);
        StringBuilder output = new StringBuilder();
        boolean pendingSpace = false;
        for (int offset = 0; offset < normalized.length(); ) {
            int codePoint = normalized.codePointAt(offset);
            offset += Character.charCount(codePoint);
            if (Character.isWhitespace(codePoint) || Character.isSpaceChar(codePoint)) {
                pendingSpace = output.length() > 0;
                continue;
            }
            if (Character.isISOControl(codePoint)) return null;
            if (pendingSpace) output.append(' ');
            pendingSpace = false;
            output.appendCodePoint(codePoint);
            if (output.length() > max) return null;
        }
        String result = output.toString();
        return result.isEmpty() ? null : result;
    }

    private static boolean sameCanonicalJson(Object left, Object right) throws Exception {
        return constantTimeEquals(canonicalize(left), canonicalize(right));
    }

    private static String expectedLocalCoupangStep(JSONObject approval) throws Exception {
        long cursor = requiredIntegerRange(approval, "cursor", 0, 200);
        JSONArray items = approval.getJSONArray("items");
        if (items.length() != 1) throw new Exception("APPROVAL_STEP_MISMATCH");
        JSONObject options = items.getJSONObject(0).getJSONObject("options");
        int choiceCount = "choices".equals(options.getString("kind"))
                ? options.getJSONArray("choices").length() : 0;
        if (cursor >= 2 && cursor < 2 + choiceCount) return "select_option";
        if (cursor == 2 + choiceCount) return "verify_options";
        if (cursor == 3 + choiceCount) return "set_quantity";
        if (cursor == 4 + choiceCount) return "buy_now";
        throw new Exception("APPROVAL_STEP_MISMATCH");
    }

    private void advanceApprovedPreparationCursor() throws Exception {
        if (mApprovedPreparation == null) throw new Exception("APPROVAL_REQUIRED");
        long cursor = requiredIntegerRange(mApprovedPreparation, "cursor", 0, 200);
        mApprovedPreparation.put("cursor", cursor + 1);
        verifyLocalApprovedPreparation(mApprovedPreparation, true);
    }

    private boolean metadataMatchesCurrent(JSONObject metadata) throws Exception {
        JSONObject current = nativeMetadata();
        return hasOnlyKeys(metadata, "tabId", "frameId", "documentEpoch", "topOrigin",
                       "frameOrigin", "pathname", "queryOrFragmentPresent", "foreground")
                && current.getString("tabId").equals(metadata.getString("tabId"))
                && current.getString("frameId").equals(metadata.getString("frameId"))
                && current.getLong("documentEpoch") == metadata.getLong("documentEpoch")
                && current.getString("topOrigin").equals(metadata.getString("topOrigin"))
                && current.getString("frameOrigin").equals(metadata.getString("frameOrigin"))
                && current.getString("pathname").equals(metadata.getString("pathname"))
                && current.getBoolean("queryOrFragmentPresent")
                        == metadata.getBoolean("queryOrFragmentPresent")
                && metadata.getBoolean("foreground");
    }

    private static boolean likelySensitive(String value) {
        String text = value.toLowerCase(Locale.US);
        if (text.matches(".*(password|passwd|passcode|otp|one[- ]?time|cvv|cvc|card[ ]*number|"
                + "access[_ -]?token|api[_ -]?key|secret|비밀번호|인증번호|카드[ ]*번호|"
                + "보안[ ]*코드|주민등록).*")) {
            return true;
        }
        if (text.matches(".*[a-z0-9._%+-]+@[a-z0-9.-]+\\.[a-z]{2,}.*")
                || text.matches(".*https?://.*")) {
            return true;
        }
        return text.replaceAll("[^0-9]", "").length() >= 11;
    }

    private static void requireOnlyActionKeys(JSONObject value, String... keys) throws Exception {
        if (!hasOnlyKeys(value, keys)) throw new Exception("POLICY_DENIED");
    }

    private static boolean hasOnlyKeys(JSONObject value, String... keys) {
        Set<String> expected = new HashSet<>(Arrays.asList(keys));
        if (value.length() != expected.size()) return false;
        Iterator<String> iterator = value.keys();
        while (iterator.hasNext()) {
            if (!expected.contains(iterator.next())) return false;
        }
        return true;
    }

    private static String requiredString(JSONObject value, String key, int max) throws Exception {
        Object raw = value.get(key);
        if (!(raw instanceof String)) throw new Exception("INVALID_" + key);
        String result = (String) raw;
        if (result.isEmpty() || result.length() > max) throw new Exception("INVALID_" + key);
        for (int i = 0; i < result.length(); i++) {
            char character = result.charAt(i);
            if ((character <= 0x08) || character == 0x0b || character == 0x0c
                    || (character >= 0x0e && character <= 0x1f)) {
                throw new Exception("INVALID_" + key);
            }
        }
        return result;
    }

    private static String requiredId(JSONObject value, String key) throws Exception {
        String result = requiredString(value, key, 128);
        if (!result.matches("^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$")) {
            throw new Exception("INVALID_" + key);
        }
        return result;
    }

    private static String requiredHash(JSONObject value, String key) throws Exception {
        String result = requiredString(value, key, 64);
        if (!result.matches("^[a-f0-9]{64}$")) throw new Exception("INVALID_" + key);
        return result;
    }

    private static long requiredInteger(JSONObject value, String key, long minimum)
            throws Exception {
        Object raw = value.get(key);
        if (!(raw instanceof Number)) throw new Exception("INVALID_" + key);
        Number number = (Number) raw;
        double decimal = number.doubleValue();
        long result = number.longValue();
        if (Double.isNaN(decimal) || Double.isInfinite(decimal) || decimal != result || result < minimum
                || result > SAFE_INTEGER_MAX) {
            throw new Exception("INVALID_" + key);
        }
        return result;
    }

    private static long parseExpiry(String value) throws Exception {
        if (!value.matches("^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}\\.\\d{3}Z$")) {
            throw new Exception("INVALID_EXPIRY");
        }
        SimpleDateFormat format = new SimpleDateFormat("yyyy-MM-dd'T'HH:mm:ss.SSS'Z'", Locale.US);
        format.setLenient(false);
        format.setTimeZone(TimeZone.getTimeZone("UTC"));
        Date date = format.parse(value);
        if (date == null) throw new Exception("INVALID_EXPIRY");
        return date.getTime();
    }

    private static byte[] sha256(String value) throws Exception {
        return MessageDigest.getInstance("SHA-256").digest(value.getBytes(StandardCharsets.UTF_8));
    }

    private static String hex(byte[] bytes) {
        StringBuilder output = new StringBuilder(bytes.length * 2);
        for (byte value : bytes) output.append(String.format(Locale.US, "%02x", value & 0xff));
        return output.toString();
    }

    private static boolean constantTimeEquals(String left, String right) {
        return MessageDigest.isEqual(
                left.getBytes(StandardCharsets.UTF_8), right.getBytes(StandardCharsets.UTF_8));
    }

    /** Canonical JSON must stay byte-for-byte compatible with server/protocol.mjs. */
    private static String canonicalize(Object value) throws Exception {
        if (value == null || value == JSONObject.NULL) return "null";
        if (value instanceof Boolean) return value.toString();
        if (value instanceof String) return quoteJson((String) value);
        if (value instanceof Number) {
            double decimal = ((Number) value).doubleValue();
            if (Double.isNaN(decimal) || Double.isInfinite(decimal)) {
                throw new Exception("NON_FINITE_NUMBER");
            }
            if (value instanceof Byte || value instanceof Short || value instanceof Integer
                    || value instanceof Long) {
                return Long.toString(((Number) value).longValue());
            }
            return JSONObject.numberToString((Number) value);
        }
        if (value instanceof JSONArray) {
            JSONArray array = (JSONArray) value;
            StringBuilder output = new StringBuilder("[");
            for (int i = 0; i < array.length(); i++) {
                if (i > 0) output.append(',');
                output.append(canonicalize(array.get(i)));
            }
            return output.append(']').toString();
        }
        if (value instanceof JSONObject) {
            JSONObject object = (JSONObject) value;
            List<String> keys = new ArrayList<>();
            Iterator<String> iterator = object.keys();
            while (iterator.hasNext()) keys.add(iterator.next());
            Collections.sort(keys);
            StringBuilder output = new StringBuilder("{");
            for (int i = 0; i < keys.size(); i++) {
                if (i > 0) output.append(',');
                String key = keys.get(i);
                output.append(quoteJson(key)).append(':').append(canonicalize(object.get(key)));
            }
            return output.append('}').toString();
        }
        throw new Exception("UNSUPPORTED_CANONICAL_VALUE");
    }

    private static String quoteJson(String value) {
        StringBuilder output = new StringBuilder(value.length() + 2).append('"');
        for (int i = 0; i < value.length(); i++) {
            char character = value.charAt(i);
            switch (character) {
                case '"': output.append("\\\""); break;
                case '\\': output.append("\\\\"); break;
                case '\b': output.append("\\b"); break;
                case '\f': output.append("\\f"); break;
                case '\n': output.append("\\n"); break;
                case '\r': output.append("\\r"); break;
                case '\t': output.append("\\t"); break;
                default:
                    if (character < 0x20 || (Character.isSurrogate(character)
                            && (i + 1 >= value.length()
                                    || !Character.isSurrogatePair(character, value.charAt(i + 1))))) {
                        output.append(String.format(Locale.US, "\\u%04x", (int) character));
                    } else {
                        output.append(character);
                        if (Character.isHighSurrogate(character) && i + 1 < value.length()
                                && Character.isLowSurrogate(value.charAt(i + 1))) {
                            output.append(value.charAt(++i));
                        }
                    }
            }
        }
        return output.append('"').toString();
    }

    public void stop(String reason) {
        cancelRun(reason, true);
    }

    private void takeOver() {
        takeOver(tr("직접 조작으로 전환했습니다. 다시 시작하면 새 페이지를 확인합니다.",
                "Control returned to you. Starting again will create a fresh observation."));
    }

    private void takeOver(String reason) {
        cancelRun(reason, false);
    }

    private void cancelRun(String reason, boolean reopenPanel) {
        boolean wasRunning = mRunning;
        mRunning = false;
        mGeneration++;
        mControlGeneration++;
        for (JournalEntry entry : new ArrayList<>(mJournal.values())) {
            if ("STARTED".equals(entry.state)) {
                finishJournal(entry, "OUTCOME_UNKNOWN", "outcome_unknown",
                        "CANCELLED_AFTER_START");
            }
        }
        mLastObservationId = "";
        mLastObservationOrigin = "";
        mApprovedPreparation = null;
        if (mTaskTab != null) mTaskTab.removeObserver(mTabObserver);
        mUi.removeCallbacksAndMessages(null);
        HttpURLConnection connection = mConnection;
        if (connection != null) connection.disconnect();
        append(reason);
        Arrays.fill(mPermitKey, (byte) 0);
        mControlBar.setVisibility(View.GONE);
        if (mStartButton != null) {
            mStartButton.setEnabled(true);
            mStartButton.setText(tr("현재 탭에서 시작", "Start in current tab"));
        }
        if (mGoalInput != null) mGoalInput.setEnabled(true);
        if (mModelInput != null) mModelInput.setEnabled(true);
        if (mApiKeyInput != null) mApiKeyInput.setEnabled(true);
        mTaskContents = null;
        mTaskTab = null;
        if (!reopenPanel && mPanel != null) mPanel.dismiss();
        if (reopenPanel && wasRunning && !mDestroyed && !mActivity.isFinishing()) showPanel();
    }

    public void onBackgrounded() {
        mApiKey = "";
        if (mApiKeyInput != null) mApiKeyInput.setText("");
        if (mRunning) {
            mDestroyed = true;
            cancelRun(tr("브라우저를 벗어나 작업을 중지했습니다.",
                    "The run stopped when the browser left the foreground."), false);
            mDestroyed = false;
        }
        if (mPanel != null) mPanel.dismiss();
    }

    public void destroy() {
        mApiKey = "";
        if (mApiKeyInput != null) mApiKeyInput.setText("");
        mDestroyed = true;
        cancelRun(tr("종료됨", "Closed"), false);
        if (mPanel != null) mPanel.dismiss();
        mNetwork.shutdownNow();
        if (mLauncher.getParent() instanceof ViewGroup) {
            ((ViewGroup) mLauncher.getParent()).removeView(mLauncher);
        }
        if (mControlBar.getParent() instanceof ViewGroup) {
            ((ViewGroup) mControlBar.getParent()).removeView(mControlBar);
        }
    }
}
