import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useLocale } from "../../../shared/i18n";
import {
  getAccountOverview,
  type WalletProjection,
} from "../infra/accountApi";

export function WalletPanel() {
  const { l } = useLocale();
  const [wallet, setWallet] = useState<WalletProjection | null>(null);

  useEffect(() => {
    let active = true;
    void getAccountOverview()
      .then(({ account }) => {
        if (active) {
          setWallet(
            account.wallets.find(
              ({ wallet: record }) => (
                record.isDefault
                && record.registrationStatus === "REGISTERED"
              ),
            ) ?? null,
          );
        }
      })
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, []);

  return (
    <Link className="wallet-trigger" to="/account">
      <span className={`connection-dot ${wallet?.ownership.status === "VALID" ? "is-connected" : ""}`} />
      {wallet
        ? `${wallet.wallet.address.slice(0, 6)}…${wallet.wallet.address.slice(-4)}`
        : l("Register wallet", "지갑 등록")}
    </Link>
  );
}
