# OpsForge Academy's Python judge: calls the learner's function in solution.py once per case of tests.json and prints
# one marked line per case (what it returned, or the error it raised). Academy compares the results.
import json
import sys
import time
import traceback

MARK = "\x1eACADEMY "


def report(record):
    try:
        line = json.dumps(record)
    except (TypeError, ValueError):
        line = json.dumps({"i": record["i"], "error": f"the result is not JSON data: {record['got']!r}"[:500],
                           "ms": record["ms"]})
    sys.stdout.write("\n" + MARK + line + "\n")
    sys.stdout.flush()


def main():
    with open("tests.json") as f:
        tests = json.load(f)
    name = tests["function"]
    try:
        import solution
    except Exception:
        report({"compile": traceback.format_exc(limit=-1).strip()})
        return 1
    fn = getattr(solution, name, None)
    if not callable(fn):
        report({"compile": f"solution.py defines no function {name}"})
        return 1
    for i, case in enumerate(tests["cases"]):
        start = time.perf_counter()
        try:
            got = fn(*case["args"])
            record = {"i": i, "got": got}
        except Exception as e:
            frames = traceback.extract_tb(e.__traceback__)
            where = next((f"line {f.lineno}" for f in reversed(frames) if f.filename.endswith("solution.py")), "")
            record = {"i": i, "error": f"{type(e).__name__}: {e}" + (f" ({where})" if where else "")}
        record["ms"] = round((time.perf_counter() - start) * 1000, 3)
        report(record)
    return 0


sys.exit(main())
