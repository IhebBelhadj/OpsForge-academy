function isValid(s) {
  // A stack of the brackets still open: a closing one must match the most recent
  const partner = { ")": "(", "]": "[", "}": "{" };
  const stack = [];
  for (const c of s) {
    if (c in partner) {
      if (stack.pop() !== partner[c]) return false;
    } else {
      stack.push(c);
    }
  }
  return stack.length === 0;
}
