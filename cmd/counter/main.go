package main

import (
	"log"
	"os"
	"runtime"

	"github.com/ChizhovVadim/CounterGo/pkg/engine"
	eval "github.com/ChizhovVadim/CounterGo/pkg/eval/counter"
	"github.com/ChizhovVadim/CounterGo/pkg/uci"
)

/*
Counter Copyright (C) 2017-2022 Vadim Chizhov
This program is free software: you can redistribute it and/or modify it under the terms of the GNU General Public License as published by the Free Software Foundation, either version 3 of the License, or (at your option) any later version.
This program is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU General Public License for more details.
You should have received a copy of the GNU General Public License along with this program. If not, see <http://www.gnu.org/licenses/>.
*/

const (
	name   = "Counter"
	author = "Vadim Chizhov"
)

var (
	versionName = "dev"
	buildDate   = "(null)"
	gitRevision = "(null)"
)

func main() {
	var logger = log.New(os.Stderr, "", log.LstdFlags)

	logger.Println(name,
		"VersionName", versionName,
		"BuildDate", buildDate,
		"GitRevision", gitRevision,
		"RuntimeVersion", runtime.Version())

	var eng = engine.NewEngine(func() engine.Evaluator {
		return eval.NewEvaluationService()
	})

	var protocol = uci.New(name, author, versionName, eng,
		[]uci.Option{
			&uci.IntOption{Name: "Hash", Min: 4, Max: 1 << 16, Value: &eng.Hash},
			&uci.IntOption{Name: "Threads", Min: 1, Max: runtime.NumCPU(), Value: &eng.Threads},
		},
	)

	uci.RunCli(logger, protocol)
}
