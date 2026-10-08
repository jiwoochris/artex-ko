import assert from "node:assert/strict";
import test from "node:test";
import * as editor from "./tool-parameter-editor.ts";

const schema = {
  type: "object",
  description: "内部说明",
  required: ["entries"],
  properties: {
    name: { type: "string", description: "名称", default: "原值" },
    entries: {
      type: "array", description: "条目", items: {
        type: "object", required: ["name"], properties: {
          name: { type: "string", description: "子名称" },
          count: { type: "integer", default: 2 },
        },
      },
    },
  },
};
const displaySchema = structuredClone(schema);
displaySchema.description = "표시 전용 요약";
displaySchema.properties.name.description = "이름";
displaySchema.properties.entries.description = "항목";
displaySchema.properties.entries.items.properties.name.description = "하위 이름";

function displayed(row, raw = schema, display = displaySchema) {
  assert.equal(typeof editor.paramDescription, "function", "parameter display overlay is available");
  return editor.paramDescription(row, raw, display);
}

test("maps display descriptions by parameter path without changing raw rows", () => {
  const rows = editor.toRows(schema);
  assert.deepEqual(rows.map((r) => displayed(r)), ["이름", "항목", "하위 이름", ""]);
  assert.deepEqual(rows.map((r) => r.description), ["名称", "条目", "子名称", ""]);
  assert.equal(rows[2].parentKey, "entries");
  assert.equal(rows[2].required, true);
});

test("unchanged save preserves the exact raw schema, including omitted descriptions and null defaults", () => {
  const raw = structuredClone(schema);
  raw.properties.name.default = null;
  const before = structuredClone(raw);
  const rows = editor.toRows(raw);
  rows.forEach((row) => displayed(row, raw));
  const saved = editor.applyRows(raw, rows);
  assert.deepEqual(saved, before);
  assert.deepEqual(raw, before);
  assert.notEqual(saved, raw);
  assert.equal(saved.description, "内部说明");
  assert.ok(!JSON.stringify(saved).includes("하위 이름"));
});

test("user edits, including empty descriptions, render and save verbatim", () => {
  const rows = editor.toRows(schema);
  rows[0].description = "내 사용자 설명";
  rows[2].description = "";
  rows[3].defaultStr = "7";
  assert.equal(displayed(rows[0]), "내 사용자 설명");
  assert.equal(displayed(rows[2]), "");
  const saved = editor.applyRows(schema, rows);
  assert.equal(saved.properties.name.description, "내 사용자 설명");
  assert.equal(saved.properties.entries.items.properties.name.description, "");
  assert.equal(saved.properties.entries.items.properties.count.default, 7);
  assert.equal(saved.properties.entries.description, "条目");
});

test("missing or partial display schema falls back to custom/raw descriptions", () => {
  const rows = editor.toRows(schema);
  assert.equal(editor.paramDescription(rows[0], schema), "名称");
  const custom = { properties: { name: { type: "string", description: "用户自定义说明" } } };
  const customRows = editor.toRows(custom);
  assert.equal(editor.paramDescription(customRows[0], custom), "用户自定义说明");
  assert.deepEqual(editor.applyRows(custom, customRows), custom);
  assert.equal(editor.paramDescription(rows[2], schema, {}), "子名称");
  assert.equal(editor.paramDescription(rows[0], schema, { properties: { name: { description: null } } }), "名称");
  assert.deepEqual(editor.applyRows(schema, rows), schema);
});

test("fresh rows after tool switch or reset use that tool's original raw schema", () => {
  const edited = editor.toRows(schema);
  edited[0].description = "편집 중";
  const other = { properties: { name: { type: "string", description: "다른 도구" } } };
  const otherRows = editor.toRows(other);
  assert.equal(editor.paramDescription(otherRows[0], other), "다른 도구");
  assert.deepEqual(editor.applyRows(other, otherRows), other);
  const resetRows = editor.toRows(schema);
  assert.equal(displayed(resetRows[0]), "이름");
  assert.deepEqual(editor.applyRows(schema, resetRows), schema);
});
