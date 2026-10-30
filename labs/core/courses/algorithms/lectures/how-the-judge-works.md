---
title: How the judge works
minutes: 3
---
Each problem asks for **one function**. You write it in Python, JavaScript or Go; the name follows the language:
`two_sum` in Python, `twoSum` in JavaScript, `TwoSum` in Go. Keep the parameters the starter code gives you.

**Run** calls your function on the examples of the statement and shows, for each one, the input, the expected
answer and what your function returned. **Submit** calls it on every test case, the hidden ones too, and records a
verdict:

| Verdict | Meaning |
|---|---|
| Accepted | every case returned the expected answer |
| Wrong answer | a case returned something else (the case is shown) |
| Runtime error | your function raised an exception, or the program crashed |
| Compile error | the code does not parse or compile (the errors are shown) |
| Time limit exceeded | the program ran longer than the problem allows |
| Memory limit exceeded | the program used more memory than its container has |

What you print goes to the output panel, so `print` and `console.log` work for debugging; it does not change the
verdict.

Your code runs in the lab, in a Kubernetes pod of its own: no network, no access to the cluster, a read-only file
system and limits on CPU, memory and time. The first accepted submission of a problem earns its XP.
