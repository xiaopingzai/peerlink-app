package main

// 兼容 Go 1.20（Win7 仍可用的最后一个工具链大版本）：
// Go 1.21 才内置的 min / max / clear 在这里给出等价实现，
// 让同一份引擎代码能同时被新工具链（UI/现代 CLI）和 1.20 旧工具链（Win7 CLI）编译。
// time.Duration 底层是 int64，被 ~int64 覆盖。

type orderedNum interface {
	~int | ~int64
}

func loMin[T orderedNum](a, b T) T {
	if a < b {
		return a
	}
	return b
}

func loMax[T orderedNum](a, b T) T {
	if a > b {
		return a
	}
	return b
}

func clearMap[K comparable, V any](m map[K]V) {
	for k := range m {
		delete(m, k)
	}
}
