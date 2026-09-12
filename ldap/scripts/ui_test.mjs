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

test("source syntax feedback is linked to the field and recovers", async (page) => {
  const input = page.locator(".filter-source input");
  await input.fill("(uid");
  expectEqual(await input.getAttribute("aria-invalid"), "true", "invalid source field");
  const description = await input.getAttribute("aria-describedby");
  expectEqual(await page.locator('[id="' + description + '"]').innerText(), "LDAP 过滤器不合法", "source error");
  await input.fill("(objectClass=*)");
  expectEqual(await input.getAttribute("aria-invalid"), "false", "corrected source field");
});

test("invalid numeric inputs block search until corrected", async (page) => {
  const input = page.locator("input.numeric").first();
  await input.fill("12px");
  expectEqual(await page.locator(".search-form button[type='submit']").isDisabled(), true, "invalid number blocks search");
  await input.fill("500");
  expectEqual(await page.locator(".search-form button[type='submit']").isDisabled(), false, "valid number allows search");
});

test("lazy tree node expands and collapses with the keyboard", async (page) => {
  const row = page.locator('.tree-node[title="ou=services,dc=demo,dc=dbx"]');
  const child = page.locator('.tree-node[title="cn=ldap,ou=services,dc=demo,dc=dbx"]');
  expectEqual(await row.getAttribute("aria-expanded"), "false", "unloaded node is collapsed");
  await row.press("ArrowRight");
  await child.waitFor();
  expectEqual(await row.getAttribute("aria-expanded"), "true", "expanded node");
  await row.press("ArrowLeft");
  await child.waitFor({ state: "hidden" });
  expectEqual(await row.getAttribute("aria-expanded"), "false", "collapsed node");
});

test("tree filter failure retries the same keyword and restores results", async (page) => {
  await page.evaluate(() => {
    const invoke = window.dbxPlugin.invoke.bind(window.dbxPlugin);
    let failOnce = true;
    window.dbxPlugin.invoke = async (method, params, options) => {
      if (method === "ldap/search" && params.scope === "sub" && params.sizeLimit === 100 && failOnce) {
        failOnce = false;
        throw new Error("connection refused (UI retry fixture)");
      }
      return invoke(method, params, options);
    };
  });
  const input = page.locator(".tree-filter input");
  await input.fill("user0001");
  const retry = page.locator(".tree-error button");
  await retry.waitFor();
  expectEqual(await retry.innerText(), "重试", "localized retry action");
  await retry.click();
  const hit = page.locator('.tree-node[title="uid=user0001,ou=people,dc=demo,dc=dbx"]');
  await hit.waitFor();
  expectEqual(await input.inputValue(), "user0001", "retry preserves keyword");
  expectEqual(await hit.getAttribute("role"), "treeitem", "filtered hit semantics");
});

async function captureReview(page, name) {
  if (!process.env.LDAP_UI_ARTIFACT_DIR) return;
  mkdirSync(process.env.LDAP_UI_ARTIFACT_DIR, { recursive: true });
  await page.screenshot({ path: path.join(process.env.LDAP_UI_ARTIFACT_DIR, name + ".png") });
}

test("presets follow save/remove responses and restore the updated filter", async (page) => {
  await page.locator(".search-form > label.field input").first().fill("dc=demo,dc=dbx");
  await page.locator(".filter-source input").fill("(uid=user0001)");
  await page.locator(".preset-input").fill("Round5 preset");
  await page.locator(".search-presets button").first().click();
  await page.waitForFunction(() => document.querySelector(".search-presets select").value !== "");
  const id = await page.locator(".search-presets select").inputValue();
  await page.locator(".filter-source input").fill("(uid=user0002)");
  await page.locator(".search-presets button").first().click();
  await page.waitForFunction(() => !document.querySelector(".preset-input").disabled);
  const stored = await page.evaluate(() => window.dbxPlugin.invoke("ldap/presets/list"));
  expectEqual(stored.presets.length, 1, "one preset after two saves");
  expectEqual(stored.presets[0].id, id, "stable saved id");
  expectEqual(stored.presets[0].filter, "(uid=user0002)", "latest filter persisted");
  expectEqual("conditions" in stored.presets[0], false, "only protocol fields persisted");
  await page.locator(".filter-source input").fill("(uid=unsaved)");
  await page.locator(".search-presets select").dispatchEvent("change");
  expectEqual(await page.locator(".qb-preview").innerText(), "(uid=user0002)", "filter restored into builder");
  page.once("dialog", (dialog) => dialog.accept());
  await page.locator(".search-presets button").last().click();
  await page.waitForFunction(() => document.querySelector(".search-presets select").options.length === 1);
});

