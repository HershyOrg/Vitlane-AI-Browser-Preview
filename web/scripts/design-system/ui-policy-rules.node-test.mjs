import assert from "node:assert/strict";
import test from "node:test";
import {
  countDirectButtons,
  countDirectNativeControls,
  countLegacyControlClasses,
  countRawColors,
  countRawNamedColors,
  countRawDimensions,
  countUnapprovedDesignDimensions,
  countUnapprovedFontFamilies,
  customPropertyDefinitions,
  customPropertyReferences,
  directRadixImports,
  forbiddenSharedUIImports,
  hasInlineStyleObject,
  moduleSpecifiers,
  countFoundationColorReferences,
  countOutlineButtons,
  countRawFontWeights,
  countRelativeColorFunctions,
  countProductScrims,
  countRoundedBoldBorders,
  countUnthemedEffects,
  countUnthemedEffectUtilities,
  productScrimFamilies,
  roundedBoldBorderFamilies,
  unthemedEffects,
  selectorFamily,
  countThickSideBorders,
  countUnapprovedUppercase,
  cssRuleBlocks,
  fontWeightTokensAbove,
} from "./ui-policy-rules.mjs";

test("raw color는 hex와 functional color를 모두 센다", () => {
  assert.equal(
    countRawColors("color: #fff; background: rgba(0, 0, 0, 0.2);"),
    2,
  );
  assert.equal(countRawColors("color: var(--vt-semantic-color-text-primary);"), 0);
});

test("CSS named color는 token 이름이나 white-space와 구분한다", () => {
  assert.equal(
    countRawNamedColors(
      "a { color: white; background: var(--vt-foundation-color-route-blue); white-space: nowrap; }",
    ),
    1,
  );
});

test("shared UI에서 금지할 raw dimension을 센다", () => {
  assert.equal(countRawDimensions("padding: 8px 1rem; transition: 160ms;"), 3);
  assert.equal(countRawDimensions("padding: var(--vt-foundation-space-2);"), 0);
});

test("관리 대상 spacing, radius와 type의 raw dimension을 센다", () => {
  const source = `
    .unsafe {
      padding: 12px;
      margin-top: 20px;
      gap: .5rem;
      border-radius: 8px;
      font-size: 14px;
      font: 700 16px/1.4 sans-serif;
    }
    .safe { padding: var(--vt-space); width: 320px; transition: 120ms; }
    @media (max-width: 40rem) {}
  `;
  assert.equal(countUnapprovedDesignDimensions(source), 6);
});

test("link에 남은 legacy control class도 찾는다", () => {
  const source = `
    <Link className="phase6-link-button is-primary" />
    <a className="button button-primary" />
    <Button emphasis="primary" />
  `;
  assert.equal(countLegacyControlClasses(source), 2);
});

test("새 direct button과 inline style 우회를 찾는다", () => {
  assert.equal(countDirectButtons("<button>첫째</button><button>둘째</button>"), 2);
  assert.equal(countDirectButtons("<Button>공유</Button>"), 0);
  assert.equal(hasInlineStyleObject("<div style={{ color: value }} />"), true);
  assert.equal(hasInlineStyleObject('<div className="safe" />'), false);
});

test("표준 stack 밖 native control과 direct Radix import를 찾는다", () => {
  assert.equal(
    countDirectNativeControls(
      "<button /><input /><select /><textarea /><details><summary /></details>",
    ),
    6,
  );
  assert.equal(countDirectNativeControls("<Button /><Input /><Disclosure />"), 0);
  assert.deepEqual(
    directRadixImports(
      'import { Dialog } from "radix-ui"; import * as Slot from "@radix-ui/react-slot";',
    ),
    ["radix-ui", "@radix-ui/react-slot"],
  );
});

test("font family는 design token만 허용한다", () => {
  assert.equal(
    countUnapprovedFontFamilies(
      `
        body { font-family: "Pretendard", sans-serif; }
        code { font-family: var(--vt-semantic-font-data); }
        small { font: 700 var(--vt-foundation-font-size-caption)/1.4 ui-monospace, monospace; }
        strong { font: 700 var(--vt-foundation-font-size-body)/1.4 var(--vt-semantic-font-body); }
        em { font: inherit; }
        i { font-family: var(--vt-foundation-color-night); }
        b { font: 700 var(--vt-foundation-font-size-body)/1.4 var(--vt-foundation-color-night); }
      `,
    ),
    4,
  );
});

