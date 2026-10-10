// Run from web: node --test src/lib/api-errors.test.mjs

import ts from "typescript";

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";

// Execute the real exported http(), not a copied error interpreter. Transpiling
// removes type-only imports; only the unused mock route's aliases need stubs.
const apiCode = ts.transpileModule(readFileSync(new URL("./api.ts", import.meta.url), "utf8"), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText;

const translatedErrors = [
  ["纠偏消息不能为空", "방향 수정 메시지는 비워 둘 수 없습니다"],
  ["offset / length 不能为负数", "offset / length는 음수일 수 없습니다"],
  ["offset 超出正文长度", "offset이 본문 길이를 초과했습니다"],
  ["任务不存在", "작업을 찾을 수 없습니다"],
  ["当前任务不可读取该漏洞", "현재 작업에서 이 취약점을 조회할 수 없습니다"],
  [
    "继承漏洞的流量证据只读，请到来源任务修改",
    "상속된 취약점의 트래픽 증거는 읽기 전용입니다. 원본 작업에서 수정하세요",
  ],
  [
    "finding_id 必须为独立漏洞记录 ID；不是探索节点 ID",
    "finding_id는 탐색 노드 ID가 아닌 독립 취약점 기록 ID여야 합니다",
  ],
];

function client(response, { browser = true } = {}) {
  const requests = [];
  const removedKeys = [];
  const window = { location: { href: "/findings" } };
  const document = { cookie: "untouched" };
  const exports = {};
  let jsonReads = 0;
  runInNewContext(apiCode, {
    exports,
    require(id) {
      if (id === "@/lib/mock/enabled") return { MOCK: false };
      if (id === "@/lib/mock/handler") {
        return {
          mockHandle() {
            assert.fail("real HTTP tests must not take the mock branch");
          },
        };
      }
      throw new Error(`Unexpected API runtime dependency: ${id}`);
    },
    fetch: async (...args) => {
      requests.push(args);
      return {
        status: response.status,
        ok: response.status >= 200 && response.status < 300,
        async json() {
          jsonReads++;
          return response.json();
        },
      };
    },
    ...(browser
      ? {
          window,
          document,
          localStorage: {
            getItem(key) {
              assert.equal(key, "artex_token");
              return "test-only-token";
            },
            removeItem(key) {
              removedKeys.push(key);
            },
          },
        }
      : {}),
  });
  return { http: exports.http, requests, removedKeys, window, document, jsonReads: () => jsonReads };
}

function unchangedAuth(c) {
  assert.deepEqual(c.removedKeys, []);
  assert.equal(c.document.cookie, "untouched");
  assert.equal(c.window.location.href, "/findings");
}

// These characterize existing behavior: adding display translations must not add
// retries, auth redirects, or message-dependent control flow to generic HTTP.
for (const pair of translatedErrors) {
  for (const message of pair) {
    test(`forwards display-only error without auth effects: ${message}`, async () => {
      for (const status of [400, 403, 404, 409, 500]) {
        const c = client({ status, json: () => ({ error: ` \n${message}\t ` }) });
        await assert.rejects(c.http("/findings/example", { method: "PATCH", body: "{}" }), {
          name: "Error",
          message,
        });
        assert.equal(c.requests.length, 1, "error messages must not trigger retries");
        assert.equal(c.requests[0][0], "/api/findings/example");
        assert.equal(c.requests[0][1].method, "PATCH");
        assert.equal(c.jsonReads(), 1);
        unchangedAuth(c);
      }
    });
  }
}

test("401 clears browser auth and redirects regardless of original or translated message", async () => {
  for (const message of [...translatedErrors.flat(), "unauthorized", "사용자 정의 오류"]) {
    const c = client({ status: 401, json: () => ({ error: message }) });
    await assert.rejects(c.http("/findings/example"), {
      name: "Error",
      message: "인증되지 않았습니다",
    });
    assert.deepEqual(c.removedKeys, ["artex_token"]);
    assert.equal(c.document.cookie, "artex_token=; path=/; max-age=0");
    assert.equal(c.window.location.href, "/login");
    assert.equal(c.requests.length, 1);
    assert.equal(c.jsonReads(), 0, "401 control flow must not inspect display text");
  }
});

test("401 also handles a non-JSON body and a browser-free caller", async () => {
  for (const browser of [true, false]) {
    const c = client(
      {
        status: 401,
        json: () => {
          throw new SyntaxError("not JSON");
        },
      },
      { browser },
    );
    await assert.rejects(c.http("/auth/status"), { name: "Error", message: "인증되지 않았습니다" });
    assert.equal(c.jsonReads(), 0);
    assert.equal(c.requests.length, 1);
    if (browser) assert.deepEqual(c.removedKeys, ["artex_token"]);
    else unchangedAuth(c);
  }
});

test("invalid JSON retains method, path, and status fallback without auth effects", async () => {
  for (const method of [undefined, "POST", "DELETE"]) {
    const c = client({
      status: 502,
      json: () => {
        throw new SyntaxError("not JSON");
      },
    });
    await assert.rejects(c.http("/findings/example?offset=3", method ? { method } : undefined), {
      name: "Error",
      message: `${method ?? "GET"} /findings/example?offset=3: 502`,
    });
    assert.equal(c.jsonReads(), 1);
    assert.equal(c.requests.length, 1);
    unchangedAuth(c);
  }
});

test("empty or non-string error fields retain the status fallback", async () => {
  for (const payload of [{}, { error: "" }, { error: " \n\t " }, { error: null }, { error: 401 }, null]) {
    const c = client({ status: 400, json: () => payload });
    await assert.rejects(c.http("/findings/example"), { name: "Error", message: "GET /findings/example: 400" });
    unchangedAuth(c);
  }
});

test("unknown user errors are displayed verbatim, not translated or interpreted as auth failures", async () => {
  for (const message of ["unauthorized", "사용자 정의 오류: finding_id=x", "custom error: offset=3"]) {
    const c = client({ status: 403, json: () => ({ error: message }) });
    await assert.rejects(c.http("/findings/example"), { name: "Error", message });
    unchangedAuth(c);
  }
});

test("successful JSON and 204 still bypass error interpretation", async () => {
  const payload = { error: translatedErrors[0][1], ok: true };
  const c = client({ status: 200, json: () => payload });
  assert.equal(await c.http("/findings/example"), payload);
  assert.equal(c.requests[0][1].headers.Authorization, "Bearer test-only-token");
  unchangedAuth(c);
  const empty = client({
    status: 204,
    json: () => {
      assert.fail("204 must not parse JSON");
    },
  });
  assert.equal(await empty.http("/findings/example", { method: "DELETE" }), undefined);
  assert.equal(empty.jsonReads(), 0);
  unchangedAuth(empty);
});