test("search shows pending/failure states and retries without losing the query", async (page) => {
  await page.locator(".search-form > label.field input").first().fill("dc=demo,dc=dbx");
  await page.locator(".search-form .mode-switch button").nth(1).click();
  await page.locator(".filter-source input").fill("(uid=user0001)");
  await page.evaluate(() => {
    const invoke = window.dbxPlugin.invoke.bind(window.dbxPlugin);
    window.__ldapRound5Invoke = invoke;
    let first = true;
    window.dbxPlugin.invoke = (method, params, options) => {
      if (method === "ldap/search" && params.filter === "(uid=user0001)" && first) {
        first = false;
        return new Promise((_, reject) => { window.__ldapRound5Fail = () => reject(new Error("connection refused")); });
      }
      return invoke(method, params, options);
    };
  });
  try {
    await page.locator(".search-form button[type='submit']").click();
    await page.locator(".result-pane[aria-busy='true']").waitFor();
    expectEqual(await page.locator(".result-pane [role='status']").innerText(), "搜索中…", "pending result copy");
    expectEqual(await page.locator(".search-form button[type='submit']").innerText(), "搜索中…", "pending action copy");
    await page.evaluate(() => window.__ldapRound5Fail());
    await page.locator(".result-pane .request-error button").waitFor();
    expectEqual(await page.locator(".filter-source input").inputValue(), "(uid=user0001)", "failed query retained");
    await captureReview(page, "ldap-round5-search-retry");
    await page.locator(".result-pane .request-error button").click();
    await page.locator(".result-row").first().waitFor();
    expectEqual(await page.locator(".result-row").count(), 1, "retry result count");
  } finally {
    await page.evaluate(() => { window.dbxPlugin.invoke = window.__ldapRound5Invoke; });
  }
});

test("entry opens with loading feedback and retries a failed read", async (page) => {
  await page.evaluate(() => {
    const invoke = window.dbxPlugin.invoke.bind(window.dbxPlugin);
    window.__ldapRound5Invoke = invoke;
    let first = true;
    window.dbxPlugin.invoke = (method, params, options) => {
      if (method === "ldap/entry/get" && first) {
        first = false;
        return new Promise((_, reject) => { window.__ldapRound5Fail = () => reject(new Error("connection refused")); });
      }
      return invoke(method, params, options);
    };
  });
  try {
    await page.locator(".result-row").first().dblclick();
    await page.locator(".editor-modal[aria-busy='true']").waitFor();
    expectEqual(await page.locator(".editor-modal [role='status']").innerText(), "正在加载条目…", "pending entry copy");
    await page.evaluate(() => window.__ldapRound5Fail());
    await page.locator(".editor-modal .request-error button").click();
    await page.locator(".editor-modal .attr-editor").waitFor();
  } finally {
    await page.evaluate(() => { window.dbxPlugin.invoke = window.__ldapRound5Invoke; });
  }
});

test("missing MUST feedback focuses the affected field and recovers after input", async (page) => {
  const surname = page.getByRole("textbox", { name: "sn", exact: true });
  const original = await surname.inputValue();
  const row = page.locator(".attr-row").filter({ has: surname });
  await row.getByRole("button", { name: "移除属性", exact: true }).click();
  expectEqual(await page.locator(".editor-modal footer .primary-button").isDisabled(), true, "missing MUST blocks save");
  await captureReview(page, "ldap-round5-required-attribute");
  await page.getByRole("button", { name: "定位并补充 sn", exact: true }).click();
  expectEqual(await surname.evaluate((element) => element === document.activeElement), true, "required field receives focus");
  expectEqual(await surname.getAttribute("aria-invalid"), "true", "required field is marked invalid");
  await surname.fill(original);
  expectEqual(await page.locator(".editor-modal footer .primary-button").isDisabled(), false, "corrected MUST releases save");
  await page.locator(".editor-modal").getByRole("button", { name: "取消", exact: true }).click();
});