test("CSS custom property 정의와 참조를 분리해 추출한다", () => {
  const source = `
    :root { --vt-defined: #fff; }
    .safe { color: var(--vt-defined); }
    .broken { background: var(--vt-missing); }
  `;
  assert.deepEqual(customPropertyDefinitions(source), ["--vt-defined"]);
  assert.deepEqual(customPropertyReferences(source), [
    "--vt-defined",
    "--vt-missing",
  ]);
});

test("module import를 추출하고 shared UI public boundary를 검사한다", () => {
  assert.deepEqual(
    moduleSpecifiers(
      'import { Button } from "../shared/ui"; import("../shared/ui/Button");',
    ),
    ["../shared/ui", "../shared/ui/Button"],
  );
  assert.deepEqual(
    forbiddenSharedUIImports(
      "src/products/planning/iface/Page.tsx",
      'import { Button } from "../../../shared/ui/Button";',
    ),
    [
      "../../../shared/ui/Button: product code must import shared UI through its public barrel",
    ],
  );
  assert.deepEqual(
    forbiddenSharedUIImports(
      "src/shared/ui/Notice.tsx",
      'import { state } from "../../products/purchase/state";',
    ),
    [
      "../../products/purchase/state: shared UI cannot depend on product or app modules",
    ],
  );
});

test("Zen N1: foundation 색 이름 참조를 CSS와 TS에서 센다", () => {
  assert.equal(
    countFoundationColorReferences(
      "a { color: var(--vt-foundation-color-route-blue); border-color: var(--vt-semantic-color-border-default); }",
    ),
    1,
  );
  assert.equal(
    countFoundationColorReferences(
      'const accent = getComputedStyle(root).getPropertyValue("--vt-foundation-color-route-blue-strong");',
    ),
    1,
  );
});

test("Zen N2: 상대색 함수만 세고 color-mix는 세지 않는다", () => {
  assert.equal(
    countRelativeColorFunctions(
      "a { color: oklch(from var(--x) calc(l + 0.2) c h); background: color-mix(in srgb, var(--y) 80%, transparent); }",
    ),
    1,
  );
  assert.equal(countRelativeColorFunctions("a { color: rgb(from var(--x) r g b / 50%); }"), 1);
});

test("Zen N3: .vt-eyebrow 밖의 uppercase만 at-rule 안에서도 센다", () => {
  const css =
    ".vt-eyebrow { text-transform: uppercase; } .a { text-transform: uppercase; } @media (min-width: 1px) { .b { text-transform: uppercase; } .vt-eyebrow--inverse { text-transform: uppercase; } }";
  assert.equal(countUnapprovedUppercase(css), 2);
});

test("Zen N4: 숫자·bold font-weight와 600 초과 토큰을 센다", () => {
  assert.equal(
    countRawFontWeights(
      "a { font-weight: 700; } b { font-weight: var(--vt-foundation-font-weight-heading); } c { font: 600 1rem/1.2 var(--vt-semantic-font-body); } d { font-weight: bold; }",
    ),
    3,
  );
  assert.deepEqual(
    fontWeightTokensAbove({
      foundation: { font: { weight: { regular: { value: 400 }, heading: { value: 720 } } } },
    }),
    ["heading=720"],
  );
});

test("Zen N5: thin 토큰·1px·0을 제외한 측면 border를 allowlist 밖에서 센다", () => {
  const css = [
    ".notice { border-inline-start: var(--vt-foundation-border-focus) solid var(--c); }",
    ".thin { border-left: var(--vt-foundation-border-thin) solid var(--c); }",
    ".raw { border-left: 3px solid var(--c); }",
    ".zero { border-left: 0; }",
    ".width { border-inline-start-width: var(--vt-foundation-space-1); }",
    ".nav li { border-inline-start: var(--vt-foundation-border-focus) solid transparent; }",
    ".rem { border-left: 0.2rem solid var(--c); }",
    ".minified{gap:0;border-left:4px solid var(--c)}",
  ].join(" ");
  assert.equal(countThickSideBorders(css, ["nav li"]), 5);
  assert.equal(countThickSideBorders(css), 6);
});

test("Zen N6: vt-button 블록의 currentColor border만 센다", () => {
  assert.equal(
    countOutlineButtons(
      ".vt-button.vt-button--secondary { border-color: currentColor; } .card { border-color: currentColor; }",
    ),
    1,
  );
});

