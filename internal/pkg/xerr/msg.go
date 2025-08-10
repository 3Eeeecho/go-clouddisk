package xerr

import "errors"

var (
	// 通用错误
	ErrSuccess        = errors.New("操作成功")
	ErrInternalServer = errors.New("服务器内部错误")

	// 客户端请求错误
	ErrInvalidParams         = errors.New("无效的请求参数")
	ErrValidationFailed      = errors.New("参数验证失败")
	ErrFileTooLarge          = errors.New("上传文件过大，超出限制")
	ErrFileNameInvalid       = errors.New("文件名包含非法字符")
	ErrFileStatusInvalid     = errors.New("文件状态异常，无法执行操作")
	ErrCannotMoveRoot        = errors.New("不能移动根目录")
	ErrCannotMoveIntoSubtree = errors.New("不能移动目录到其子目录下")
	ErrTargetNotFolder       = errors.New("操作目标不是一个文件夹")
	ErrCannotDownloadFolder  = errors.New("无法下载文件夹，请使用文件夹下载接口")
	ErrChunkMissing          = errors.New("部分上传分片丢失，请重新上传")
	ErrHashMismatch          = errors.New("文件哈希值校验失败")

	// 认证与授权错误
	ErrUnauthorized       = errors.New("用户未授权")
	ErrTokenInvalid       = errors.New("认证 Token 无效或已过期")
	ErrInvalidCredentials = errors.New("用户名或密码不正确")
	ErrUserAlreadyExists  = errors.New("该用户名已被注册")
	ErrEmailAlreadyExists = errors.New("邮箱已被注册")

	// 权限错误
	ErrForbidden              = errors.New("禁止访问")
	ErrPermissionDenied       = errors.New("您没有操作此资源的权限")
	ErrSharePasswordRequired  = errors.New("分享链接需要密码")
	ErrSharePasswordIncorrect = errors.New("分享链接密码不正确")

	// 缓存错误系列(402xx)
	ErrEmptyCache = errors.New("缓存为空")

	// 资源未找到错误
	ErrUserNotFound          = errors.New("用户不存在")
	ErrFileNotFound          = errors.New("文件不存在")
	ErrDirectoryNotFound     = errors.New("目录不存在")
	ErrShareNotFound         = errors.New("分享链接不存在或已过期")
	ErrFileNotInRecycleBin   = errors.New("文件不在回收站中")
	ErrUploadSessionNotFound = errors.New("上传会话不存在或已过期")

	// 业务逻辑冲突
	ErrDirNotEmpty        = errors.New("目录不为空，无法删除")
	ErrShareAlreadyExists = errors.New("该文件已存在有效的分享链接")
	ErrFileAlreadyExists  = errors.New("文件或目录已存在")

	// 数据库与外部服务错误
	ErrDatabaseError = errors.New("数据库操作失败")
	ErrStorageError  = errors.New("存储服务操作失败")
	ErrMQError       = errors.New("消息队列操作失败")
)
