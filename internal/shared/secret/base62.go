package secret

import "io"

// Base62Alphabet is the alphabet credential values are drawn from: letters
// and digits, safe in a header, a URL and a shell without quoting.
const Base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// Base62 draws n characters of Base62Alphabet from random without bias: a
// byte is used only below 248, the largest multiple of 62 a byte holds,
// since reducing the bytes above it would favour the first characters. It
// is the one generator of the platform's credential values.
func Base62(random io.Reader, n int) string {
	out := make([]byte, 0, n)
	buf := make([]byte, 64)
	for len(out) < n {
		if _, err := io.ReadFull(random, buf); err != nil {
			// The system's randomness never fails; an exhausted test source
			// is a programming error.
			panic("secret: reading randomness: " + err.Error())
		}
		for _, b := range buf {
			if b < 248 && len(out) < n {
				out = append(out, Base62Alphabet[b%62])
			}
		}
	}
	return string(out)
}

// IsBase62 reports whether s holds only characters of Base62Alphabet.
func IsBase62(s string) bool {
	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') {
			return false
		}
	}
	return true
}
