// Run: ./node_modules/.bin/ts-node -P tsconfig.scripts.json src/scripts/test-korean-ui.ts

import ts from "typescript";

import * as localeConfig from "../i18n/config";
import { sidebarItems } from "../navigation/sidebar/sidebar-items";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import test from "node:test";
import { runInNewContext } from "node:vm";

const root = resolve(__dirname, "../..");
const messages = JSON.parse(readFileSync(resolve(root, "messages/ko.json"), "utf8"));
const han = /\p{Script=Han}/u;

function source(path: string) {
  return readFileSync(resolve(root, path), "utf8");
}

// Execute the actual config (including locale resolution), not a copy of its values.
function metadata(locale?: string) {
  const exports = {} as { APP_CONFIG: { meta: { title: string; description: string } } };
  const code = ts.transpileModule(source("src/config/app-config.ts"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, esModuleInterop: true },
  }).outputText;
  const previous = process.env.NEXT_PUBLIC_LOCALE;
  if (locale === undefined) delete process.env.NEXT_PUBLIC_LOCALE;
  else process.env.NEXT_PUBLIC_LOCALE = locale;
  try {
    runInNewContext(code, {
      exports,
      require: (id: string) => {
        if (id === "@/i18n/config") return localeConfig;
        if (id === "../../package.json") return JSON.parse(source("package.json"));
        throw new Error(`Unexpected config dependency: ${id}`);
      },
    });
    return exports.APP_CONFIG.meta;
  } finally {
    if (previous === undefined) delete process.env.NEXT_PUBLIC_LOCALE;
    else process.env.NEXT_PUBLIC_LOCALE = previous;
  }
}

// Read literal demo fixtures with the TS AST; do not import a client page and its hooks.
function demoLogTexts() {
  const file = ts.createSourceFile(
    "page.tsx",
    source("src/app/(main)/system/logs/page.tsx"),
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TSX,
  );
  const texts: string[] = [];
  function visit(node: ts.Node) {
    if (ts.isPropertyAssignment(node) && node.name.getText(file) === "text" && ts.isStringLiteral(node.initializer)) {
      texts.push(node.initializer.text);
    }
    ts.forEachChild(node, visit);
  }
  visit(file);
  return texts;
}

test("demo logs use Korean narrative without changing raw technical examples", () => {
  const texts = demoLogTexts();
  assert.equal(texts.length, 6);
  for (const text of texts) assert.equal(han.test(text), false, text);
  assert.equal(texts[0], "ARTEX v0.1.0 backend listening on :8787 (workers=3)");
  assert.equal(texts[1], "LLM configured from DB: anthropic / claude-opus-4-8");
  assert.match(texts[2], /t-acme-web.*3차.*i-4/);
  assert.match(texts[3], /block bash.*out\.evil\.example.*scope/);
  assert.match(texts[4], /report_finding.*Default Credentials \(high\)/);
  assert.match(texts[5], /intercept.*mysqldump.*승인/);
});

test("Korean metadata is already selected by default and invalid locale fallback", () => {
  for (const locale of [undefined, "ko", "invalid"]) {
    const meta = metadata(locale);
    assert.equal(meta.title, "ARTEX: 자율 침투 테스트 콘솔");
    assert.equal(meta.description, "LLM 기반으로 자율 침투 테스트를 수행하는 시스템 콘솔");
    assert.equal(han.test(meta.title + meta.description), false);
  }
  assert.equal(metadata("zh").title, "ARTEX — 自主渗透测试控制台");
  assert.equal(han.test(JSON.stringify(messages)), false);
});

test("sidebar fallback labels match Korean translation messages", () => {
  assert.equal(sidebarItems.length, 2);
  for (const group of sidebarItems) {
    assert.equal(group.label, messages.nav.group[group.id === 1 ? "function" : "system"]);
    for (const item of group.items) {
      assert.equal(item.title, messages.nav.item[item.id], `fallback for ${item.id}`);
    }
  }
});
