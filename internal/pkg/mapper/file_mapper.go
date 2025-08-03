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

// 将 map[string]string 映射回 models.File
// 需要处理字符串到正确类型的转换，尤其是时间类型和指针
// 采用手动转换，确保类型安全，彻底解决 unmarshal 错误。
func MapToFile(dataMap map[string]string) (*models.File, error) {
	var file models.File

	// 定义一个解码钩子，用于将字符串转换为各种目标类型
	hook := func(f reflect.Type, t reflect.Type, data any) (any, error) {
		// 只处理从 string 到其他类型的转换
		if f.Kind() != reflect.String {
			return data, nil
		}

		// 获取源字符串
		sourceString := data.(string)

		// 如果源字符串为空，对于指针类型应为 nil，对于值类型应为其零值
		if sourceString == "" {
			if t.Kind() == reflect.Ptr {
				return nil, nil // 返回 nil 指针
			}
			// 对于非指针类型，返回其零值
			return reflect.Zero(t).Interface(), nil
		}

		// 根据目标类型进行转换
		switch t {
		case reflect.TypeOf(time.Time{}):
			return time.Parse(time.RFC3339Nano, sourceString)

		case reflect.TypeOf(gorm.DeletedAt{}):
			parsedTime, err := time.Parse(time.RFC3339Nano, sourceString)
			if err != nil {
				return nil, err
			}
			return gorm.DeletedAt{Time: parsedTime, Valid: true}, nil
		}

		// 处理所有数值类型和指针数值类型
		switch t.Kind() {
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			return strconv.ParseUint(sourceString, 10, 64)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return strconv.ParseInt(sourceString, 10, 64)
		case reflect.Ptr:
			// 处理指针类型的数值，例如 *uint64
			switch t.Elem().Kind() {
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				val, err := strconv.ParseUint(sourceString, 10, 64)
				if err != nil {
					return nil, err
				}
				// 需要返回一个指向该值的指针，但类型要匹配
				// 例如，如果目标是 *uint64，我们需要返回一个 *uint64
				// 使用反射来创建正确类型的指针
				ptr := reflect.New(t.Elem())
				ptr.Elem().SetUint(val)
				return ptr.Interface(), nil
			}
		}

		// 其他类型保持默认转换行为
		return data, nil
	}

	// 配置解码器
	config := &mapstructure.DecoderConfig{
		Result:  &file,
		TagName: "json", // 使用 'json' 标签来匹配 map 的键和结构体字段
		DecodeHook: mapstructure.ComposeDecodeHookFunc(
			// 可以组合多个钩子，这里用一个就够了
			hook,
		),
		// 当 map 中的 key 在 struct 中找不到时，返回错误
		// 这有助于发现字段名不匹配的问题
		ErrorUnused: false,
	}

	decoder, err := mapstructure.NewDecoder(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create map decoder: %w", err)
	}

	// 执行解码
	if err := decoder.Decode(dataMap); err != nil {
		return nil, fmt.Errorf("failed to decode map to File struct: %w", err)
	}

	return &file, nil
}
