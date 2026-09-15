// PBKDF2（RFC 2898）口令派生的本地实现。
//
// Go 1.24 起标准库提供 crypto/pbkdf2；为兼容 Go 1.23 工具链，这里内置一份
// 等价实现（HMAC-SHA256 等任意 HMAC 哈希均可使用）。升级工具链后可直接改回
// 标准库 crypto/pbkdf2，本文件应一并删除。

package auth

import (
	"crypto/hmac"
	"errors"
	"hash"
)

// errPBKDF2Parameters 表示 PBKDF2 派生参数非法。
var errPBKDF2Parameters = errors.New("pbkdf2: 迭代次数、密钥长度或哈希函数非法")

// pbkdf2Key 按 PBKDF2-HMAC 标准从口令派生密钥。
func pbkdf2Key(h func() hash.Hash, password string, salt []byte, iter, keyLen int) ([]byte, error) {
	prf := h()
	if iter <= 0 || keyLen <= 0 || prf == nil {
		return nil, errPBKDF2Parameters
	}
	hashLen := prf.Size()
	// RFC 5325：派生密钥最大长度 (2^32-1)*hLen。
	maxLen := (1<<32 - 1) * hashLen
	if keyLen > maxLen {
		return nil, errPBKDF2Parameters
	}

	numBlocks := (keyLen + hashLen - 1) / hashLen
	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	U := make([]byte, hashLen)
	T := make([]byte, hashLen)

	pw := []byte(password)
	for block := 1; block <= numBlocks; block++ {
		// U1 = PRF(Password, Salt || INT(i))
		mac := hmac.New(h, pw)
		mac.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		mac.Write(buf[:])
		U = mac.Sum(U[:0])

		// T_i = U1 ^ U2 ^ ... ^ Uc
		copy(T, U)
		for n := 2; n <= iter; n++ {
			mac = hmac.New(h, pw)
			mac.Write(U)
			U = mac.Sum(U[:0])
			for x := range T {
				T[x] ^= U[x]
			}
		}
		dk = append(dk, T...)
	}
	return dk[:keyLen], nil
}
