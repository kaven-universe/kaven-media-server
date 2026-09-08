package auth

import (
	"errors"
	"strings"
)

func parseDigestAuthorization(header string) (map[string]string, error) {
	scheme, rest, found := strings.Cut(strings.TrimSpace(header), " ")
	if !found || !strings.EqualFold(scheme, "Digest") {
		return nil, errors.New("authorization scheme is not Digest")
	}
	parameters := make(map[string]string)
	input := strings.TrimSpace(rest)
	for len(input) > 0 {
		nameEnd := strings.IndexByte(input, '=')
		if nameEnd < 1 {
			return nil, errors.New("malformed Digest parameter")
		}
		name := strings.ToLower(strings.TrimSpace(input[:nameEnd]))
		if !validToken(name) {
			return nil, errors.New("invalid Digest parameter name")
		}
		input = strings.TrimSpace(input[nameEnd+1:])
		value, remaining, err := parseParameterValue(input)
		if err != nil {
			return nil, err
		}
		if _, duplicate := parameters[name]; duplicate {
			return nil, errors.New("duplicate Digest parameter")
		}
		parameters[name] = value
		input = strings.TrimSpace(remaining)
		if input == "" {
			break
		}
		if input[0] != ',' {
			return nil, errors.New("malformed Digest separator")
		}
		input = strings.TrimSpace(input[1:])
		if input == "" {
			return nil, errors.New("trailing Digest separator")
		}
	}
	return parameters, nil
}

func parseParameterValue(input string) (string, string, error) {
	if input == "" {
		return "", "", errors.New("missing Digest parameter value")
	}
	if input[0] != '"' {
		end := strings.IndexByte(input, ',')
		if end < 0 {
			end = len(input)
		}
		value := strings.TrimSpace(input[:end])
		if !validToken(value) {
			return "", "", errors.New("invalid Digest token value")
		}
		return value, input[end:], nil
	}
	var value strings.Builder
	for index := 1; index < len(input); index++ {
		switch input[index] {
		case '\\':
			index++
			if index >= len(input) {
				return "", "", errors.New("unterminated Digest escape")
			}
			value.WriteByte(input[index])
		case '"':
			return value.String(), input[index+1:], nil
		case '\r', '\n', 0:
			return "", "", errors.New("invalid Digest quoted value")
		default:
			value.WriteByte(input[index])
		}
	}
	return "", "", errors.New("unterminated Digest quoted value")
}

func validToken(value string) bool {
	if value == "" {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if character <= 0x20 || character >= 0x7f || strings.ContainsRune(`()<>@,;:\"/[]?={}`, rune(character)) {
			return false
		}
	}
	return true
}
