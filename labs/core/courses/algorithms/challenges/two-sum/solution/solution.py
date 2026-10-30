def two_sum(nums, target):
    # One pass: for each number, look up the one it needs among those already seen
    seen = {}
    for i, n in enumerate(nums):
        if target - n in seen:
            return [seen[target - n], i]
        seen[n] = i
    return []
