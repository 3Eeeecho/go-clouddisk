package xerr

import "errors"

var (
	ErrFileNotFound               = errors.New("file or directory not found")
	ErrFileUnavailable            = errors.New("file or directory is not in an operable state")
	ErrNotADirectory              = errors.New("target is not a directory")
	ErrFileNotFoundOrAccessDenied = errors.New("file or folder not found or access denied")
	ErrParentFolderNotFound       = errors.New("target parent folder not found or is not a folder")
	ErrCannotMoveToSelfOrSub      = errors.New("cannot move a file/folder to itself or its own subdirectory")
	ErrNameConflict               = errors.New("a file or folder with the same name already exists in the target location")
	ErrInvalidFileName            = errors.New("invalid file or folder name")
	ErrDatabaseTransaction        = errors.New("database transaction failed")

	// redis
	ErrCacheMiss = errors.New("缓存未命中,key不存在")
)
