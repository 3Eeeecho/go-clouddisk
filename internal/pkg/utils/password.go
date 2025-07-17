package utils

import (
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		logger.Error("Error hashing password", zap.Error(err))
		return "", err
	}
	return string(bytes), nil
}

func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		logger.Error("incorrect passwordHash")
		return false
	}
	return true
}
