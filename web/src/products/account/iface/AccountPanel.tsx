import { AppOverlay } from "../../../shared/AppOverlay";
import { useLocale } from "../../../shared/i18n";
import {
  AccountOverviewPage,
  type AccountOverviewView,
} from "./AccountOverviewPage";

export function AccountPanel({
  onClose,
  view,
}: {
  onClose: () => void;
  view: AccountOverviewView;
}) {
  const { l } = useLocale();
  const presentation = {
    all: {
      title: l("Account", "계정"),
      description: l(
        "Manage your profile and the account information required for purchases.",
        "프로필과 구매에 필요한 계정 정보를 관리합니다.",
      ),
    },
    account: {
      title: l("Account", "계정"),
      description: l(
        "Manage your profile, wallet, and shipping address.",
        "프로필, 지갑, 배송지를 관리합니다.",
      ),
    },
    liked: {
      title: l("Liked products", "좋아요한 상품"),
      description: l(
        "Review the products you liked in curations.",
        "큐레이션에서 표시한 상품을 모아 확인합니다.",
      ),
    },
    purchased: {
      title: l("Purchase-checked products", "구매 체크한 상품"),
      description: l(
        "Review the external products you marked as purchased. These are your own records, not order confirmations.",
        "구매했다고 직접 표시한 외부 상품을 모아 확인합니다. 주문 확인 내역이 아닙니다.",
      ),
    },
    management: {
      title: l("Account management", "계정 관리"),
      description: l(
        "Manage sign-in sessions and account deletion requests.",
        "로그인 세션과 계정 삭제 요청을 관리합니다.",
      ),
    },
  }[view];

  return (
    <AppOverlay
      description={presentation.description}
      eyebrow="PROFILE / VITLANE"
      onClose={onClose}
      title={presentation.title}
    >
      <AccountOverviewPage embedded view={view} />
    </AppOverlay>
  );
}
