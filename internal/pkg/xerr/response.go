package xerr

import (
	"errors"
)

// CodeError 结构体用于在服务层传递带有业务码的错误
// 它实现了 error 接口
type CodeError struct {
	Code int   // 业务错误码
	Err  error // 被包裹的底层错误
}

// Error 实现 error 接口
func (e *CodeError) Error() string {
	return e.Err.Error()
}

// Unwrap 返回被包裹的底层错误，支持 errors.Unwrap
func (e *CodeError) Unwrap() error {
	return e.Err
}

// NewCodeError 创建一个 CodeError 实例
func NewCodeError(code int, err error) *CodeError {
	return &CodeError{Code: code, Err: err}
}

// Is 判断错误是否为指定的错误类型
// 如果 err 是 *CodeError，则会解包后与 target 比较
func Is(err, target error) bool {
	return errors.Is(err, target)
}
