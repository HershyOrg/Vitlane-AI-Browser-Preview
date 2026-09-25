import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { AppRouter } from "./app/router";
import { CurrentUserProvider } from "./products/account/app/useCurrentUser";
import { PreferencesProvider } from "./products/account/app/usePreferences";
import { installLocalReviewWallet } from "./products/payment/giwa/infra/localReviewWallet";
import {
  AppearanceProvider,
  TooltipProvider,
} from "./shared/ui";
import { LocaleProvider } from "./shared/i18n";
import "pretendard/dist/web/variable/pretendardvariable-dynamic-subset.css";
import "@fontsource/ibm-plex-mono/latin-400.css";
import "@fontsource/ibm-plex-mono/latin-500.css";
import "@fontsource/ibm-plex-mono/latin-600.css";
import "./styles.css";
import "./product-shell.css";
import "./order-operations.css";
import "./catalog-surfaces.css";
import "./account-surfaces.css";

async function bootstrap() {
  await installLocalReviewWallet();
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <LocaleProvider>
        <AppearanceProvider>
          <TooltipProvider>
            <CurrentUserProvider>
              <PreferencesProvider><AppRouter /></PreferencesProvider>
            </CurrentUserProvider>
          </TooltipProvider>
        </AppearanceProvider>
      </LocaleProvider>
    </StrictMode>,
  );
}

void bootstrap();
