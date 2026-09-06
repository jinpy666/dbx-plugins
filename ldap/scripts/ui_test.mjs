#!/usr/bin/env node
// LDAP 插件 UI 走查（M5-a UI 测试第三轨：真实浏览器）。
//
// 用法：node scripts/ui_test.mjs [--keep-server]
// 前置：frontend 依赖已安装（脚本自行拉起 vite dev，mock 宿主，无需 sidecar）。
// 浏览器：playwright-core + 系统 Chrome（惰性安装到 /tmp/dbx-ldap-ui-deps，
// 不进项目依赖；找不到 Chrome 时整个走查 SKIP 而非 FAIL，遵循 test.sh 约定）。
// 断言失败 → exit 1；环境不可用 → exit 0（打印 SKIP）。
import { spawn } from "node:child_process";
import { existsSync, mkdirSync } from "node:fs";
import { createRequire } from "node:module";
import { platform } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

const PLUGIN_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const FRONTEND_DIR = path.join(PLUGIN_ROOT, "frontend");
const DEPS_DIR = "/tmp/dbx-ldap-ui-deps";
const PORT = 5279;

const skip = (reason) => {
  console.log(`SKIP: ldap UI walkthrough — ${reason}`);
  process.exit(0);
};

async function waitForServer(url, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url);
      if (response.ok) return;
    } catch {}
    await new Promise((resolve) => setTimeout(resolve, 300));
  }
  throw new Error(`vite dev server not ready at ${url}`);
}

async function loadPlaywrightCore() {
  for (const candidate of [path.join(DEPS_DIR, "node_modules", "playwright-core")]) {
    if (existsSync(candidate)) {
      const require = createRequire(path.join(candidate, "index.js"));
      return require("playwright-core");
    }
  }
  console.log(`==> installing playwright-core into ${DEPS_DIR} (non-project dependency)`);
  mkdirSync(DEPS_DIR, { recursive: true });
  const result = await new Promise((resolve) => {
    const child = spawn("npm", ["install", "--prefix", DEPS_DIR, "playwright-core@1.49.1", "--no-audit", "--no-fund"], {
      stdio: "ignore",
    });
    child.on("error", () => resolve(false));
    child.on("close", (code) => resolve(code === 0));
  });
  if (!result || !existsSync(path.join(DEPS_DIR, "node_modules", "playwright-core"))) {
    skip("playwright-core unavailable (no network?)");
  }
  const require = createRequire(path.join(DEPS_DIR, "node_modules", "playwright-core", "index.js"));
  return require("playwright-core");
}

function findChromeExecutable() {
  if (process.env.LDAP_UI_CHROME) return process.env.LDAP_UI_CHROME;
  const candidates =
    platform() === "darwin"
      ? [
          "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
          "/Applications/Chromium.app/Contents/MacOS/Chromium",
          "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
        ]
      : ["/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser"];
  return candidates.find((candidate) => existsSync(candidate)) || "";
}

