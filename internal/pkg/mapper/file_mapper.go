package mapper

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/3Eeeecho/go-clouddisk/internal/models"
	"github.com/go-viper/mapstructure/v2"
	"gorm.io/gorm"
)

// 将models.File转换成map[string]any类型
func FileToMap(file *models.File) (map[string]any, error) {
	// 使用 json.Marshal 和 json.Unmarshal 是一个将 struct 转换为 map 的高效技巧
	data, err := json.Marshal(file)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}

	// 为确保 Redis 中存储的是可预测的格式，我们手动处理特殊类型。
	// 虽然很多客户端会自动转换，但显式处理更安全。
	if file.CreatedAt.IsZero() {
		result["created_at"] = ""
	} else {
		result["created_at"] = file.CreatedAt.Format(time.RFC3339Nano)
	}

	if file.UpdatedAt.IsZero() {
		result["updated_at"] = ""
	} else {
		result["updated_at"] = file.UpdatedAt.Format(time.RFC3339Nano)
	}

	if file.DeletedAt.Valid {
		result["deleted_at"] = file.DeletedAt.Time.Format(time.RFC3339Nano)
	} else {
		// 如果 DeletedAt 无效，json omitempty 可能会直接移除该字段
		// 确保它存在且为空字符串，以保持字段统一
		result["deleted_at"] = ""
	}

	// 对于指针类型，如果为 nil，json.Marshal 会将其变为 null。
	// 需要确保它们在 map 中，以便后续转换，或者直接在这里处理成空字符串。
	// json marshal 的默认行为通常是可接受的。

	return result, nil
}

// stringToTimeHookFunc 创建一个 mapstructure 解码钩子，用于将字符串转换为时间类型。
func stringToTimeHookFunc() mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		if f.Kind() != reflect.String {
			return data, nil
		}
		s := data.(string)
		if s == "" {
			// 对于空字符串，返回类型的零值
			return reflect.Zero(t).Interface(), nil
		}

		switch t {
		case reflect.TypeOf(time.Time{}):
			return time.Parse(time.RFC3339Nano, s)
		case reflect.TypeOf(gorm.DeletedAt{}):
			parsedTime, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				return nil, err
			}
			return gorm.DeletedAt{Time: parsedTime, Valid: true}, nil
		}

		return data, nil
	}
}

// stringToNumericHookFunc 创建一个解码钩子，用于将字符串转换为所有数值类型（包括指针）。
func stringToNumericHookFunc() mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		if f.Kind() != reflect.String {
			return data, nil
		}
		sourceString := data.(string)

		// 如果字符串为空，指针类型应为 nil，值类型为其零值
		if sourceString == "" {
			if t.Kind() == reflect.Ptr {
				return nil, nil // 返回 nil 指针
			}
			return reflect.Zero(t).Interface(), nil
		}

		// 统一处理指针和非指针类型
		targetType := t
		isPtr := t.Kind() == reflect.Ptr
		if isPtr {
			targetType = t.Elem()
		}

		var result any
		var err error

		switch targetType.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			val, parseErr := strconv.ParseUint(sourceString, 10, 64)
			if parseErr == nil {
				// 使用反射来设置正确大小的 uint
				newVal := reflect.New(targetType).Elem()
				newVal.SetUint(val)
				result = newVal.Interface()
			}
			err = parseErr
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			val, parseErr := strconv.ParseInt(sourceString, 10, 64)
			if parseErr == nil {
				// 使用反射来设置正确大小的 int
				newVal := reflect.New(targetType).Elem()
				newVal.SetInt(val)
				result = newVal.Interface()
			}
			err = parseErr
		default:
			// 如果不是目标数值类型，则不处理
			return data, nil
		}

		if err != nil {
			return nil, err
		}

		if isPtr {
			// 如果原始目标类型是指针，则返回一个指向新值的指针
			ptr := reflect.New(targetType)
			ptr.Elem().Set(reflect.ValueOf(result))
			return ptr.Interface(), nil
		}

		return result, nil
	}
}

// 将 map[string]string 映射回 models.File
func MapToFile(dataMap map[string]string) (*models.File, error) {
	var file models.File

	config := &mapstructure.DecoderConfig{
		Result:      &file,
		TagName:     "json",
		ErrorUnused: false,
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			stringToTimeHookFunc(),
			stringToNumericHookFunc(),
		),
	}

	decoder, err := mapstructure.NewDecoder(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create map decoder: %w", err)
	}

	if err := decoder.Decode(dataMap); err != nil {
		return nil, fmt.Errorf("failed to decode map to File struct: %w", err)
	}

	return &file, nil
}
