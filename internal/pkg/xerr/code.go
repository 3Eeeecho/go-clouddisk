package xerr

// 定义一些常用的业务错误码 (可以根据需要扩展)
const (
	CodeSuccess            = 0    // 成功
	CodeInvalidParams      = 1001 // 参数错误
	CodeUserAlreadyExists  = 1002 // 用户已存在
	CodeEmailAlreadyExists = 1003 // 邮箱已存在
	CodeUserNotFound       = 1004 // 用户不存在
	CodeInvalidCredentials = 1005 // 用户名或密码错误

	CodeInternalServerError = 2001 // 服务器内部错误
	CodeUnauthorized        = 2002 // 未认证
	CodeForbidden           = 2003 // 无权限

	CodeResourceAlreadyExists = 3001 //文件夹已存在
	CodeNotFound              = 3002 //文件不存在
	CodeFileNotAvailable      = 3003 //文件无法获取
	CodeConflict              = 3004 //文件冲突
)
