package xerr

import "errors"

var (
	ErrFileNotFound    = errors.New("file or directory not found")
	ErrFileUnavailable = errors.New("file or directory is not in an operable state")
	ErrNotADirectory   = errors.New("target is not a directory")
)
