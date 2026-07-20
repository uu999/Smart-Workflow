package varpool

import (
	"fmt"
	"strconv"
	"strings"
)

// token 是路径中的一个访问步骤：字段名或数组下标。
type token struct {
	key   string // 字段名（isIndex=false 时有效）
	index int    // 数组下标（isIndex=true 时有效）
	isIdx bool
}

// parsePath 把 "result.segments[0].text" 解析成 token 序列：
// [field:result, field:segments, index:0, field:text]。
// 支持形如 a.b、a[0]、a.b[0].c 的混合写法。
func parsePath(path string) ([]token, error) {
	if path == "" {
		return nil, fmt.Errorf("empty path")
	}
	var tokens []token
	// 先按 '.' 分段，再在每段内解析尾部的 [i][j]...
	for _, seg := range strings.Split(path, ".") {
		if seg == "" {
			return nil, fmt.Errorf("invalid path %q: empty segment", path)
		}
		name := seg
		if b := strings.IndexByte(seg, '['); b >= 0 {
			name = seg[:b]
		}
		if name != "" {
			tokens = append(tokens, token{key: name})
		} else if !strings.HasPrefix(seg, "[") {
			return nil, fmt.Errorf("invalid path %q", path)
		}
		// 解析该段中所有 [i]
		rest := seg[len(name):]
		for len(rest) > 0 {
			if rest[0] != '[' {
				return nil, fmt.Errorf("invalid path %q near %q", path, rest)
			}
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil, fmt.Errorf("invalid path %q: missing ']'", path)
			}
			idxStr := rest[1:end]
			idx, err := strconv.Atoi(idxStr)
			if err != nil || idx < 0 {
				return nil, fmt.Errorf("invalid path %q: bad index %q", path, idxStr)
			}
			tokens = append(tokens, token{index: idx, isIdx: true})
			rest = rest[end+1:]
		}
	}
	return tokens, nil
}

// walkPath 从 root 出发按 tokens 逐步取值。
// map 只接受 map[string]any；数组接受 []any（JSON 反序列化的通用形态）。
// 任一步类型不符或越界，返回明确错误，供校验/调试定位。
func walkPath(root any, tokens []token) (any, error) {
	cur := root
	for i, t := range tokens {
		if t.isIdx {
			arr, ok := cur.([]any)
			if !ok {
				return nil, fmt.Errorf("path step %d: expected array, got %T", i, cur)
			}
			if t.index >= len(arr) {
				return nil, fmt.Errorf("path step %d: index %d out of range (len %d)", i, t.index, len(arr))
			}
			cur = arr[t.index]
			continue
		}
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("path step %d: expected object for key %q, got %T", i, t.key, cur)
		}
		v, exists := m[t.key]
		if !exists {
			return nil, fmt.Errorf("path step %d: key %q not found", i, t.key)
		}
		cur = v
	}
	return cur, nil
}
