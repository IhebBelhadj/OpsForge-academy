def is_valid(s):
    # A stack of the brackets still open: a closing one must match the most recent
    partner = {")": "(", "]": "[", "}": "{"}
    stack = []
    for c in s:
        if c in partner:
            if not stack or stack.pop() != partner[c]:
                return False
        else:
            stack.append(c)
    return not stack