test("cssRuleBlocks는 중첩 at-rule 안의 selector와 자기 선언만 돌려준다", () => {
  const blocks = cssRuleBlocks("@media (x) { .a { color: var(--a); } .b { color: var(--b); } }");
  assert.deepEqual(
    blocks.map((block) => block.selector),
    [".a", ".b", "@media (x)"],
  );
  assert.equal(blocks[2].declarations.trim(), "");
});

test("N9: selector family는 상태 pseudo-class·속성·is- 클래스를 벗긴다", () => {
  assert.equal(selectorFamily(".tab[aria-pressed=\"true\"]:hover"), ".tab");
  assert.equal(selectorFamily(".card.is-current .title, .card:not(:disabled) .title"), ".card .title");
  assert.equal(selectorFamily(".a:focus-visible, .b::after"), ".a, .b");
});

test("N9: radius와 2px 이상 border 강조가 한 family에 있으면 센다", () => {
  const css = `
    .tab { border-radius: var(--vt-semantic-radius-control); }
    .tab[aria-pressed="true"] { border-bottom: var(--vt-foundation-border-focus) solid currentColor; }
    .pill { border-radius: 999px; border: var(--vt-foundation-border-thin) solid currentColor; }
    .flat { border-radius: 0; box-shadow: inset 0 -3px var(--vt-semantic-color-action-primary); }
    .ring { border-radius: 8px; box-shadow: inset 0 0 0 2px currentColor; }
    .glow { border-radius: 8px; box-shadow: 0 4px 12px rgba(0, 0, 0, 0.2); }
    .none { border-radius: var(--vt-foundation-radius-none); border-width: 2px; }
    .focus { border-radius: 6px; box-shadow: inset 0 calc(var(--vt-foundation-border-focus) * -1) currentColor; }
  `;
  assert.deepEqual(roundedBoldBorderFamilies(css), [".tab", ".ring", ".focus"]);
  assert.equal(countRoundedBoldBorders(css), 3);
  assert.equal(countRoundedBoldBorders(css, [".tab", ".ring"]), 1);
});

test("N9: 1px border, blur가 있는 그림자, radius 0은 세지 않는다", () => {
  const css = `
    .card { border-radius: 8px; border: 1px solid currentColor; box-shadow: 0 0 0 1px currentColor; }
    .stripe { border-left: 3px solid currentColor; }
    @media (max-width: 40rem) { .card { border-radius: 4px; } }
  `;
  assert.equal(countRoundedBoldBorders(css), 0);
});

test("N10: 제품 CSS가 전체 화면 막을 직접 칠하면 센다 — 글자색·scrim 토큰·blur 모두", () => {
  const css = `
    .overlay { position: fixed; inset: 0; background: color-mix(in srgb, var(--vt-semantic-color-text-primary) 52%, transparent); }
    .modal{position:fixed;inset:var(--vt-foundation-space-0);background:color-mix(in srgb,var(--vt-semantic-color-text-primary) 55%,transparent)}
    .drawer { position: fixed; inset: 0; background: var(--vt-semantic-color-surface-scrim); }
    .frosted { position: fixed; inset: 0; backdrop-filter: blur(var(--vt-foundation-space-1)); }
  `;
  assert.deepEqual(productScrimFamilies(css), [".overlay", ".modal", ".drawer", ".frosted"]);
  assert.equal(countProductScrims(css, [".modal"]), 3);
});

test("N10: 상태 selector에서 칠해도 같은 selector family로 센다", () => {
  const css = `
    .sidebar-backdrop { position: fixed; z-index: 100; inset: 0; }
    .sidebar-backdrop.is-open { display: block; background: var(--vt-semantic-color-surface-scrim); }
  `;
  assert.deepEqual(productScrimFamilies(css), [".sidebar-backdrop"]);
});

test("N10: 위치만 가진 막, 불투명 전체 화면 표면, 전체가 아닌 fixed 층은 세지 않는다", () => {
  const css = `
    .overlay { position: fixed; inset: 0; display: grid; place-items: center; }
    .fullscreen-panel { position: fixed; inset: 0; background: var(--vt-semantic-color-surface-base); }
    .caption { position: absolute; inset: 0; background: color-mix(in srgb, var(--vt-semantic-color-text-primary) 78%, transparent); }
    .dock { position: fixed; bottom: 0; background: color-mix(in srgb, var(--vt-semantic-color-surface-canvas) 78%, transparent); backdrop-filter: var(--vt-semantic-effect-glass-blur); }
    .sheet-shelf { position: fixed; inset: 0 0 auto; background: var(--vt-semantic-color-surface-scrim); }
    .closed { position: fixed; inset: 0; backdrop-filter: none; }
  `;
  assert.equal(countProductScrims(css), 0);
});

