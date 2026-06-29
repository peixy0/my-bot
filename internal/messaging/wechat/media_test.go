package wechat

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key, err := generateAESKey()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"small", []byte("hello world")},
		{"exact block", bytes.Repeat([]byte("A"), 16)},
		{"multi block", bytes.Repeat([]byte("B"), 48)},
		{"large", bytes.Repeat([]byte("C"), 1024)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enc, err := encryptAES128ECB(key, tc.data)
			if err != nil {
				t.Fatalf("encrypt: %v", err)
			}
			dec, err := decryptAES128ECB(key, enc)
			if err != nil {
				t.Fatalf("decrypt: %v", err)
			}
			if !bytes.Equal(dec, tc.data) {
				t.Fatalf("got %x, want %x", dec, tc.data)
			}
		})
	}
}

func TestParseAESKey(t *testing.T) {
	rawKey := bytes.Repeat([]byte{0xAB}, 16)

	// Format 1: 32 hex chars
	hexStr := hex.EncodeToString(rawKey)
	k, err := parseAESKey(hexStr)
	if err != nil || !bytes.Equal(k, rawKey) {
		t.Errorf("hex format: got err=%v key=%x", err, k)
	}

	// Format 2: base64(raw 16 bytes)
	b64Raw := base64.StdEncoding.EncodeToString(rawKey)
	k, err = parseAESKey(b64Raw)
	if err != nil || !bytes.Equal(k, rawKey) {
		t.Errorf("base64-raw format: got err=%v key=%x", err, k)
	}

	// Format 3: base64(hex string)
	b64Hex := base64.StdEncoding.EncodeToString([]byte(hexStr))
	k, err = parseAESKey(b64Hex)
	if err != nil || !bytes.Equal(k, rawKey) {
		t.Errorf("base64-hex format: got err=%v key=%x", err, k)
	}
}
