import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { DesignLabPage } from "./DesignLabPage";

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <DesignLabPage />
  </StrictMode>,
);