test("N11: blur는 국소 표면의 glass 토큰만 — 간격 토큰·리터럴과 막의 blur를 센다", () => {
  const css = `
    .raw { backdrop-filter: blur(18px); }
    .spacing { -webkit-backdrop-filter: blur(var(--vt-foundation-space-2)); backdrop-filter: blur(var(--vt-foundation-space-2)); }
    .borrowed { backdrop-filter: var(--vt-semantic-effect-band-blur); }
    .vt-scrim { background: var(--vt-semantic-color-surface-scrim); backdrop-filter: blur(4px); }
    .vt-scrim.is-open { backdrop-filter: var(--vt-semantic-effect-glass-blur); }
  `;
  assert.deepEqual(unthemedEffects(css), [
    ".raw: backdrop-filter blur(18px)",
    ".spacing: backdrop-filter blur(var(--vt-foundation-space-2))",
    ".spacing: backdrop-filter blur(var(--vt-foundation-space-2))",
    ".borrowed: backdrop-filter var(--vt-semantic-effect-band-blur)",
    ".vt-scrim: a scrim darkens and never blurs (backdrop-filter blur(4px))",
    ".vt-scrim.is-open: a scrim darkens and never blurs (backdrop-filter var(--vt-semantic-effect-glass-blur))",
  ]);
});

test("N11: band blur는 랜딩 Searching 밴드 안에서만 — 다른 표면이 빌리거나 밴드가 glass를 쓰면 센다", () => {
  const css = `
    .source-band { background: color-mix(in srgb, var(--vt-marketing-canvas) 55%, transparent); -webkit-backdrop-filter: var(--vt-semantic-effect-band-blur); backdrop-filter: var(--vt-semantic-effect-band-blur); }
    .dock { backdrop-filter: var(--vt-semantic-effect-band-blur); }
    .source-band-strong { backdrop-filter: var(--vt-semantic-effect-glass-blur); }
  `;
  assert.deepEqual(unthemedEffects(css), [
    ".dock: backdrop-filter var(--vt-semantic-effect-band-blur)",
    ".source-band-strong: backdrop-filter var(--vt-semantic-effect-glass-blur)",
  ]);
});

test("N11: 흐리는 층을 글자색으로 칠하면 센다(테마에 따라 뒤집힌다)", () => {
  const css = `
    .caption { background: color-mix(in srgb, var(--vt-semantic-color-text-primary) 78%, transparent); }
    .caption:hover { backdrop-filter: var(--vt-semantic-effect-glass-blur); }
  `;
  assert.deepEqual(unthemedEffects(css), [".caption: blurred layer painted from a text color"]);
});

test("N11: 어둡게만 칠한 막, 국소 표면의 glass 토큰, none은 세지 않는다", () => {
  const css = `
    .vt-scrim { background: var(--vt-semantic-color-surface-scrim); }
    .vt-scrim.is-closing { backdrop-filter: none; }
    .dock { background: color-mix(in srgb, var(--vt-semantic-color-surface-canvas) 78%, transparent); backdrop-filter: var(--vt-semantic-effect-glass-blur); -webkit-backdrop-filter: var(--vt-semantic-effect-glass-blur); }
    .badge::before { background: color-mix(in srgb, var(--vt-semantic-color-surface-base) 60%, transparent); backdrop-filter: var(--vt-semantic-effect-glass-blur) !important; }
    .dock.has-thread { backdrop-filter: none; -webkit-backdrop-filter: none; }
  `;
  assert.equal(countUnthemedEffects(css), 0);
});

test("N11: TSX의 Tailwind blur utility와 흑백 고정 채움을 센다", () => {
  assert.equal(
    countUnthemedEffectUtilities('"fixed inset-0 z-50 bg-black/10 supports-backdrop-filter:backdrop-blur-xs"'),
    2,
  );
  assert.equal(countUnthemedEffectUtilities('"dark:bg-white/20 backdrop-blur-[6px]"'), 2);
  assert.equal(countUnthemedEffectUtilities('"vt-scrim fixed inset-0 z-50 bg-muted/50 bg-white-soft"'), 0);
  assert.equal(countUnthemedEffectUtilities("const blur = getComputedStyle(node).backdropFilter;"), 0);
});
