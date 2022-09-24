package main

import (
	"math/rand"
)

func initUniform(rnd *rand.Rand, data []float64, max float64) {
	for i := range data {
		data[i] = (rnd.Float64() - 0.5) * 2 * max
	}
}

func ValidationCost(output, target float64) float64 {
	var x = output - target
	return x * x
}
