package main

import "sort"

// Merge sorts by start: an interval can then only overlap the last merged one.
func Merge(intervals [][]int) [][]int {
	sort.Slice(intervals, func(i, j int) bool { return intervals[i][0] < intervals[j][0] })
	var merged [][]int
	for _, iv := range intervals {
		if n := len(merged); n > 0 && iv[0] <= merged[n-1][1] {
			merged[n-1][1] = max(merged[n-1][1], iv[1])
		} else {
			merged = append(merged, []int{iv[0], iv[1]})
		}
	}
	return merged
}
