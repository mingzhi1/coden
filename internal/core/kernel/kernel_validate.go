package kernel

import (
	"fmt"
	"strings"

	"github.com/mingzhi1/coden/internal/core/model"
	"github.com/mingzhi1/coden/internal/core/toolruntime"
)

// validateToolPath 检查 mutation 工具（write_file, edit_file）是否在工作区边界内操作。
// 对路径遍历尝试返回错误。
func validateToolPath(kind, path string) error {
	switch kind {
	case "write_file", "edit_file":
	default:
		return nil
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("tool %s: path is required", kind)
	}
	// KA-09: reject null bytes — some filesystems truncate at \x00.
	if strings.ContainsRune(path, 0) {
		return fmt.Errorf("tool %s: path contains null byte: %q", kind, path)
	}
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "\\") || (len(path) >= 2 && path[1] == ':') {
		return fmt.Errorf("tool %s: absolute path not allowed: %s", kind, path)
	}
	for _, segment := range strings.Split(strings.ReplaceAll(path, "\\", "/"), "/") {
		if segment == ".." {
			return fmt.Errorf("tool %s: path traversal not allowed: %s", kind, path)
		}
	}
	return nil
}

// validateTaskDAG 使用 Kahn 拓扑排序算法（O(V+E)）检测任务依赖图中的循环。
// 如果存在任何循环则返回错误。
func validateTaskDAG(tasks []model.Task) error {
	if len(tasks) == 0 {
		return nil
	}

	idx := make(map[string]int, len(tasks))
	for i, t := range tasks {
		if t.ID != "" {
			idx[t.ID] = i
		}
	}

	inDegree := make([]int, len(tasks))
	for i, t := range tasks {
		for _, dep := range t.DependsOn {
			if _, ok := idx[dep]; ok {
				inDegree[i]++
			}
		}
	}

	queue := make([]int, 0, len(tasks))
	for i, d := range inDegree {
		if d == 0 {
			queue = append(queue, i)
		}
	}

	visited := 0
	for len(queue) > 0 {
		curr := queue[0]
		queue = queue[1:]
		visited++
		currID := tasks[curr].ID
		if currID == "" {
			continue
		}
		for j, t := range tasks {
			for _, dep := range t.DependsOn {
				if dep == currID {
					inDegree[j]--
					if inDegree[j] == 0 {
						queue = append(queue, j)
					}
				}
			}
		}
	}

	if visited < len(tasks) {
		return fmt.Errorf("task dependency graph contains a cycle (%d/%d tasks reachable)", visited, len(tasks))
	}
	return nil
}

// validateToolScope enforces task-level file scope guards.
// When the Coder writes to a path not in the task's declared Files list,
// log a warning and auto-expand the list (soft enforcement) rather than
// aborting the workflow. This handles the common case where
// Planner/Replanner underspecify the files list.
// Empty allowedPaths means unrestricted (backward compatible).
// Returns (warning message, nil) on soft expansion, or ("", nil) on match.
func validateToolScope(req toolruntime.Request, allowedPaths []string) (string, error) {
	if len(allowedPaths) == 0 {
		return "", nil
	}
	if req.Kind != "write_file" && req.Kind != "edit_file" {
		return "", nil
	}
	path := req.Path
	for _, a := range allowedPaths {
		if a == path || strings.HasPrefix(path, strings.TrimRight(a, "/")+"/") {
			return "", nil
		}
	}
	return fmt.Sprintf("scope auto-expanded: %q was not in declared Files %v", path, allowedPaths), nil
}
