package cache

import "errors"

// ErrMiss 表示键不存在（缓存未命中）。调用方据此走回源逻辑，而非当错误处理。
var ErrMiss = errors.New("cache: key not found")

// 键前缀（设计文档 M9-a）。集中定义，避免散落拼串导致命名漂移。
const (
	prefixDef  = "swf:def:"  // 定义缓存：workflow 某版本的 DSL 原文
	prefixPlan = "swf:plan:" // 引擎缓存：构建好的执行计划（可序列化时）
	prefixRun  = "swf:run:"  // 运行态镜像：run 状态快照
)

// DefKey 返回某 workflow 某版本 DSL 的定义缓存键。
func DefKey(workflowID string, version int32) string {
	return prefixDef + workflowID + ":" + itoa(int(version))
}

// PlanKey 返回某 workflow 某版本执行计划的引擎缓存键。
func PlanKey(workflowID string, version int32) string {
	return prefixPlan + workflowID + ":" + itoa(int(version))
}

// RunKey 返回某 run 运行态镜像键。
func RunKey(runID string) string {
	return prefixRun + runID
}

// itoa 处理负版本号（-1=草稿）而不依赖 strconv 的额外 import 语义。
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [12]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
