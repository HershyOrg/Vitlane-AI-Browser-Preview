import { useEffect, useMemo, useState } from "react";
import { useLocation } from "react-router";
import { operatorFreshAuthRequiredEvent } from "../../../shared/api/client";
import { useLocale } from "../../../shared/i18n";
import {
  Alert,
  AlertDescription,
  AlertTitle,
  Button,
  ButtonLink,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "../../../shared/ui";

export function OperatorFreshAuthAlert() {
  const { l } = useLocale();
  const location = useLocation();
  const [returnTo, setReturnTo] = useState<string | null>(null);

  useEffect(() => {
    const currentPath = `${location.pathname}${location.search}`;
    const onFreshAuthRequired = () => setReturnTo(currentPath);
    window.addEventListener(
      operatorFreshAuthRequiredEvent,
      onFreshAuthRequired,
    );
    return () => {
      window.removeEventListener(
        operatorFreshAuthRequiredEvent,
        onFreshAuthRequired,
      );
    };
  }, [location.pathname, location.search]);

  const freshAuthURL = useMemo(() => {
    if (!returnTo) return "";
    return `/api/v1/auth/google/start?fresh=1&returnTo=${encodeURIComponent(returnTo)}`;
  }, [returnTo]);

  return (
    <Dialog
      open={returnTo !== null}
      onOpenChange={(open) => {
        if (!open) setReturnTo(null);
      }}
    >
      <DialogContent role="alertdialog" showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>{l("Reauthenticate operator access", "운영자 권한 재인증")}</DialogTitle>
          <DialogDescription>
            {l(
              "The sensitive action was not run because your operator authentication is no longer fresh.",
              "최근 인증된 운영자 권한이 없어 민감 작업을 실행하지 않았습니다.",
            )}
          </DialogDescription>
        </DialogHeader>
        <Alert variant="destructive">
          <AlertTitle>{l("Authentication required", "다시 인증해야 합니다")}</AlertTitle>
          <AlertDescription>
            {l(
              "For security, Google authentication from the last 15 minutes is required. You will return to this operator screen afterward.",
              "보안을 위해 최근 15분 내 Google 인증이 필요합니다. 재인증 후 지금 보고 있는 운영 화면으로 돌아옵니다.",
            )}
          </AlertDescription>
        </Alert>
        <DialogFooter>
          <Button emphasis="quiet" onClick={() => setReturnTo(null)}>
            {l("Close", "닫기")}
          </Button>
          <ButtonLink emphasis="primary" href={freshAuthURL}>
            {l("Reauthenticate with Google", "Google로 다시 인증")}
          </ButtonLink>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
