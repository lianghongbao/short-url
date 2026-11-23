package codec

import "errors"

var (
	ErrInvalidCode = errors.New("无效的短码")
)

const chars = "TSIVCuyGJNKctW3rgMhe9fE0U8wa2X1vb5BQjmD4Ak7oLqzPRFHdx6nZlsipOY"

const base = int64(len(chars))
const codeLen = 5

// MaxCodeValue 是 5 位 base62 能表示的最大连续 ID 值 (62^5 - 1)
// 用于确保 ID 在安全范围内进行编码
const MaxCodeValue = int64(916132831)

func Encode(id int64) string {
	if id < 0 {
		return ""
	}
	result := make([]byte, codeLen)

	for i := codeLen - 1; i >= 0; i-- {
		result[i] = chars[id%base]
		id /= base
	}
	return string(result)
}

func Decode(code string) (int64, error) {
	if len(code) != codeLen {
		return 0, ErrInvalidCode
	}

	var id int64
	for _, c := range code {
		idx := indexOf(c)
		if idx < 0 {
			return 0, ErrInvalidCode
		}
		id = id*base + int64(idx)
	}
	return id, nil
}

func indexOf(c rune) int {
	for i, v := range chars {
		if v == c {
			return i
		}
	}
	return -1
}
