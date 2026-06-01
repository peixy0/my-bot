package util

import (
	"bytes"
	"encoding/json"
)

func ToJSON(v any) ([]byte, error) {
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return []byte{}, err
	}
	return buffer.Bytes(), nil
}

func ToJSONIndent(v any) ([]byte, error) {
	buffer := &bytes.Buffer{}
	encoder := json.NewEncoder(buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(v); err != nil {
		return []byte{}, err
	}
	return buffer.Bytes(), nil
}
