package utils

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// IsValidHex indicates if value is a proper hex strings that can be contained
// with the given number of bits
func IsValidHex(value string, bits int) bool {
	str := strings.ToUpper(value)
	precZeros := true
	bitcount := 0
	for _, c := range str {
		// Ensure the rune is a HEX character
		if !strings.Contains("0123456789ABCDEF", string(c)) {
			return false
		}
		// Ensure that we are within the given bit size
		if precZeros {
			value, err := strconv.ParseInt(string(c), 16, 8)
			if err != nil {
				// This is unclear how this could ever error out
				fmt.Println("err on parse")
				return false
			}
			// Add in variable number of bits for first HEX char
			if value == 0 {
				continue
			} else {
				precZeros = false
			}
			bitcount += int(math.Ceil(math.Log2(float64(value + 1))))
		} else {
			// Add in a nibble
			bitcount += 4
		}
		if bitcount > bits {
			return false
		}
	}
	return true
}
