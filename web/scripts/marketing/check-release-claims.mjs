import { readFile, readdir } from "node:fs/promises";
import { extname, join, resolve } from "node:path";

const repositoryRoot = resolve(process.cwd(), "..");
const generatedDirectory = resolve(
  process.argv[2] ?? join(repositoryRoot, "marketing/assets/generated"),
);
const releaseFiles = [
  ...(await listFiles(generatedDirectory)),
  join(repositoryRoot, "marketing/index.html"),
  join(repositoryRoot, "marketing/ko/index.html"),
];
const forbiddenClaims = [
  "Purchase support is available through the PayPal Live Pilot",
  "PayPal Live Pilot is the separate real USD order, payment, and delivery path",
  "PayPal Live Pilot은 실제 USD 주문·결제·배송이 가능한 별도 경로",
  "Stores Vitlane searches",
  "Vitlane이 찾는 판매처",
  "Compare, buy, and stay on budget",
  "비교, 구매, 예산 맞춤까지",
  "After you choose, the order and payment continue within the approved budget",
  "고르고 나면 주문과 결제는 승인한 예산 안에서 이어집니다",
  "Anything over budget never appears",
  "예산 밖은 처음부터 보이지 않고",
];

for (const file of releaseFiles) {
  if (![".js", ".css", ".html"].includes(extname(file))) continue;
  const content = await readFile(file, "utf8");
  const leaked = forbiddenClaims.find((claim) => content.includes(claim));
  if (leaked) {
    throw new Error(`Release marketing surface contains an inactive capability claim in ${file}: ${leaked}`);
  }
}

const english = await readFile(join(repositoryRoot, "marketing/index.html"), "utf8");
const korean = await readFile(join(repositoryRoot, "marketing/ko/index.html"), "utf8");
for (const [locale, content, required] of [
  ["en", english, ["Live payment and ordering are disabled by default", "It is not a public purchase path."]],
  ["ko", korean, ["실결제와 실제 주문은 기본적으로 비활성화되어 있습니다", "현재 공개 구매 경로가 아닙니다."]],
]) {
  for (const statement of required) {
    if (!content.includes(statement)) {
      throw new Error(`Release marketing ${locale} page is missing its activation boundary: ${statement}`);
    }
  }
}

async function listFiles(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  const nested = await Promise.all(entries.map((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? listFiles(path) : [path];
  }));
  return nested.flat();
}
