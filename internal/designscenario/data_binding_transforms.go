package designscenario

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

var (
	bindingJSONNumber  = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
	bindingJSONInteger = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
)

// ProjectBindingType returns the type after an ordered transformation chain.
// An unknown source is allowed through the chain for analysis, while runtime
// still checks every concrete value. Callers should report that uncertainty.
func ProjectBindingType(sourceType string, transforms []DataBindingTransform) (string, error) {
	if len(transforms) > MaxBindingTransforms {
		return "", fmt.Errorf("допускается не более %d преобразований", MaxBindingTransforms)
	}
	current := sourceType
	for i, transform := range transforms {
		if !validBindingTransformKind(transform.Kind) {
			return "", fmt.Errorf("неизвестное преобразование %d", i+1)
		}
		if current != "unknown" && !bindingTransformAccepts(transform.Kind, current) {
			return "", fmt.Errorf("преобразование %d (%s) не принимает тип %s", i+1, transform.Kind, current)
		}
		current = bindingTransformResultType(transform.Kind)
	}
	return current, nil
}

func bindingTransformAccepts(kind, typ string) bool {
	switch kind {
	case "trim", "lower", "upper", "to_number", "to_integer":
		return typ == "string"
	case "to_string":
		return typ == "string" || typ == "number" || typ == "integer" || typ == "boolean"
	}
	return false
}

func bindingTransformResultType(kind string) string {
	switch kind {
	case "to_number":
		return "number"
	case "to_integer":
		return "integer"
	default:
		return "string"
	}
}

func applyBindingTransforms(value any, transforms []DataBindingTransform) (any, string, error) {
	if len(transforms) > MaxBindingTransforms {
		return nil, "", fmt.Errorf("допускается не более %d преобразований", MaxBindingTransforms)
	}
	currentType := bindingValueType(value)
	for i, transform := range transforms {
		if !validBindingTransformKind(transform.Kind) || !bindingTransformAccepts(transform.Kind, currentType) {
			return nil, "", fmt.Errorf("преобразование %d (%s) не принимает тип %s", i+1, transform.Kind, currentType)
		}
		switch transform.Kind {
		case "trim":
			value = strings.TrimSpace(value.(string))
		case "lower":
			value = strings.ToLower(value.(string))
		case "upper":
			value = strings.ToUpper(value.(string))
		case "to_string":
			switch v := value.(type) {
			case jsonx.Number:
				value = string(v)
			case bool:
				value = strconv.FormatBool(v)
			}
		case "to_number", "to_integer":
			text := value.(string)
			valid := bindingJSONNumber.MatchString(text)
			if transform.Kind == "to_integer" {
				valid = bindingJSONInteger.MatchString(text)
			}
			if !valid || !jsonx.Valid([]byte(text)) {
				return nil, "", fmt.Errorf("преобразование %d (%s): некорректное число JSON", i+1, transform.Kind)
			}
			value = jsonx.Number(text)
		}
		currentType = bindingTransformResultType(transform.Kind)
		encoded, err := jsonx.Marshal(value)
		if err != nil || len(encoded) > MaxExecutionBody {
			return nil, "", fmt.Errorf("результат преобразования превышает допустимый размер")
		}
	}
	return value, currentType, nil
}
