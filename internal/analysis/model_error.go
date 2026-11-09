// input: Python 返回的固定错误类别、HTTP 状态与请求标识。
// output: 经白名单过滤的可持久化错误和结构化日志字段。
// pos: Bub 总结与知识共享错误边界。一旦我被更新务必更新我的开头注释以及所属文件夹的 AGENTS.md。
package analysis

import (
	"fmt"
	"regexp"
)

type ModelError struct {
	Code       string
	HTTPStatus int
	RequestID  string
}

func (e *ModelError) Error() string {
	s := "Bub: " + e.Code
	if e.HTTPStatus != 0 {
		s += fmt.Sprintf("; HTTP %d", e.HTTPStatus)
	}
	if e.RequestID != "" {
		s += "; request_id=" + e.RequestID
	}
	return s
}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,96}$`)

func modelError(code string, status int, id string) error {
	if status < 100 || status > 599 {
		status = 0
	}
	if !requestIDPattern.MatchString(id) {
		id = ""
	}
	return &ModelError{Code: code, HTTPStatus: status, RequestID: id}
}
