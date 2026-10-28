import { test } from "node:test";
import assert from "node:assert/strict";
import { apiPath } from "../src/server.ts";

test("API paths stay within the same-origin API", () => {
  assert.equal(apiPath("/entries?page=2"), "/api/entries?page=2");
  assert.equal(apiPath("/releases/id/download"), "/api/releases/id/download");
  assert.equal(apiPath("/entries?q=美香"), "/api/entries?q=%E7%BE%8E%E9%A6%99");
  for (const path of ["//evil.test", "/../admin", "/%2e%2e/admin", "/\\\\evil.test", "https://evil.test", "/entries#x"]) {
    assert.throws(() => apiPath(path), path);
  }
});