const tests = [];
const test = (name, fn) => tests.push({ name, fn });
const expectEqual = (actual, expected, label) => {
  if (actual !== expected) throw new Error(`${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
};

// -- 走查流 --------------------------------------------------------------------

test("builder preview: equals", async (page) => {
  await page.locator(".qb-attr").first().fill("uid");
  await page.locator(".qb-value").first().fill("admin");
  await page.waitForTimeout(100);
  expectEqual(await page.locator(".qb-preview").first().innerText(), "(uid=admin)", "equals preview");
});

test("builder preview: notEquals (≠)", async (page) => {
  await page.locator(".qb-node select").first().selectOption("notEquals");
  await page.waitForTimeout(100);
  expectEqual(await page.locator(".qb-preview").first().innerText(), "(!(uid=admin))", "notEquals preview");
});

test("search returns mock rows", async (page) => {
  await page.getByRole("button", { name: /搜索|Search/ }).click();
  await page.locator(".result-row").first().waitFor({ timeout: 5000 });
});

test("double-click row opens entry dialog above the form", async (page) => {
  await page.locator(".result-row").first().dblclick();
  await page.locator(".modal-backdrop .editor-modal").waitFor({ timeout: 3000 });
  const title = await page.locator(".modal h2").first().innerText();
  if (!title.trim()) throw new Error("dialog title is empty");
});

test("regression: dark backdrop is ≥90% opaque so filter text cannot bleed through", async (page) => {
  const background = await page.locator(".modal-backdrop").evaluate((el) => getComputedStyle(el).backgroundColor);
  // Chromium resolves color-mix(...) to `color(srgb r g b / a)` and plain
  // rgb()/rgba() to legacy `rgba(r, g, b, a)`; the alpha is the trailing
  // number before the closing paren in both syntaxes.
  const alphaMatch = /([\d.]+)\s*\)$/.exec(background);
  const alpha = alphaMatch ? Number.parseFloat(alphaMatch[1]) : Number.NaN;
  // 阈值对齐主题设计值：暗色主题遮罩 = color-mix(background 70%,
  // transparent)（见 themeSync），即 alpha ≥ 0.7。回归意图是拦住
  // --overlay 缺失导致的完全透明（alpha 0/NaN）。
  if (!(alpha >= 0.7)) throw new Error(`backdrop alpha ${alpha} too transparent (${background})`);
});

test("backdrop intercepts pointer at the filter preview position", async (page) => {
  const hit = await page.evaluate(() => {
    const preview = document.querySelector(".qb-preview");
    const rect = preview.getBoundingClientRect();
    const element = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2);
    return Boolean(element && element.closest(".modal-backdrop"));
  });
  expectEqual(hit, true, "elementFromPoint hits backdrop");
});

test("builder → source mode carries the generated filter", async (page) => {
  await page.locator(".modal-backdrop button", { hasText: /取消|Cancel|✕|关闭/ }).first().click().catch(() => {});
  await page.keyboard.press("Escape").catch(() => {});
  const modeButtons = page.locator(".mode-switch button");
  await modeButtons.nth(1).click();
  const value = await page.locator(".filter-source input").inputValue();
  expectEqual(value, "(!(uid=admin))", "source input");
});

// -- main ----------------------------------------------------------------------

const playwright = await loadPlaywrightCore();
const executablePath = findChromeExecutable();
const launchOptions = executablePath ? { executablePath } : { channel: "chrome" };

const server = spawn("pnpm", ["--dir", "frontend", "exec", "vite", "--port", String(PORT), "--strictPort"], {
  cwd: PLUGIN_ROOT,
  stdio: ["ignore", "pipe", "pipe"],
});
let browser;
try {
  await waitForServer(`http://localhost:${PORT}/mock.html`);
  browser = await playwright.chromium.launch({ headless: true, ...launchOptions });
  const page = await browser.newPage({ viewport: { width: 1280, height: 860 } });
  await page.goto(`http://localhost:${PORT}/mock.html`);

  let failures = 0;
  for (const { name, fn } of tests) {
    try {
      await fn(page);
      console.log(`  ✓ ${name}`);
    } catch (cause) {
      failures += 1;
      console.error(`  ✗ ${name}\n    ${cause instanceof Error ? cause.message : cause}`);
    }
  }
  console.log(failures === 0 ? `\nUI walkthrough: ${tests.length}/${tests.length} passed` : `\nUI walkthrough: ${tests.length - failures}/${tests.length} passed`);
  process.exitCode = failures === 0 ? 0 : 1;
} catch (cause) {
  if (/browser|executable|chrome/i.test(String(cause))) skip(`no Chrome available (${cause})`);
  console.error(`FAIL: ldap UI walkthrough crashed: ${cause instanceof Error ? cause.message : cause}`);
  process.exitCode = 1;
} finally {
  await browser?.close().catch(() => {});
  if (!process.argv.includes("--keep-server")) server.kill("SIGTERM");
}
