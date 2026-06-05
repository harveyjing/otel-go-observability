package dice

import "math/rand/v2"

func Roll() int {
	return rand.IntN(6) + 1
}