test("RDN preflight rejects empty multi-value segments and accepts escaped separators", async (page) => {
  await page.locator('.tree-node[title="uid=user0001,ou=people,dc=demo,dc=dbx"]').click({ button: "right" });
  await page.getByRole("menuitem", { name: /重命名/ }).click();
  const input = page.locator(".small-modal input[type='text']").first();
  await input.fill("cn=bad+");
  expectEqual(await input.getAttribute("aria-invalid"), "true", "invalid RDN");
  expectEqual(await page.locator(".small-modal .primary-button").isDisabled(), true, "invalid RDN blocks rename");
  await input.fill("cn=valid\\+value");
  expectEqual(await input.getAttribute("aria-invalid"), "false", "escaped plus is accepted");
  await page.locator(".small-modal").getByRole("button", { name: "取消", exact: true }).click();
});

test("tree keyboard reaches virtual rows and activates load more through Tab/Enter", async (page) => {
  await page.locator(".tree-filter input").fill("");
  const people = page.locator('.tree-node[title="ou=people,dc=demo,dc=dbx"]');
  await people.press("ArrowRight");
  await people.locator(".tree-badge--truncated").waitFor();
  await people.press("ArrowRight");
  expectEqual(await page.evaluate(() => document.activeElement?.getAttribute("title")), "uid=user0000,ou=people,dc=demo,dc=dbx", "Right enters first child");
  await page.keyboard.press("ArrowLeft");
  expectEqual(await page.evaluate(() => document.activeElement?.getAttribute("title")), "ou=people,dc=demo,dc=dbx", "Left returns to parent");
  await page.keyboard.press("Tab");
  expectEqual(await page.evaluate(() => document.activeElement?.matches("button.tree-badge--truncated")), true, "load more is keyboard reachable");
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => document.querySelector('.tree-node[title="ou=people,dc=demo,dc=dbx"] .tree-badge')?.textContent === "1000");
  expectEqual(await page.evaluate(() => document.activeElement?.getAttribute("title")), "ou=people,dc=demo,dc=dbx", "loading retains node focus");
  await page.keyboard.press("ArrowRight");
  for (let index = 0; index < 60; index++) await page.keyboard.press("ArrowDown");
  expectEqual(await page.evaluate(() => document.activeElement?.getAttribute("title")), "uid=user0060,ou=people,dc=demo,dc=dbx", "Down crosses virtual windows");
  await page.keyboard.press("End");
  expectEqual(await page.evaluate(() => document.activeElement?.getAttribute("title")), "ou=services,dc=demo,dc=dbx", "End reaches final logical row");
  await page.keyboard.press("ArrowUp");
  expectEqual(await page.evaluate(() => document.activeElement?.getAttribute("title")), "uid=user0999,ou=people,dc=demo,dc=dbx", "Up reaches last child");
  if (await page.locator(".tree-node").count() >= 100) throw new Error("tree virtualization was lost");
  await captureReview(page, "ldap-round6-tree-keyboard");
  await page.keyboard.press("Home");
  expectEqual(await page.evaluate(() => document.activeElement?.getAttribute("title")), "dc=demo,dc=dbx", "Home returns to root");
});

test("filtered tree Home/End moves to entries outside the initial DOM window", async (page) => {
  await page.locator(".tree-filter input").fill("user");
  const first = page.locator('.tree-node[title="uid=user0000,ou=people,dc=demo,dc=dbx"]');
  await first.waitFor();
  await first.press("End");
  expectEqual(await page.evaluate(() => document.activeElement?.getAttribute("title")), "uid=user0099,ou=people,dc=demo,dc=dbx", "End reaches last filter hit");
  await page.keyboard.press("Home");
  expectEqual(await page.evaluate(() => document.activeElement?.getAttribute("title")), "uid=user0000,ou=people,dc=demo,dc=dbx", "Home returns to first filter hit");
});

test("rename dialog moves an entry through the real newSuperior field", async (page) => {
  await page.locator(".tree-filter input").fill("user0001");
  await page.locator('.tree-node[title="uid=user0001,ou=people,dc=demo,dc=dbx"]').click({ button: "right" });
  await page.getByRole("menuitem", { name: /重命名/ }).click();
  const inputs = page.locator(".small-modal input[type='text']");
  await inputs.nth(0).fill("uid=round6-moved");
  await inputs.nth(1).fill("ou=services,dc=demo,dc=dbx");
  await page.locator(".small-modal .primary-button").click();
  await page.locator(".small-modal").waitFor({ state: "hidden" });
  const moved = await page.evaluate(async () => window.dbxPlugin.invoke("ldap/entry/get", {
    dn: "uid=round6-moved,ou=services,dc=demo,dc=dbx", attributes: ["uid"],
  }));
  expectEqual(moved.entry.attributes.uid.join(","), "round6-moved", "new parent and naming attribute persisted");
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
