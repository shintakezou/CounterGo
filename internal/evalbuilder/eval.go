package evalbuilder

import (
	"embed"
	"fmt"
	"sync"

	counter "github.com/ChizhovVadim/CounterGo/pkg/eval/counter"
	fast "github.com/ChizhovVadim/CounterGo/pkg/eval/fast"
	linear "github.com/ChizhovVadim/CounterGo/pkg/eval/linear"
	material "github.com/ChizhovVadim/CounterGo/pkg/eval/material"
	nnue "github.com/ChizhovVadim/CounterGo/pkg/eval/nnue"
	pesto "github.com/ChizhovVadim/CounterGo/pkg/eval/pesto"
	weiss "github.com/ChizhovVadim/CounterGo/pkg/eval/weiss"
)

//go:embed n-28-5177.nn
var content embed.FS

var once sync.Once
var weights *nnue.Weights

func Build(key string) interface{} {
	switch key {
	case "counter":
		return counter.NewEvaluationService()
	case "weiss":
		return weiss.NewEvaluationService()
	case "linear":
		return linear.NewEvaluationService()
	case "pesto":
		return pesto.NewEvaluationService()
	case "material":
		return material.NewEvaluationService()
	case "fast":
		return fast.NewEvaluationService()
	case "nnue":
		once.Do(func() {
			var w, err = loadWeights()
			if err != nil {
				panic(err)
			}
			weights = w
		})
		return nnue.NewEvaluationService(weights)
	}
	panic(fmt.Errorf("bad eval %v", key))
}

func loadWeights() (*nnue.Weights, error) {
	var f, err = content.Open("n-28-5177.nn")
	if err != nil {
		return nil, err
	}
	defer f.Close()
	weights, err = nnue.LoadWeights(f)
	if err != nil {
		return nil, err
	}
	return weights, nil
}
