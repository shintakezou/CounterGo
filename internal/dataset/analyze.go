package dataset

import (
	"context"
	"fmt"
	"log"
	"math"

	"github.com/ChizhovVadim/CounterGo/internal/pgn"

	"github.com/ChizhovVadim/CounterGo/pkg/common"
)

func (dp *DatasetProvider) analyzeGames(
	ctx context.Context,
	games <-chan pgn.GameRaw,
	dataset chan<- datasetInfo,
) error {
	for game := range games {
		var err = dp.analyzeGame(ctx, game, dataset)
		if err != nil {
			log.Println("analyzeGame failed",
				"Game", game,
				"err", err)
		}
	}
	return nil
}

func (dp *DatasetProvider) analyzeGame(
	ctx context.Context,
	gameRaw pgn.GameRaw,
	dataset chan<- datasetInfo,
) error {
	var game, err = pgn.ParseGame(gameRaw)
	if err != nil {
		return err
	}

	var gameResult, gameResOk = calcGameResult(game.Result)
	if !gameResOk {
		return fmt.Errorf("bad game result %v", game.Result)
	}

	var startFen = game.Fen
	if startFen == "" {
		startFen = common.InitialPositionFen
	}
	pos, err := common.NewPositionFromFEN(startFen)
	if err != nil {
		return err
	}

	var repeatPositions = make(map[uint64]struct{})

	for i := range game.Items {
		repeatPositions[pos.Key] = struct{}{}

		//Make move
		var child common.Position
		if !pos.MakeMove(game.Items[i].Move, &child) {
			break
		}
		pos = child

		//filter quiet positions
		var comment = game.Items[i].Comment
		if comment.Depth < 8 {
			continue
		}
		/*if comment.Score.Mate != 0 {
			continue
		}*/
		if pos.IsCheck() {
			continue
		}
		if isDraw(&pos) {
			continue
		}
		if _, found := repeatPositions[pos.Key]; found {
			continue
		}
		if isNoisyPos(&pos, dp.CheckNoisyOnlyForSideToMove) {
			continue
		}

		var targetBySearch float64
		if comment.Score.Mate != 0 {
			if comment.Score.Mate > 0 {
				targetBySearch = 1
			} else {
				targetBySearch = 0
			}
		} else {
			targetBySearch = dp.sigmoid(float64(comment.Score.Centipawns))
		}
		if pos.WhiteMove {
			//!
			targetBySearch = 1 - targetBySearch
		}

		var target = targetBySearch*dp.SearchRatio + gameResult*(1-dp.SearchRatio)

		//save position
		dataset <- datasetInfo{
			fen:    pos.String(),
			key:    pos.Key,
			target: target,
		}
	}
	return nil
}

func (dp *DatasetProvider) sigmoid(x float64) float64 {
	return 1.0 / (1.0 + math.Exp(dp.SigmoidScale*(-x)))
}

func calcGameResult(sGameResult string) (float64, bool) {
	switch sGameResult {
	case pgn.GameResultWhiteWin:
		return 1, true
	case pgn.GameResultBlackWin:
		return 0, true
	case pgn.GameResultDraw:
		return 0.5, true
	default:
		return 0, false
	}
}
