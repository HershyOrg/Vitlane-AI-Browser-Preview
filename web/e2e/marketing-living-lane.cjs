const { firefox } = require("playwright");
const fs = require("node:fs");
const path = require("node:path");

const baseURL = process.env.MARKETING_BASE_URL || "http://marketing.localhost:8080";
const screenshotDir = process.env.MARKETING_SCREENSHOT_DIR || "/tmp/vitlane-marketing-living-lane";
let browser;

(async () => {
  fs.mkdirSync(screenshotDir, { recursive: true });
  browser = await firefox.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1440, height: 1000 } });
  await page.route("https://static.cloudflareinsights.com/**", (route) =>
    route.fulfill({ status: 200, contentType: "application/javascript", body: "" }),
  );
  await page.addInitScript(() => {
    localStorage.setItem("vitlane.marketing.theme", "light");
  });
  await page.goto(`${baseURL}/`, { waitUntil: "networkidle" });
  await page.locator('.living-lane .vt-reeded-glass[data-painted="true"]').waitFor();

  const desktop = await page.evaluate(() => {
    const hero = document.querySelector(".hero");
    const copy = document.querySelector(".hero-copy");
    const thesisCopy = document.querySelector(".hero-thesis-copy");
    const actions = document.querySelector(".hero-actions");
    const productLink = document.querySelector(".hero-product-link");
    const socialLink = document.querySelector(".hero-social-link");
    const band = document.querySelector(".source-band");
    const header = document.querySelector(".site-header--hero");
    const lane = document.querySelector(".living-lane");
    const glass = document.querySelector(".living-lane .vt-reeded-glass");
    const canvas = glass?.querySelector("canvas");
    if (!hero || !copy || !thesisCopy || !actions || !productLink || !socialLink || !band || !header || !lane || !canvas) return null;
    const heroRect = hero.getBoundingClientRect();
    const copyRect = copy.getBoundingClientRect();
    const thesisCopyRect = thesisCopy.getBoundingClientRect();
    const actionsRect = actions.getBoundingClientRect();
    const productLinkRect = productLink.getBoundingClientRect();
    const socialLinkRect = socialLink.getBoundingClientRect();
    const bandRect = band.getBoundingClientRect();
    const bandStyle = getComputedStyle(band);
    const headerStyle = getComputedStyle(header);
    const productStyle = getComputedStyle(productLink);
    const socialStyle = getComputedStyle(socialLink);
    const probe = document.createElement("span");
    probe.style.setProperty("transition", "none", "important");
    document.querySelector(".marketing-home").append(probe);
    const readColor = (value) => {
      probe.style.color = value;
      return getComputedStyle(probe).color;
    };
    const plate = readColor("var(--vt-marketing-action-plate)");
    const route = readColor("var(--vt-marketing-route)");
    probe.remove();
    return {
      // ADR-0078: reeded glass swells on the Beam's settled tempo; no input drives it.
      glassSwell: glass.getAttribute("data-motion") === "swell",
      glassTempo:
        glass.getAttribute("data-swell-cycle-ms") === "8230" &&
        glass.getAttribute("data-counter-swell-cycle-ms") === "12345" &&
        glass.getAttribute("data-drift-cycle-ms") === "24690",
      glassReeds: glass.getAttribute("data-reed-width") === "22",
      inquiryAbsent: !document.querySelector(".inquiry-panel, #inquiry-form"),
      copyInsideHero: copyRect.left >= heroRect.left && copyRect.right <= heroRect.right,
      routeLineRemoved: document.querySelectorAll(".hero-route, .brand-word").length === 0,
      // 2026-09-18: the Searching band hangs on the first viewport's bottom edge
      // inside the glass as a soft frosted strip without rule lines; the header
      // keeps its rule line and never blurs.
      sourceBandOnFirstViewportEdge: Math.abs(bandRect.bottom - window.innerHeight) <= 2,
      sourceBandInsideGlass: bandRect.bottom <= lane.getBoundingClientRect().bottom + 1,
      sourceBandSoftBlur: (bandStyle.backdropFilter || bandStyle.webkitBackdropFilter || "").includes("blur(6px)"),
      sourceBandWithoutRules: bandStyle.borderTopWidth === "0px" && bandStyle.borderBottomWidth === "0px",
      // No visible label (owner 2026-09-18): the names start on the header's left line.
      sourceBandLabelHidden:
        getComputedStyle(band.querySelector("#source-band-title")).position === "absolute" &&
        band.querySelector("#source-band-title").getBoundingClientRect().width <= 1,
      sourceBandNamesOnHeaderLine:
        Math.abs(band.querySelector(".source-band-viewport").getBoundingClientRect().left -
          header.querySelector(".brand-mark").getBoundingClientRect().left) <= 1,
      sourceBandPadded: getComputedStyle(band.querySelector(".source-band-inner")).paddingTop === "20px",
      sourceBandSpansStrip:
        band.querySelector(".source-band-viewport").getBoundingClientRect().right >= window.innerWidth - 33,
      headerNotBlurred: (headerStyle.backdropFilter || "none") === "none",
      headerKeepsRule: headerStyle.borderBottomWidth === "1px",
      productLinkRightOfThesis: actionsRect.left > thesisCopyRect.right,
      productLinkInsideHero: productLinkRect.right <= heroRect.right + 1,
      productLinkPositionedRight:
        productLinkRect.left + productLinkRect.width / 2 >=
          heroRect.left + heroRect.width * 0.62 &&
        productLinkRect.left + productLinkRect.width / 2 <
          heroRect.left + heroRect.width * 0.9,
      // Owner 2026-09-16: the glass carries the Still light, so the action is a
      // plain plate of the theme's own background with the brand color in the words.
      productLinkPlate: productStyle.backgroundColor === plate,
      productLinkAccentLabel: productStyle.color === route,
      productLinkBorderless: productStyle.borderTopWidth === "0px",
      productLinkContentCentered: productStyle.justifyContent === "center",
      productLinkFilled: productStyle.backgroundColor !== "rgba(0, 0, 0, 0)",
      productLinkTypeEmphasized: Number.parseFloat(productStyle.fontSize) >= 20,
      socialLinkCorrect:
        socialLink.getAttribute("href") === "https://x.com/Vitlane_" &&
        socialLink.getAttribute("target") === "_blank",
      socialLinkBelowProduct: socialLinkRect.top >= productLinkRect.bottom + 23,
      secondaryActionsInsetFromProduct:
        document.querySelector(".hero-secondary-actions").getBoundingClientRect().left - productLinkRect.left >= 11 &&
        document.querySelector(".hero-secondary-actions").getBoundingClientRect().left - productLinkRect.left <= 13,
      socialLinkNarrowerThanProduct: socialLinkRect.width < productLinkRect.width,
      socialLinkTouchTarget: socialLinkRect.height >= 44,
      socialLinkVisuallySecondary:
        Number.parseFloat(socialStyle.fontSize) < Number.parseFloat(productStyle.fontSize) &&
        Number.parseFloat(socialStyle.fontWeight) < Number.parseFloat(productStyle.fontWeight) &&
        socialStyle.boxShadow === "none" &&
        socialStyle.borderTopWidth === "0px" &&
        socialStyle.backgroundColor === "rgba(0, 0, 0, 0)" &&
        socialStyle.textDecorationLine === "underline",
      canvasSized: canvas.getBoundingClientRect().width > 0 && canvas.getBoundingClientRect().height > 0,
    };
  });
  const glassRow = (target) => target.evaluate(() => {
    const canvas = document.querySelector(".living-lane .vt-reeded-glass canvas");
    const context = canvas?.getContext("2d");
    if (!canvas || !context) return "";
    const row = Math.round(canvas.height * 0.45);
    return Array.from(context.getImageData(0, row, canvas.width, 1).data).join(",");
  });
  // ADR-0078: the reeds fade out toward the copy (left on a wide glass, up on a
  // phone) so the headline never sits on a comb of lines; they gather where the
  // light is. The second difference along a row keeps the comb and drops the
  // light's own smooth gradient.
  const reedComb = (target, at, from, to) => target.evaluate(({ at, from, to }) => {
    const canvas = document.querySelector(".living-lane .vt-reeded-glass canvas");
    const context = canvas?.getContext("2d");
    if (!canvas || !context) return null;
    const data = context.getImageData(0, Math.round(canvas.height * at), canvas.width, 1).data;
    const luminance = (index) =>
      0.2126 * data[index] + 0.7152 * data[index + 1] + 0.0722 * data[index + 2];
    let sum = 0;
    let count = 0;
    for (let x = Math.round(canvas.width * from) + 1; x < Math.round(canvas.width * to) - 1; x += 1) {
      sum += Math.abs(2 * luminance(x * 4) - luminance((x - 1) * 4) - luminance((x + 1) * 4));
      count += 1;
    }
    return count ? sum / count : null;
  }, { at, from, to });
  const copyComb = await reedComb(page, 0.5, 0, 0.25);
  const lightComb = await reedComb(page, 0.5, 0.7, 1);
  desktop.glassReedsFadeAtCopy =
    copyComb !== null && lightComb !== null &&
    copyComb < 0.12 && lightComb > 0.3 && lightComb > copyComb * 4;
  desktop.glassFadesAcross = await page.evaluate(
    () => document.querySelector(".living-lane .vt-reeded-glass")?.dataset.fade === "x",
  );

  const glassBefore = await glassRow(page);
  await page.waitForTimeout(1_200);
  desktop.glassSwells = glassBefore !== "" && glassBefore !== await glassRow(page);
  const productLink = page.locator(".hero-product-link");
  const productBackgroundBeforeHover = await productLink.evaluate(
    (element) => getComputedStyle(element).backgroundColor,
  );
  await productLink.hover();
  await page.waitForTimeout(220);
  desktop.productLinkHoverEffect = await productLink.evaluate(
    (element, before) => {
      const style = getComputedStyle(element);
      return style.backgroundColor !== before || style.transform !== "none";
    },
    productBackgroundBeforeHover,
  );
  const socialLink = page.locator(".hero-social-link");
  const socialColorsBeforeHover = await socialLink.evaluate((element) => {
    const style = getComputedStyle(element);
    return { color: style.color, underline: style.textDecorationColor };
  });
  await socialLink.hover();
  await page.waitForTimeout(220);
  desktop.socialLinkColorsUnified = await socialLink.evaluate(
    (element, before) => {
      const style = getComputedStyle(element);
      return before.color === before.underline &&
        style.color === style.textDecorationColor &&
        style.color !== before.color;
    },
    socialColorsBeforeHover,
  );
  // "먼저 써 본 사람들": the loop never pauses on hover (2026-09-14 owner decision).
  // The track animates continuously, so Playwright's stability-gated hover
  // would time out; move the pointer over the viewport box instead.
  const voicesTrack = page.locator(".voices-track");
  const voicesViewport = page.locator(".voices-viewport");
  const releaseBuild = await page.evaluate(() => document.documentElement.dataset.releaseBuild === "true");
  if (releaseBuild) {
    // Release builds intentionally omit unverified, development-only quotes.
    desktop.stubVoicesAbsent = await page.locator('.voice[data-stub="true"]').count() === 0;
  } else {
    await voicesViewport.scrollIntoViewIfNeeded();
    const voicesBox = await voicesViewport.boundingBox();
    await page.mouse.move(voicesBox.x + voicesBox.width / 2, voicesBox.y + voicesBox.height / 2);
    await page.waitForTimeout(150);
    const driftBefore = await voicesTrack.evaluate((element) => getComputedStyle(element).transform);
    await page.waitForTimeout(900);
    desktop.voicesKeepDriftingOnHover = await voicesTrack.evaluate(
      (element, before) =>
        getComputedStyle(element).animationPlayState === "running" &&
        getComputedStyle(element).transform !== before,
      driftBefore,
    );
  }
  await page.locator("#lane").evaluate((element) => {
    window.scrollTo(0, element.getBoundingClientRect().top + window.scrollY);
  });
  await page.waitForTimeout(250);
  Object.assign(desktop, await page.evaluate(() => {
    const home = document.querySelector(".marketing-home");
    const firstStep = document.querySelector(".lane-item");
    const frame = document.querySelector(".lane-frame");
    const spot = document.querySelector(".lane-spot");
    if (!home || !firstStep || !frame || !spot) return {
      laneSubheadingRemoved: false,
      laneFrameAlignedToFirstStep: false,
      laneLightVeilDark: false,
    };

    const probe = document.createElement("span");
    probe.style.color = "var(--vt-marketing-frame-veil-base)";
    home.append(probe);
    const veilBase = getComputedStyle(probe).color;
    probe.remove();
    const channels = veilBase.match(/\d+(?:\.\d+)?/g)?.slice(0, 3).map(Number) || [];
    return {
      laneSubheadingRemoved:
        !document.querySelector(".lane-subheading") &&
        !document.body.textContent.includes("Start to finish, on the real screens."),
      laneFrameAlignedToFirstStep:
        Math.abs(frame.getBoundingClientRect().top - firstStep.getBoundingClientRect().top) <= 1,
      laneLightVeilDark:
        document.documentElement.dataset.theme === "light" &&
        getComputedStyle(spot).boxShadow !== "none" &&
        channels.length === 3 &&
        channels.reduce((sum, channel) => sum + channel, 0) / channels.length < 128,
    };
  }));
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: path.join(screenshotDir, "landing-desktop.png"), fullPage: true });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.reload({ waitUntil: "networkidle" });
  const mobile = await page.evaluate(() => {
    const hero = document.querySelector(".hero");
    const heading = document.querySelector("#hero-title");
    const productLink = document.querySelector(".hero-product-link");
    const socialLink = document.querySelector(".hero-social-link");
    if (!hero || !heading || !productLink || !socialLink) return null;
    const heroRect = hero.getBoundingClientRect();
    const headingRect = heading.getBoundingClientRect();
    const productLinkRect = productLink.getBoundingClientRect();
    const socialLinkRect = socialLink.getBoundingClientRect();
    return {
      headingFits: headingRect.left >= heroRect.left - 1 && headingRect.right <= heroRect.right + 1,
      productLinkFits:
        productLinkRect.left >= heroRect.left - 1 &&
        productLinkRect.right <= heroRect.right + 1,
      socialLinkFits:
        socialLinkRect.left >= heroRect.left - 1 &&
        socialLinkRect.right <= heroRect.right + 1,
      socialLinkBelowProduct: socialLinkRect.top >= productLinkRect.bottom + 23,
      secondaryActionsInsetFromProduct:
        document.querySelector(".hero-secondary-actions").getBoundingClientRect().left - productLinkRect.left >= 11 &&
        document.querySelector(".hero-secondary-actions").getBoundingClientRect().left - productLinkRect.left <= 13,
      socialLinkNarrowerThanProduct: socialLinkRect.width < productLinkRect.width,
      noHorizontalOverflow: document.documentElement.scrollWidth <= innerWidth,
      inquiryAbsent: !document.querySelector(".inquiry-panel, #inquiry-form"),
      headerLocaleVisible:
        document.querySelector(".site-header .header-locale-link")?.getBoundingClientRect().width > 0,
      headerProductLinkAbsent: !document.querySelector(".site-header .header-product-link"),
    };
  });
  mobile.glassFadesUp = await page.evaluate(
    () => document.querySelector(".living-lane .vt-reeded-glass")?.dataset.fade === "y",
  );
  // The light gathers behind the product action; since the pilot sentence left
  // the hero (2026-09-18) the action and its light sit higher, so the reed-free
  // top row carries a brighter smooth gradient whose 8-bit steps read ~0.3.
  // Average a few swell frames and keep the 2.5x fall toward the top.
  const averageComb = async (at) => {
    const samples = [];
    for (let index = 0; index < 4; index += 1) {
      samples.push(await reedComb(page, at, 0, 1));
      await page.waitForTimeout(400);
    }
    return samples.includes(null) ? null : samples.reduce((sum, value) => sum + value, 0) / samples.length;
  };
  const phoneTopComb = await averageComb(0.08);
  const phoneBottomComb = await averageComb(0.8);
  mobile.glassReedsFadeAboveCopy =
    phoneTopComb !== null && phoneBottomComb !== null &&
    phoneTopComb < 0.4 && phoneBottomComb > 0.45 && phoneBottomComb > phoneTopComb * 2.5;

  await page.getByRole("button", { name: "Mobile menu", exact: true }).click();
  const mobileMenu = page.locator(".marketing-mobile-menu");
  mobile.menuTermsVisible = await mobileMenu
    .getByText("Service & payment terms", { exact: true })
    .isVisible();
  mobile.localeOutsideMenu = await mobileMenu.getByText("Korean", { exact: true }).count() === 0;
  await page.screenshot({ path: path.join(screenshotDir, "landing-mobile-menu.png") });
  await page.keyboard.press("Escape");
  await mobileMenu.waitFor({ state: "hidden" });
  await page.screenshot({ path: path.join(screenshotDir, "landing-mobile.png"), fullPage: true });

  // ADR-0076: a phone runs the same sticky Lane. Scrolling advances the six
  // moments, one moment is shown at a time, and the camera moves in on the evidence.
  const laneTrack = await page.locator("#lane").evaluate((element) => ({
    top: element.getBoundingClientRect().top + window.scrollY,
    travel: element.offsetHeight - window.innerHeight,
  }));
  const laneAt = async (fraction) => {
    await page.evaluate(
      (top) => window.scrollTo({ top, behavior: "instant" }),
      Math.round(laneTrack.top + laneTrack.travel * fraction),
    );
    await page.waitForTimeout(500);
    return page.evaluate(() => {
      const sticky = document.querySelector(".lane-sticky");
      const list = document.querySelector(".lane-list");
      const active = document.querySelector(".lane-item.is-active");
      const frame = document.querySelector(".lane-frame");
      const talk = document.querySelector(".lane-talk");
      if (!sticky || !list || !active || !frame || !talk) return null;
      const listRect = list.getBoundingClientRect();
      const activeRect = active.getBoundingClientRect();
      const frameRect = frame.getBoundingClientRect();
      const talkRect = talk.getBoundingClientRect();
      return {
        stage: Number(sticky.dataset.stage),
        stuck: Math.abs(sticky.getBoundingClientRect().top) <= 1,
        oneMomentShown: activeRect.left >= listRect.left - 1 && activeRect.right <= listRect.right + 1,
        sceneFits: frameRect.left >= -1 && frameRect.right <= innerWidth + 1 && talkRect.bottom <= innerHeight + 1,
        zoom: Number(getComputedStyle(frame).getPropertyValue("--lane-camera-zoom")),
        framesLoaded: [...frame.querySelectorAll("img")].every((image) => image.complete && image.naturalWidth > 0),
        noHorizontalOverflow: document.documentElement.scrollWidth <= innerWidth,
      };
    });
  };
  const laneMoments = [await laneAt(0.05), await laneAt(0.55), await laneAt(0.97)];
  await page.screenshot({ path: path.join(screenshotDir, "landing-mobile-lane-receipt.png") });
  mobile.laneStickyOnPhone = laneMoments.every(Boolean) && await page.locator(".lane-stack").count() === 0;
  mobile.laneAdvancesWithScroll = laneMoments.map((moment) => moment?.stage).join() === "0,3,5";
  mobile.laneOneMomentInView = laneMoments.every((moment) =>
    moment?.stuck && moment.oneMomentShown && moment.sceneFits && moment.noHorizontalOverflow && moment.framesLoaded);
  mobile.laneCameraOnEvidence = laneMoments[1]?.zoom > 1 && laneMoments[2]?.zoom === 1;
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.waitForTimeout(200);
  mobile.laneReducedMotionStacks =
    await page.locator(".lane-stack figure").count() === 6 && await page.locator(".lane-sticky").count() === 0;
  await page.emulateMedia({ reducedMotion: "no-preference" });
  await page.evaluate(() => window.scrollTo({ top: 0, behavior: "instant" }));

  await page.setViewportSize({ width: 320, height: 568 });
  await page.waitForTimeout(300);
  mobile.laneFitsCompactPhone = await page.evaluate(() => {
    const sticky = document.querySelector(".lane-sticky");
    return Boolean(sticky) && sticky.scrollHeight <= sticky.clientHeight + 2;
  });
  mobile.compactHeaderFits = await page.evaluate(() => {
    const localeLink = document.querySelector(".site-header .header-locale-link");
    const menuTrigger = document.querySelector(".marketing-mobile-menu-trigger");
    if (!localeLink || !menuTrigger) return false;
    const localeRect = localeLink.getBoundingClientRect();
    const menuRect = menuTrigger.getBoundingClientRect();
    return document.documentElement.scrollWidth <= innerWidth &&
      localeRect.left >= 0 &&
      menuRect.right <= innerWidth;
  });
  await page.evaluate(() => {
    document.documentElement.style.fontSize = "200%";
  });
  // Enlarged text cannot fit the phone scene: the Lane reads as the list instead of clipping.
  await page.waitForTimeout(300);
  mobile.laneEnlargedTextStacks =
    await page.locator(".lane-stack").count() === 1 && await page.locator(".lane-sticky").count() === 0;
  mobile.englishZoomedActionsFit = await page.evaluate(() => {
    const hero = document.querySelector(".hero");
    const actions = document.querySelector(".hero-actions");
    const productLink = document.querySelector(".hero-product-link");
    const socialLink = document.querySelector(".hero-social-link");
    if (!hero || !actions || !productLink || !socialLink) return false;
    const heroRect = hero.getBoundingClientRect();
    const actionsRect = actions.getBoundingClientRect();
    return actionsRect.left >= heroRect.left - 1 &&
      actionsRect.right <= heroRect.right + 1 &&
      productLink.scrollWidth <= productLink.clientWidth + 1 &&
      socialLink.scrollWidth <= socialLink.clientWidth + 1;
  });
  await page.evaluate(() => {
    document.documentElement.style.removeProperty("font-size");
  });
  await page.setViewportSize({ width: 390, height: 844 });

  await Promise.all([
    page.waitForURL("**/ko/", { waitUntil: "domcontentloaded" }),
    page.locator(".site-header .header-locale-link").click(),
  ]);
  await page.locator("#hero-title").waitFor();
  mobile.koreanRouteReached =
    await page.locator("html").getAttribute("lang") === "ko" &&
    await page.locator(".site-header .header-locale-link").innerText() === "English";
  mobile.koreanXLinkVisible = await page.locator(".hero-social-link")
    .getByText("X에서 보기", { exact: true })
    .isVisible();
  mobile.koreanInstallLinkAbsent = await page.locator(".hero-install-link").count() === 0;
  await page.setViewportSize({ width: 320, height: 568 });
  await page.evaluate(() => {
    document.documentElement.style.fontSize = "200%";
  });
  mobile.koreanZoomedActionsFit = await page.evaluate(() => {
    const hero = document.querySelector(".hero");
    const actions = document.querySelector(".hero-actions");
    const productLink = document.querySelector(".hero-product-link");
    const socialLink = document.querySelector(".hero-social-link");
    if (!hero || !actions || !productLink || !socialLink) return false;
    const heroRect = hero.getBoundingClientRect();
    const actionsRect = actions.getBoundingClientRect();
    return actionsRect.left >= heroRect.left - 1 &&
      actionsRect.right <= heroRect.right + 1 &&
      productLink.scrollWidth <= productLink.clientWidth + 1 &&
      socialLink.scrollWidth <= socialLink.clientWidth + 1;
  });

  const stillContext = await browser.newContext({
    reducedMotion: "reduce",
    viewport: { width: 1440, height: 1000 },
  });
  const stillPage = await stillContext.newPage();
  await stillPage.route("https://static.cloudflareinsights.com/**", (route) =>
    route.fulfill({ status: 200, contentType: "application/javascript", body: "" }),
  );
  await stillPage.addInitScript(() => {
    localStorage.setItem("vitlane.marketing.theme", "light");
  });
  await stillPage.goto(`${baseURL}/`, { waitUntil: "networkidle" });
  await stillPage.locator('.living-lane .vt-reeded-glass[data-painted="true"]').waitFor();
  const stillBefore = await glassRow(stillPage);
  await stillPage.waitForTimeout(1_200);
  mobile.glassHoldsWithReducedMotion = stillBefore !== "" && stillBefore === await glassRow(stillPage);
  // The light behind the product action keeps the Still hue under reduced motion.
  mobile.glassTintedWithReducedMotion = await stillPage.evaluate(() => {
    const glass = document.querySelector(".living-lane .vt-reeded-glass");
    const canvas = glass?.querySelector("canvas");
    const action = document.querySelector(".hero-product-link");
    const context = canvas?.getContext("2d");
    if (!glass || !canvas || !action || !context) return false;
    const box = glass.getBoundingClientRect();
    const target = action.getBoundingClientRect();
    const scale = canvas.width / box.width;
    const x = Math.floor((target.right + 24 - box.left) * scale);
    const y = Math.floor((target.top + target.height / 2 - box.top) * scale);
    const [red, , blue] = context.getImageData(x, y, 1, 1).data;
    return blue - red >= 15;
  });
  await stillContext.close();

  const result = { ...(desktop || {}), ...(mobile || {}) };
  console.log(JSON.stringify(result));
  if (!desktop || !mobile || !Object.values(result).every(Boolean)) process.exitCode = 1;
  await browser.close();
  browser = undefined;
})().catch(async (error) => {
  console.error(error);
  await browser?.close();
  process.exitCode = 1;
});
