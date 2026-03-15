// Package skykey provides cryptographic primitives for the sky10 ecosystem.
//
// It handles Ed25519 keypairs, Bech32m address encoding (sky10q...),
// sealing/opening encrypted messages, key wrapping, and digital signatures.
// Every other sky10 package imports skykey for cryptographic operations.
package skykey

import (
	"fmt"
	"strings"
)

// Bech32m encoding with a custom "10" separator instead of the standard "1".
// The BCH checksum math is identical to BIP-350 (Bech32m). Only the
// separator parsing differs.
//
// Format: <hrp>10<data+checksum>
// Example: sky10qvx2mz9...
//   hrp = "sky"
//   separator = "10"
//   data = version byte + encoded public key + 6-char checksum

const (
	bech32mConst = 0x2bc830a3 // Bech32m constant (BIP-350)
	separator    = "10"
	charset      = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
)

var charsetRev [128]int8

func init() {
	for i := range charsetRev {
		charsetRev[i] = -1
	}
	for i, c := range charset {
		charsetRev[c] = int8(i)
	}
}

// Bech32mEncode encodes data with a human-readable prefix using Bech32m.
// The version byte is prepended to data as the first 5-bit group.
func Bech32mEncode(hrp string, version byte, data []byte) (string, error) {
	if len(hrp) == 0 {
		return "", fmt.Errorf("empty hrp")
	}
	if version > 31 {
		return "", fmt.Errorf("version must be 0-31, got %d", version)
	}

	// Convert 8-bit data to 5-bit groups
	converted, err := convertBits(data, 8, 5, true)
	if err != nil {
		return "", fmt.Errorf("converting bits: %w", err)
	}

	// Prepend version byte
	values := make([]byte, 0, 1+len(converted)+6)
	values = append(values, version)
	values = append(values, converted...)

	// Compute checksum
	checksum := bech32mChecksum(hrp, values)
	values = append(values, checksum...)

	// Encode to charset
	var result strings.Builder
	result.WriteString(hrp)
	result.WriteString(separator)
	for _, v := range values {
		result.WriteByte(charset[v])
	}

	return result.String(), nil
}

// Bech32mDecode decodes a Bech32m string with the custom "10" separator.
// Returns the HRP, version byte, and decoded data.
func Bech32mDecode(s string) (string, byte, []byte, error) {
	s = strings.ToLower(s)

	// Find the last occurrence of "10" as separator.
	// Since "1" is not in the charset, any "1" must be part of the separator.
	sepIdx := strings.LastIndex(s, separator)
	if sepIdx < 1 {
		return "", 0, nil, fmt.Errorf("no separator found")
	}

	hrp := s[:sepIdx]
	dataStr := s[sepIdx+len(separator):]

	if len(hrp) == 0 {
		return "", 0, nil, fmt.Errorf("empty hrp")
	}
	if len(dataStr) < 7 { // minimum: 1 version + 6 checksum
		return "", 0, nil, fmt.Errorf("data too short")
	}

	// Decode characters to 5-bit values
	values := make([]byte, len(dataStr))
	for i, c := range dataStr {
		if c >= 128 || charsetRev[c] == -1 {
			return "", 0, nil, fmt.Errorf("invalid character: %c", c)
		}
		values[i] = byte(charsetRev[c])
	}

	// Verify checksum
	if !bech32mVerify(hrp, values) {
		return "", 0, nil, fmt.Errorf("invalid checksum")
	}

	// Strip checksum (last 6 values)
	values = values[:len(values)-6]

	// First value is the version byte
	version := values[0]
	values = values[1:]

	// Convert 5-bit groups back to 8-bit data
	data, err := convertBits(values, 5, 8, false)
	if err != nil {
		return "", 0, nil, fmt.Errorf("converting bits: %w", err)
	}

	return hrp, version, data, nil
}

// polymod computes the BCH checksum polynomial.
func polymod(values []byte) uint32 {
	gen := [5]uint32{0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3}
	chk := uint32(1)
	for _, v := range values {
		b := chk >> 25
		chk = (chk&0x1ffffff)<<5 ^ uint32(v)
		for i := 0; i < 5; i++ {
			if (b>>uint(i))&1 == 1 {
				chk ^= gen[i]
			}
		}
	}
	return chk
}

// hrpExpand expands the HRP for checksum computation.
func hrpExpand(hrp string) []byte {
	result := make([]byte, 0, len(hrp)*2+1)
	for _, c := range hrp {
		result = append(result, byte(c>>5))
	}
	result = append(result, 0)
	for _, c := range hrp {
		result = append(result, byte(c&31))
	}
	return result
}

func bech32mChecksum(hrp string, values []byte) []byte {
	enc := append(hrpExpand(hrp), values...)
	enc = append(enc, 0, 0, 0, 0, 0, 0)
	mod := polymod(enc) ^ bech32mConst
	checksum := make([]byte, 6)
	for i := 0; i < 6; i++ {
		checksum[i] = byte((mod >> uint(5*(5-i))) & 31)
	}
	return checksum
}

func bech32mVerify(hrp string, values []byte) bool {
	return polymod(append(hrpExpand(hrp), values...)) == bech32mConst
}

// convertBits converts between bit groups (e.g., 8-bit to 5-bit).
func convertBits(data []byte, fromBits, toBits uint, pad bool) ([]byte, error) {
	acc := uint32(0)
	bits := uint(0)
	maxV := uint32((1 << toBits) - 1)
	var result []byte

	for _, value := range data {
		acc = (acc << fromBits) | uint32(value)
		bits += fromBits
		for bits >= toBits {
			bits -= toBits
			result = append(result, byte((acc>>bits)&maxV))
		}
	}

	if pad {
		if bits > 0 {
			result = append(result, byte((acc<<(toBits-bits))&maxV))
		}
	} else {
		if bits >= fromBits {
			return nil, fmt.Errorf("invalid padding")
		}
		if (acc<<(toBits-bits))&maxV != 0 {
			return nil, fmt.Errorf("non-zero padding")
		}
	}

	return result, nil
}
