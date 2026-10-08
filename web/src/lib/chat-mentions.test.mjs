import { activeMention, mentionKinds, mentionSearch, mentionToken, selectedMentions } from "./chat-mentions.ts";
import assert from "node:assert/strict";
import test from "node:test";

test("Korean display labels and search keep Chinese wire tokens compatible", () => {
  const labels = ["취약점", "자산", "기업", "엔드포인트", "IP", "앱", "도메인", "서브도메인", "서비스"];
  const tokens = ["漏洞", "资产", "企业", "接口", "IP", "应用", "域名", "子域名", "服务"];
  assert.deepEqual(
    mentionKinds.map((item) => item.label),
    labels,
  );
  for (const [index, item] of mentionKinds.entries()) {
    for (const alias of [labels[index], tokens[index], item.alias]) {
      assert.equal(mentionSearch(`${alias} example`).kind, item.kind);
      assert.equal(mentionSearch(`${alias} example`).query, "example");
    }
    const token = mentionToken({ kind: item.kind, id: 42, label: "테스트", description: "" });
    assert.equal(token, `@[${tokens[index]}#42 테스트]`);
    assert.equal(selectedMentions(token)[0].kind, item.kind);
  }
  assert.equal(mentionSearch("취").categories[0].kind, "finding");
  assert.equal(mentionSearch("취약점SQL").query, "SQL");
  assert.equal(mentionToken({ kind: "unknown", id: 1, label: "테스트" }), "@[资产#1 테스트]");
});

test("mention trigger supports Chinese and cursor placement without hijacking email", () => {
  assert.equal(activeMention("user@example.com", 16), null);
  assert.equal(activeMention("已选 @[漏洞#1 X]", 12), null);
  assert.deepEqual(activeMention("查看@漏洞 后面的文字", 5), { start: 2, end: 5, query: "漏洞" });
  assert.equal(activeMention("@漏洞\n下一行", 8), null);
});

test("categories, Chinese aliases, IP and keyword search", () => {
  assert.equal(mentionSearch("").categories.length, 9);
  assert.equal(mentionSearch("漏").categories[0].kind, "finding");
  assert.equal(mentionSearch("漏洞").kind, "finding");
  assert.equal(mentionSearch("漏洞SQL注入").query, "SQL注入");
  assert.equal(mentionSearch("ip 192.0.2.1").kind, "ip");
  assert.equal(mentionSearch("接口 GET /api").query, "GET /api");
  assert.equal(mentionSearch("acme.com").kind, "");
});

test("tokens roundtrip labels and removing one reference preserves its neighbors", () => {
  const first = mentionToken({ kind: "finding", id: 12, label: "标题[1]\n描述" });
  const second = mentionToken({ kind: "ip", id: 13, label: "192.0.2.1" });
  const value = `分析 ${first} 和 ${second}`;
  const selected = selectedMentions(value);
  assert.equal(selected.length, 2);
  assert.equal(selected[0].kind, "finding");
  assert.equal(selected[0].id, "12");
  assert.equal(selected[0].text, "标题（1） 描述");
  assert.equal(selected[1].kind, "ip");
  const next = value.slice(0, selected[0].start) + value.slice(selected[0].start + selected[0].token.length);
  assert.equal(selectedMentions(next)[0].token, second);
});
