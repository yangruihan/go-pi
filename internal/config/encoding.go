package config

import (
	"encoding/binary"
	"fmt"
	"unicode/utf16"
)

// normalizeYAMLBytes 将常见文本编码统一为 UTF-8 字节序列。
// 支持：UTF-8(含/不含 BOM)、UTF-16 LE/BE（需 BOM）。
func normalizeYAMLBytes(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return data, nil
	}

	// UTF-8 BOM
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:], nil
	}

	// UTF-16 LE BOM
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		return decodeUTF16(data[2:], binary.LittleEndian)
	}

	// UTF-16 BE BOM
	if len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF {
		return decodeUTF16(data[2:], binary.BigEndian)
	}

	// 默认按 UTF-8 处理
	return data, nil
}

func decodeUTF16(data []byte, order binary.ByteOrder) ([]byte, error) {
	if len(data)%2 != 0 {
		return nil, fmt.Errorf("invalid utf-16 data length")
	}

	u16 := make([]uint16, 0, len(data)/2)
	for i := 0; i < len(data); i += 2 {
		u16 = append(u16, order.Uint16(data[i:i+2]))
	}

	runes := utf16.Decode(u16)
	return []byte(string(runes)), nil
}
