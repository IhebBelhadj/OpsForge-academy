// OpsForge Academy's JavaScript judge: calls the learner's function in solution.js once per case of tests.json and
// prints one marked line per case (what it returned, or the error it threw). Academy compares the results.
"use strict";
const fs = require("fs");
const vm = require("vm");

const MARK = "\x1eACADEMY ";
const report = (record) => {
  let line;
  try {
    line = JSON.stringify(record, (_, v) => (v === undefined ? null : v));
  } catch (e) {
    line = JSON.stringify({ i: record.i, error: "the result is not JSON data: " + String(e), ms: record.ms });
  }
  process.stdout.write("\n" + MARK + line + "\n");
};

const tests = JSON.parse(fs.readFileSync("tests.json", "utf8"));
const name = tests.function;
let fn;
try {
  const source = fs.readFileSync("solution.js", "utf8");
  fn = vm.runInThisContext(
    `(function () {${source}\n;return typeof ${name} === "function" ? ${name} : undefined; })()`,
    { filename: "solution.js" },
  );
} catch (e) {
  report({ compile: String((e && e.stack) || e).split("\n").slice(0, 6).join("\n") });
  process.exit(1);
}
if (typeof fn !== "function") {
  report({ compile: `solution.js defines no function ${name}` });
  process.exit(1);
}
tests.cases.forEach((c, i) => {
  const start = process.hrtime.bigint();
  let record;
  try {
    record = { i, got: fn(...c.args) };
  } catch (e) {
    record = { i, error: e instanceof Error ? `${e.name}: ${e.message}` : String(e) };
  }
  record.ms = Number(process.hrtime.bigint() - start) / 1e6;
  report(record);
});
