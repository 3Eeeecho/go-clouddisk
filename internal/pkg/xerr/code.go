package xerr

// 定义了统一的业务错误码
const (
	SuccessCode = 20000 // 通用成功码

	// --- 客户端请求错误系列 (400xx) ---
	InvalidParamsCode         = 40000 // 无效的请求参数
	ValidationFailedCode      = 40001 // 参数验证失败
	MethodNotAllowedCode      = 40002 // HTTP 方法不支持
	FileTooLargeCode          = 40003 // 文件过大
	FileNameInvalidCode       = 40004 // 文件名无效
	FileStatusInvalidCode     = 40006 // 文件状态异常，无法操作
	CannotMoveRootCode        = 40007 // 不能移动根目录
	CannotMoveIntoSubtreeCode = 40008 // 不能移动目录到其子目录下
	TargetNotFolderCode       = 40009 // 操作目标不是一个文件夹
	CannotDownloadFolderCode  = 40010 // 无法使用文件下载接口下载文件夹
	ChunkMissingCode          = 40011 // 上传分片丢失
	HashMismatchCode          = 40012 // 文件Hash不匹配

	// --- 认证与授权错误系列 (401xx) ---
	UnauthorizedCode       = 40100 // 通用未授权
	TokenInvalidCode       = 40101 // Token 无效或过期
	InvalidCredentialsCode = 40102 // 用户名或密码错误

	// --- 权限错误系列 (403xx) ---
	ForbiddenCode              = 40300 // 通用无权限
	PermissionDeniedCode       = 40301 // 权限不足 (细分)
	SharePasswordRequiredCode  = 40302 // 分享需要密码
	SharePasswordIncorrectCode = 40303 // 分享密码不正确

	// --- 资源未找到错误系列 (404xx) ---
	NotFoundCode              = 40400 // 通用资源未找到
	UserNotFoundCode          = 40401 // 用户不存在
	FileNotFoundCode          = 40402 // 文件不存在
	DirectoryNotFoundCode     = 40403 // 目录不存在
	ShareNotFoundCode         = 40404 // 分享链接不存在
	FileNotInRecycleBinCode   = 40405 // 文件不在回收站中
	UploadSessionNotFoundCode = 40406 // 上传会话不存在

	// --- 业务逻辑冲突系列 (409xx) ---
	UserAlreadyExistsCode  = 40900 // 用户名已存在
	EmailAlreadyExistsCode = 40901 // 邮箱已存在
	DirNotEmptyCode        = 40902 // 目录不为空，无法删除
	ShareAlreadyExistsCode = 40903 // 分享链接已存在
	FileAlreadyExistsCode  = 40904 // 文件或目录已存在

	// --- 服务器内部错误系列 (500xx) ---
	InternalServerErrorCode = 50000 // 服务器内部通用错误
	DatabaseErrorCode       = 50001 // 数据库操作失败
	StorageErrorCode        = 50002 // 存储服务操作失败（如MinIO）
	MQErrorCode             = 50003 // 消息队列操作失败
)
