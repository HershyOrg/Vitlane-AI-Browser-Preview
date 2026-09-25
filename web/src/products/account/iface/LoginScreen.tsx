import { markAnalyticsLogin } from "../../../shared/analytics/analytics";
import {
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import { Navigate, useNavigate, useSearchParams } from "react-router";
import { APIError } from "../../../shared/api/client";
import { ModeBanner } from "../../../shared/ModeBanner";
import { useLocale } from "../../../shared/i18n";
import {
  BrandMark,
  Button,
  ButtonLink,
  Disclosure,
  Notice,
  ReededGlass,
} from "../../../shared/ui";
import { useCurrentUser } from "../app/useCurrentUser";
import {
  type AuthenticationCapabilities,
  createDevelopmentSession,
  getAuthenticationCapabilities,
  resetDevelopmentProfile,
} from "../infra/accountApi";

export function LoginScreen() {
  const { l } = useLocale();
  const { user, refresh } = useCurrentUser();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const [workingProfile, setWorkingProfile] = useState<string | null>(null);
  const postLoginPath = useRef<string | null>(null);
  const automaticProfile = useRef<string | null>(null);
  const [devError, setDevError] = useState<string | null>(null);
  const [capabilities, setCapabilities] =
    useState<AuthenticationCapabilities | null>(null);
  const [capabilitiesError, setCapabilitiesError] = useState(false);
  const returnTo = safeReturnTo(params.get("returnTo"));
  const hasExplicitReturnTo = params.has("returnTo");
  const errorCode = params.get("error") ?? "";
  const localReviewBuildEnabled =
    import.meta.env.DEV ||
    import.meta.env.VITE_ALLOW_DEV_AUTH_UI === "true";
  const localReviewEnabled =
    localReviewBuildEnabled && capabilities?.localReviewEnabled === true;
  const localReviewReady =
    localReviewEnabled && capabilities?.localReviewSeeded === true;
  const loginDrag = useLoginDragScroll();
  const loginMessages: Record<string, string> = {
    AUTH_LOGIN_EXPIRED: l(
      "Your sign-in session expired. Start Google sign-in again.",
      "로그인 시간이 지났습니다. Google 로그인을 다시 시작해 주세요.",
    ),
    AUTH_LOGIN_INVALID: l(
      "We couldn't verify this sign-in request. Start a new sign-in and try again.",
      "로그인 요청을 확인할 수 없습니다. 새 로그인으로 다시 시도해 주세요.",
    ),
    AUTH_PROVIDER_FAILED: l(
      "We couldn't start Google sign-in. Please try again.",
      "Google 로그인을 시작하지 못했습니다. 다시 시도해 주세요.",
    ),
    AUTH_IDENTITY_INVALID: l(
      "We couldn't verify your Google account. Try again with another account.",
      "Google 계정 정보를 확인하지 못했습니다. 다른 계정으로 다시 시도해 주세요.",
    ),
  };

  useEffect(() => {
    let active = true;
    void getAuthenticationCapabilities()
      .then((result) => {
        if (!active) return;
        setCapabilities(result);
        setCapabilitiesError(false);
      })
      .catch(() => {
        if (!active) return;
        setCapabilitiesError(true);
      });
    return () => {
      active = false;
    };
  }, []);

  const devLogin = useCallback(
    async (profileKey?: string, startPath?: string) => {
      const destination = startPath ?? returnTo;
      postLoginPath.current = destination;
      setWorkingProfile(profileKey ?? "default");
      setDevError(null);
      try {
        await createDevelopmentSession(profileKey);
        await refresh();
        navigate(destination, { replace: true });
      } catch (caught) {
        postLoginPath.current = null;
        setDevError(
          caught instanceof APIError
            ? caught.message
            : l(
                "We couldn't start the development account.",
                "개발용 계정을 시작하지 못했습니다.",
              ),
        );
      } finally {
        setWorkingProfile(null);
      }
    },
    [l, navigate, refresh, returnTo],
  );

  useEffect(() => {
    if (
      user ||
      !hasExplicitReturnTo ||
      !localReviewReady ||
      capabilities?.googleEnabled ||
      automaticProfile.current
    ) {
      return;
    }
    const profile = capabilities?.localReviewProfiles.find(
      (candidate) => candidate.startPath === returnTo,
    );
    if (!profile) return;
    automaticProfile.current = profile.key;
    void devLogin(profile.key, returnTo);
  }, [
    capabilities,
    devLogin,
    hasExplicitReturnTo,
    localReviewReady,
    returnTo,
    user,
  ]);

  if (user) {
    return <Navigate to={postLoginPath.current ?? returnTo} replace />;
  }

  async function resetProfile(profileKey: string, label: string) {
    if (
      !window.confirm(
        l(
          "Clear all wallet, shipping address, KYC, purchase, and plan data for {label} and return it to a new-user state?",
          "{label}의 지갑·배송지·KYC·구매·계획 데이터를 모두 비우고 신규 사용자 상태로 되돌릴까요?",
          { label },
        ),
      )
    ) {
      return;
    }
    setWorkingProfile(`reset:${profileKey}`);
    setDevError(null);
    try {
      await resetDevelopmentProfile(profileKey);
    } catch (caught) {
      setDevError(
        caught instanceof APIError
          ? caught.message
          : l(
              "We couldn't reset the local review profile.",
              "로컬 검수 프로필을 초기화하지 못했습니다.",
            ),
      );
    } finally {
      setWorkingProfile(null);
    }
  }

  const googleURL = `/api/v1/auth/google/start?returnTo=${encodeURIComponent(returnTo)}`;

  return (
    <main className="catalog-ui-login-shell vt-dark-scope">
      <div
        className="catalog-ui-login"
        data-drag-scroll="enabled"
        {...loginDrag}
      >
        <ReededGlass className="catalog-ui-login__glass" />
        <section
          className="catalog-ui-login__panel"
          aria-labelledby="login-title"
        >
          <header className="catalog-ui-login__brand">
            <BrandMark href="/" />
          </header>

          <div className="catalog-ui-login__content">
            <div className="catalog-ui-login__intro">
              <h1 id="login-title">{l("Sign in", "로그인")}</h1>
              <p>
                {l(
                  "Sign in to continue to Vitlane.",
                  "로그인이 필요한 서비스입니다. 계속 진행하려면 로그인 해주세요.",
                )}
              </p>
            </div>

            <div
              className="catalog-ui-login__methods"
              aria-label={l("Sign-in methods", "로그인 방법")}
            >
              {errorCode && (
                <Notice
                  announce
                  className="catalog-ui-login__notice"
                  tone="danger"
                  title={l("Sign-in failed", "로그인을 완료하지 못했습니다")}
                >
                  {loginMessages[errorCode] ??
                    l(
                      "We couldn't complete sign-in. Please try again.",
                      "로그인을 완료하지 못했습니다. 다시 시도해 주세요.",
                    )}
                </Notice>
              )}
              {devError && (
                <Notice
                  announce
                  className="catalog-ui-login__notice"
                  tone="danger"
                  title={l(
                    "Development account unavailable",
                    "개발용 계정을 시작하지 못했습니다",
                  )}
                >
                  {devError}
                </Notice>
              )}

              {capabilitiesError && (
                <Notice
                  announce
                  className="catalog-ui-login__notice"
                  tone="danger"
                  title={l(
                    "Sign-in configuration unavailable",
                    "로그인 구성을 확인하지 못했습니다",
                  )}
                >
                  {l(
                    "Check the local server's `/readyz` endpoint and authentication settings.",
                    "로컬 서버의 `/readyz`와 인증 설정을 확인해 주세요.",
                  )}
                </Notice>
              )}

              {!capabilities && !capabilitiesError && (
                <p className="catalog-ui-login__capability-loading" role="status">
                  {l(
                    "Checking available sign-in methods…",
                    "로그인 방법을 확인하는 중…",
                  )}
                </p>
              )}

              {capabilities?.googleEnabled && (
                <ButtonLink
                  className="catalog-ui-login__google"
                  emphasis="primary"
                  href={googleURL}
                  onClick={markAnalyticsLogin}
                >
                  <span className="catalog-ui-login__google-mark" aria-hidden="true">
                    {l("G", "G")}
                  </span>
                  {l("Continue with Google", "Google로 계속")}
                </ButtonLink>
              )}

              {localReviewReady && (
                <Disclosure
                  className="catalog-ui-login__review"
                  defaultOpen={!capabilities?.googleEnabled}
                  summary={
                    (capabilities?.localReviewProfiles?.length ?? 0) > 0
                      ? l("Choose a local TEST account", "로컬 TEST 계정 선택")
                      : l("Start with a local TEST account", "로컬 TEST 계정으로 시작")
                  }
                >
                  <p className="catalog-ui-login__review-intro">
                    {l(
                      "Starts with deterministic sample data.",
                      "고정된 샘플 데이터로 시작합니다.",
                    )}
                  </p>

                  {(capabilities?.localReviewProfiles?.length ?? 0) === 0 ? (
                    <Button
                      className="catalog-ui-login__dev"
                      emphasis={
                        capabilities?.googleEnabled ? "secondary" : "primary"
                      }
                      busy={workingProfile === "default"}
                      onClick={() => void devLogin()}
                    >
                      {workingProfile === "default"
                        ? l("Starting…", "시작하는 중…")
                        : l(
                            "Start with a local TEST account",
                            "로컬 TEST 계정으로 시작",
                          )}
                    </Button>
                  ) : (
                    <div
                      className="catalog-ui-login__profiles"
                      aria-label={l(
                        "Local review profiles",
                        "로컬 검수 프로필",
                      )}
                    >
                      {capabilities?.localReviewProfiles.map((profile) => {
                        const localizedProfile =
                          localReviewProfileCopy(profile.key, l) ?? profile;
                        const starting = workingProfile === profile.key;
                        const resetting =
                          workingProfile === `reset:${profile.key}`;
                        return (
                          <article
                            className="catalog-ui-login__profile"
                            key={profile.key}
                          >
                            <div>
                              <span className="catalog-ui-login__profile-role">
                                {profile.operator
                                  ? l("Operator", "운영자")
                                  : l("Customer", "일반 사용자")}
                              </span>
                              <h3>{localizedProfile.label}</h3>
                              <p>{localizedProfile.description}</p>
                            </div>
                            <div className="catalog-ui-login__profile-actions">
                              <Button
                                emphasis={
                                  capabilities?.googleEnabled
                                    ? "secondary"
                                    : "primary"
                                }
                                busy={starting}
                                disabled={workingProfile !== null}
                                onClick={() =>
                                  void devLogin(profile.key, profile.startPath)
                                }
                              >
                                {starting
                                  ? l("Starting…", "시작하는 중…")
                                  : l("Use this profile", "이 프로필로 시작")}
                              </Button>
                              {profile.resettable ? (
                                <Button
                                  emphasis="secondary"
                                  busy={resetting}
                                  disabled={workingProfile !== null}
                                  onClick={() =>
                                    void resetProfile(
                                      profile.key,
                                      localizedProfile.label,
                                    )
                                  }
                                >
                                  {resetting
                                    ? l("Resetting…", "초기화 중…")
                                    : l(
                                        "Reset to new-user state",
                                        "신규 사용자 상태로 초기화",
                                      )}
                                </Button>
                              ) : (
                                <span className="catalog-ui-login__profile-note">
                                  {l(
                                    "Restored when the server restarts",
                                    "서버 재시작 시 원본 복원",
                                  )}
                                </span>
                              )}
                            </div>
                          </article>
                        );
                      })}
                    </div>
                  )}
                </Disclosure>
              )}

              {localReviewEnabled && !localReviewReady && (
                <p className="vt-field__hint">
                  {l(
                    "Sample users are not ready. Restart the environment with `scripts/local-review.sh`.",
                    "샘플 사용자가 준비되지 않았습니다. `scripts/local-review.sh`로 환경을 다시 시작해 주세요.",
                  )}
                </p>
              )}

              {capabilities &&
                !capabilities.googleEnabled &&
                !localReviewReady &&
                !localReviewEnabled && (
                  <Notice
                    className="catalog-ui-login__notice"
                    tone="danger"
                    title={l(
                      "No sign-in method is available",
                      "사용 가능한 로그인 방법이 없습니다",
                    )}
                  >
                    {l(
                      "Start local review with `scripts/local-review.sh`. Deployed environments require Google OIDC configuration.",
                      "로컬 검수는 `scripts/local-review.sh`로 시작하고, 배포 환경은 Google OIDC 설정을 주입해야 합니다.",
                    )}
                  </Notice>
                )}
            </div>
          </div>

          <footer className="catalog-ui-login__footer">
            <ModeBanner />
          </footer>
        </section>
      </div>
    </main>
  );
}

type Localize = ReturnType<typeof useLocale>["l"];

function localReviewProfileCopy(key: string, l: Localize) {
  switch (key) {
    case "empty-user":
      return {
        label: l("Empty customer", "빈 일반 사용자"),
        description: l(
          "Starts without a wallet, shipping address, KYC, or purchase history.",
          "지갑·배송지·KYC·구매 이력 없이 시작합니다.",
        ),
      };
    case "empty-operator":
      return {
        label: l("Empty operator", "빈 운영자 사용자"),
        description: l(
          "Personal data is empty and operator navigation is available.",
          "개인 데이터가 비어 있고 운영자 화면을 사용할 수 있습니다.",
        ),
      };
    case "multi-product":
      return {
        label: l(
          "Multi-product purchase scenario",
          "다중 상품 구매 시나리오",
        ),
        description: l(
          "Review the Shopify candidate pool, cart, and purchase-preparation flow with three completed targets.",
          "완료된 목표 3개로 Shopify 후보군·장바구니·구매 준비 흐름을 검수합니다.",
        ),
      };
    default:
      return null;
  }
}

function safeReturnTo(value: string | null): string {
  if (!value || !value.startsWith("/") || value.startsWith("//")) return "/";
  return value;
}

type LoginDragState = {
  boundary: Element;
  dragged: boolean;
  pointerId: number;
  scrollElement: HTMLElement;
  startScrollTop: number;
  startX: number;
  startY: number;
};

/**
 * The desktop review browser is often panned like a canvas. Keep native touch
 * scrolling, while letting mouse and pen users pull whichever login pane owns
 * the overflow. A short press remains a normal profile-button click.
 */
function useLoginDragScroll() {
  const dragState = useRef<LoginDragState | null>(null);
  const suppressedClick = useRef<Element | null>(null);
  const resetClickTimer = useRef<number | null>(null);

  const finish = useCallback((pointerId: number) => {
    const state = dragState.current;
    if (!state || state.pointerId !== pointerId) return;
    suppressedClick.current = state.dragged ? state.boundary : null;
    state.scrollElement.classList.remove("is-pointer-dragging");
    dragState.current = null;
    if (resetClickTimer.current !== null) {
      window.clearTimeout(resetClickTimer.current);
    }
    resetClickTimer.current = window.setTimeout(() => {
      suppressedClick.current = null;
      resetClickTimer.current = null;
    }, 250);
  }, []);

  useEffect(() => {
    const onPointerMove = (event: PointerEvent) => {
      const state = dragState.current;
      if (!state || state.pointerId !== event.pointerId) return;
      const deltaX = event.clientX - state.startX;
      const deltaY = event.clientY - state.startY;
      if (!state.dragged && Math.hypot(deltaX, deltaY) < 6) return;

      state.dragged = true;
      state.scrollElement.classList.add("is-pointer-dragging");
      state.scrollElement.scrollTop = state.startScrollTop - deltaY;
      event.preventDefault();
    };
    const onPointerEnd = (event: PointerEvent) => finish(event.pointerId);

    window.addEventListener("pointermove", onPointerMove, { passive: false });
    window.addEventListener("pointerup", onPointerEnd);
    window.addEventListener("pointercancel", onPointerEnd);
    return () => {
      window.removeEventListener("pointermove", onPointerMove);
      window.removeEventListener("pointerup", onPointerEnd);
      window.removeEventListener("pointercancel", onPointerEnd);
      dragState.current?.scrollElement.classList.remove("is-pointer-dragging");
      if (resetClickTimer.current !== null) {
        window.clearTimeout(resetClickTimer.current);
      }
    };
  }, [finish]);

  return {
    onPointerDown(event: ReactPointerEvent<HTMLDivElement>) {
      if (
        event.button !== 0 ||
        event.pointerType === "touch" ||
        (event.target instanceof Element &&
          event.target.closest("input, textarea, select, [contenteditable='true']"))
      ) return;

      const eventTarget = event.target instanceof Element
        ? event.target
        : event.currentTarget;
      const scrollElement = findLoginScrollOwner(
        eventTarget,
        event.currentTarget,
      );
      if (!scrollElement) return;
      dragState.current = {
        boundary: eventTarget.closest("button, a") ?? eventTarget,
        dragged: false,
        pointerId: event.pointerId,
        scrollElement,
        startScrollTop: scrollElement.scrollTop,
        startX: event.clientX,
        startY: event.clientY,
      };
      suppressedClick.current = null;
    },
    onClickCapture(event: ReactMouseEvent<HTMLDivElement>) {
      const boundary = suppressedClick.current;
      const target = event.target;
      suppressedClick.current = null;
      if (!(target instanceof Node) || !boundary?.contains(target)) return;
      event.preventDefault();
      event.stopPropagation();
    },
  };
}

function findLoginScrollOwner(
  target: Element,
  boundary: HTMLElement,
): HTMLElement | null {
  let candidate: Element | null = target;
  while (candidate && boundary.contains(candidate)) {
    if (candidate instanceof HTMLElement) {
      const overflowY = window.getComputedStyle(candidate).overflowY;
      if (
        (overflowY === "auto" || overflowY === "scroll") &&
        candidate.scrollHeight > candidate.clientHeight
      ) {
        return candidate;
      }
    }
    if (candidate === boundary) break;
    candidate = candidate.parentElement;
  }
  return null;
}
