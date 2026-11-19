package netease

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"net/url"
	"strings"
)

const (
	weapiIV = "0102030405060708"
	weapiKey = "\x30\x43\x6f\x4a\x55\x6d\x36\x51\x79\x77\x38\x57\x38\x6a\x75\x64"
	eapiKey = "\x65\x38\x32\x63\x6b\x65\x6e\x68\x38\x64\x69\x63\x68\x65\x6e\x38"
)

var weapiModulus, _ = new(big.Int).SetString("e0b509f6259df8642dbc35662901477df22677ec152b5ff68ace615bb7b725152b3ab17a876aea8a5aa76d2e417629ec4ee341f56135fccf695280104e0312ecbda92557c93870114af6c9d05c4f7f0c3685b7a46bee255932575cce10b424d813cfe4875d3e82047b97ddef52741d546b8e289dc6935b3ece0462db0a22b8e7", 16)
var weapiExponent = big.NewInt(65537)

func weapiSecret() [16]byte {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var secret [16]byte
	var random [24]byte
	for n := 0; n < len(secret); {
		rand.Read(random[:])
		for _, b := range random {
			if b < 248 {
				secret[n] = alphabet[b%62]
				n++
				if n == len(secret) {
					break
				}
			}
		}
	}
	return secret
}

func weapi(data []byte, secret [16]byte) url.Values {
	inner := encryptCBC(data, []byte(weapiKey))
	outer := encryptCBC(inner, secret[:])
	for i := range len(secret)/2 {
		j := len(secret)-1-i
		secret[i], secret[j] = secret[j], secret[i]
	}
	var ciphertext [128]byte
	key := new(big.Int).SetBytes(secret[:])
	key.Exp(key, weapiExponent, weapiModulus).FillBytes(ciphertext[:])
	return url.Values{"params": {string(outer)}, "encSecKey": {hex.EncodeToString(ciphertext[:])}}
}

func encryptCBC(data, key []byte) []byte {
	block, _ := aes.NewCipher(key)
	padded := padAES(append(make([]byte, 0, len(data)+aes.BlockSize), data...))
	cipher.NewCBCEncrypter(block, []byte(weapiIV)).CryptBlocks(padded, padded)
	return base64.StdEncoding.AppendEncode(nil, padded)
}

func eapi(path string, data []byte) string {
	const separator = "-36cd479b6b5-"
	digest := md5.Sum([]byte("nobody" + path + "use" + string(data) + "md5forencrypt"))
	message := make([]byte, 0, len(path)+len(data)+2*len(separator)+2*len(digest)+aes.BlockSize)
	message = append(message, path...)
	message = append(message, separator...)
	message = append(message, data...)
	message = append(message, separator...)
	message = hex.AppendEncode(message, digest[:])
	padded := padAES(message)
	block, _ := aes.NewCipher([]byte(eapiKey))
	for i := 0; i < len(padded); i += aes.BlockSize {
		block.Encrypt(padded[i:i+aes.BlockSize], padded[i:i+aes.BlockSize])
	}
	return strings.ToUpper(hex.EncodeToString(padded))
}

func padAES(data []byte) []byte {
	n := aes.BlockSize - len(data)%aes.BlockSize
	padded := append(data, make([]byte, n)...)
	for i := len(data); i < len(padded); i++ {
		padded[i] = byte(n)
	}
	return padded
}
