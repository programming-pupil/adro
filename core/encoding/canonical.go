// Package encoding implements ADRO canonical JSON v1.
package encoding

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

const (
	Name          = "json-canonical"
	Version       = "v1"
	HashAlgorithm = "sha-256"
	HashVersion   = "v1"
	maxExponent   = 10000
)

type Identity struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	HashAlgorithm string `json:"hash_algorithm"`
	HashVersion   string `json:"hash_version"`
}

var Current = Identity{Name: Name, Version: Version, HashAlgorithm: HashAlgorithm, HashVersion: HashVersion}

func Marshal(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode JSON: %w", err)
	}
	return Canonicalize(raw)
}

func Canonicalize(raw []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	if err := writeValue(&out, value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func Digest(value any) (string, error) {
	data, err := Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func DigestRaw(raw []byte) (string, error) {
	data, err := Canonicalize(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func ensureEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	} else if err != nil {
		return fmt.Errorf("decode trailing JSON: %w", err)
	}
	return errors.New("multiple JSON values are not canonical")
}

func writeValue(out *bytes.Buffer, value any) error {
	switch value := value.(type) {
	case nil:
		out.WriteString("null")
	case bool:
		out.WriteString(strconv.FormatBool(value))
	case string:
		encoded, _ := json.Marshal(value)
		out.Write(encoded)
	case json.Number:
		number, err := canonicalNumber(value.String())
		if err != nil {
			return err
		}
		out.WriteString(number)
	case []any:
		out.WriteByte('[')
		for i, item := range value {
			if i > 0 {
				out.WriteByte(',')
			}
			if err := writeValue(out, item); err != nil {
				return err
			}
		}
		out.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out.WriteByte('{')
		for i, key := range keys {
			if i > 0 {
				out.WriteByte(',')
			}
			encoded, _ := json.Marshal(key)
			out.Write(encoded)
			out.WriteByte(':')
			if err := writeValue(out, value[key]); err != nil {
				return err
			}
		}
		out.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical JSON type %T", value)
	}
	return nil
}

func canonicalNumber(value string) (string, error) {
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign, value = "-", value[1:]
	}
	exponent := 0
	if index := strings.IndexAny(value, "eE"); index >= 0 {
		parsed, err := strconv.Atoi(value[index+1:])
		if err != nil || parsed < -maxExponent || parsed > maxExponent {
			return "", fmt.Errorf("number exponent is outside canonical bounds: %q", value)
		}
		exponent, value = parsed, value[:index]
	}
	integer, fraction := value, ""
	if index := strings.IndexByte(value, '.'); index >= 0 {
		integer, fraction = value[:index], value[index+1:]
	}
	digits := strings.TrimLeft(integer+fraction, "0")
	if digits == "" {
		return "0", nil
	}
	scale := len(fraction) - exponent
	for scale > 0 && strings.HasSuffix(digits, "0") {
		digits = strings.TrimSuffix(digits, "0")
		scale--
	}
	var result string
	switch {
	case scale <= 0:
		result = digits + strings.Repeat("0", -scale)
	case len(digits) > scale:
		point := len(digits) - scale
		result = digits[:point] + "." + digits[point:]
	default:
		result = "0." + strings.Repeat("0", scale-len(digits)) + digits
	}
	return sign + result, nil
}
