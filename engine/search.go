package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	. "github.com/ChizhovVadim/CounterGo/common"
)

const pawnValue = 100

var errSearchTimeout = errors.New("search timeout")
var errSearchLowDepth = errors.New("search low depth")

/*func savePV(transTable TransTable, p *Position, pv []Move) {
	var parent = *p
	var child Position
	for _, m := range pv {
		transTable.Update(&parent, 0, 0, 0, m)
		parent.MakeMove(m, &child)
		parent = child
	}
}*/

func lazySmp(ctx context.Context, e *Engine) {
	var ml = e.genRootMoves()
	if len(ml) != 0 {
		e.mainLine = mainLine{
			depth: 0,
			score: 0,
			moves: []Move{ml[0]},
		}
		e.depth = 0
	}
	if len(ml) <= 1 {
		return
	}

	e.done = ctx.Done()

	if e.Threads == 1 {
		iterativeDeepening(&e.threads[0], ml, 1, 1)
	} else {

		var wg = &sync.WaitGroup{}

		for i := 0; i < e.Threads; i++ {
			var ml = cloneMoves(ml)
			wg.Add(1)
			go func(i int) {
				var t = &e.threads[i]
				iterativeDeepening(t, ml, 1+i%2, 2)
				wg.Done()
			}(i)
		}

		wg.Wait()
	}
}

func iterativeDeepening(t *thread, ml []Move, startDepth, incDepth int) { //TODO, aspirationMargin
	defer func() {
		if r := recover(); r != nil {
			if r == errSearchTimeout {
				return
			}
			panic(r)
		}
	}()

	const height = 0
	for h := 0; h <= 2; h++ {
		t.stack[h].killer1 = MoveEmpty
		t.stack[h].killer2 = MoveEmpty
	}
	for depth := startDepth; depth <= maxHeight; depth += incDepth {
		t.depth = int32(depth)
		if isDone(t.engine.done) {
			break
		}

		var globalLine mainLine
		t.engine.mu.Lock()
		globalLine = t.engine.mainLine
		t.engine.mu.Unlock()

		if depth <= globalLine.depth {
			continue
		}
		if index := findMoveIndex(ml, globalLine.moves[0]); index >= 0 {
			moveToBegin(ml, index)
		}

		var score, iterationComplete = aspirationWindow(t, ml, depth, globalLine.score)
		if iterationComplete {
			t.engine.updateMainLine(mainLine{
				depth: depth,
				score: score,
				moves: t.stack[height].pv.toSlice(),
			})
		}
	}
}

func aspirationWindow(t *thread, ml []Move, depth, prevScore int) (int, bool) {
	defer func() {
		if r := recover(); r != nil {
			if r == errSearchLowDepth {
				return
			}
			panic(r)
		}
	}()

	if depth >= 5 && !(prevScore <= valueLoss || prevScore >= valueWin) {
		var alphaMargin = 25
		var betaMargin = 25
		for i := 0; i < 2; i++ {
			var alpha = Max(-valueInfinity, prevScore-alphaMargin)
			var beta = Min(valueInfinity, prevScore+betaMargin)
			var score = searchRoot(t, ml, alpha, beta, depth)
			if score >= valueWin || score <= valueLoss {
				break
			} else if score >= beta {
				betaMargin *= 2
			} else if score <= alpha {
				alphaMargin *= 2
			} else {
				return score, true
			}
		}
	}
	return searchRoot(t, ml, -valueInfinity, valueInfinity, depth), true
}

func searchRoot(t *thread, ml []Move, alpha, beta, depth int) int {
	const height = 0
	t.stack[height].pv.clear()
	var p = &t.stack[height].position
	t.stack[height].staticEval = t.evaluator.Evaluate(p)
	var child = &t.stack[height+1].position
	var bestMoveIndex = 0
	for i, move := range ml {
		p.MakeMove(move, child)
		var extension, reduction int
		extension = t.extend(depth, height)
		if depth >= 3 && i > 0 &&
			!(isCaptureOrPromotion(move)) {
			reduction = t.engine.lateMoveReduction(depth, i+1)
			reduction = Max(0, Min(depth-2, reduction))
		}
		var newDepth = depth - 1 + extension
		var nextFirstline = i == 0
		var score = alpha + 1
		// LMR/PVS
		if reduction > 0 || beta != alpha+1 && i > 0 && newDepth > 0 {
			score = -t.alphaBeta(-(alpha + 1), -alpha, newDepth-reduction, height+1, nextFirstline)
		}
		// full search
		if score > alpha {
			score = -t.alphaBeta(-beta, -alpha, newDepth, height+1, nextFirstline)
		}
		if score > alpha {
			alpha = score
			t.stack[height].pv.assign(move, &t.stack[height+1].pv)
			bestMoveIndex = i
			if alpha >= beta {
				break
			}
		}
	}
	moveToBegin(ml, bestMoveIndex)
	return alpha
}

// main search method
func (t *thread) alphaBeta(alpha, beta, depth, height int, firstline bool) int {
	var oldAlpha = alpha
	var newDepth int
	t.stack[height].pv.clear()

	var position = &t.stack[height].position

	if height >= maxHeight {
		return t.evaluator.Evaluate(position)
	}

	if t.isRepeat(height) {
		return Max(alpha, valueDraw)
	}

	if depth <= 0 {
		return t.quiescence(alpha, beta, height)
	}

	t.incNodes()

	if isDraw(position) {
		return valueDraw
	}

	var isCheck = position.IsCheck()

	// mate distance pruning
	if winIn(height+1) <= alpha {
		return alpha
	}
	if lossIn(height+2) >= beta && !isCheck {
		return beta
	}

	// transposition table
	var ttDepth, ttValue, ttBound, ttMove, ttHit = t.engine.transTable.Read(position.Key)
	if ttHit {
		ttValue = valueFromTT(ttValue, height)
		if ttDepth >= depth {
			if ttValue >= beta && (ttBound&boundLower) != 0 {
				if ttMove != MoveEmpty && !isCaptureOrPromotion(ttMove) {
					t.updateKiller(ttMove, height)
				}
				return ttValue
			}
			if ttValue <= alpha && (ttBound&boundUpper) != 0 {
				return ttValue
			}
		}
	}

	var staticEval = t.evaluator.Evaluate(position)
	t.stack[height].staticEval = staticEval
	var improving = position.LastMove == MoveEmpty ||
		height >= 2 && staticEval > t.stack[height-2].staticEval

	// reverse futility pruning
	if !firstline && depth <= 8 && !isCheck {
		var score = staticEval - pawnValue*depth
		if score >= beta {
			return score
		}
	}

	if height+2 <= maxHeight {
		t.stack[height+2].killer1 = MoveEmpty
		t.stack[height+2].killer2 = MoveEmpty
	}

	// null-move pruning
	var child = &t.stack[height+1].position
	if !firstline && depth >= 2 && !isCheck &&
		position.LastMove != MoveEmpty &&
		(height <= 1 || t.stack[height-1].position.LastMove != MoveEmpty) &&
		beta < valueWin &&
		!(ttHit && ttValue < beta && (ttBound&boundUpper) != 0) &&
		!isLateEndgame(position, position.WhiteMove) &&
		staticEval >= beta {
		var reduction = 4 + depth/6
		if staticEval >= beta+50 {
			reduction = Min(reduction, depth)
		} else {
			reduction = Min(reduction, depth-1)
		}
		if reduction >= 2 {
			position.MakeNullMove(child)
			var score = -t.alphaBeta(-beta, -(beta - 1), depth-reduction, height+1, false)
			if score >= beta {
				if score >= valueWin {
					score = beta
				}
				return score
			}
		}
	}

	// Internal iterative deepening
	if depth >= 8 && ttMove == MoveEmpty {
		var iidDepth = depth - depth/4 - 5
		t.alphaBeta(alpha, beta, iidDepth, height, firstline)
		if t.stack[height].pv.size != 0 {
			ttMove = t.stack[height].pv.items[0]
			t.stack[height].pv.clear()
		}
	}

	var followUp Move
	if height > 0 {
		followUp = t.stack[height-1].position.LastMove
	}
	var historyContext = t.history.getContext(position.WhiteMove, position.LastMove, followUp)

	var mi = moveIterator{
		position:  position,
		buffer:    t.stack[height].moveList[:],
		history:   historyContext,
		transMove: ttMove,
		killer1:   t.stack[height].killer1,
		killer2:   t.stack[height].killer2,
	}
	mi.Init()

	// singular extension
	var ttMoveIsSingular = false
	if depth >= 8 &&
		ttHit && ttMove != MoveEmpty &&
		(ttBound&boundLower) != 0 && ttDepth >= depth-3 &&
		ttValue > valueLoss && ttValue < valueWin {

		ttMoveIsSingular = true
		var singularBeta = Max(-valueInfinity, ttValue-depth)
		newDepth = depth/2 - 1
		var quietsPlayed = 0
		for mi.Reset(); ; {
			var move = mi.Next()
			if move == MoveEmpty {
				break
			}
			if quietsPlayed >= 6 && !isCaptureOrPromotion(move) {
				continue
			}
			if !position.MakeMove(move, child) {
				continue
			}
			if move == ttMove {
				if t.extend(depth, height) == 1 {
					ttMoveIsSingular = false
					break
				}
				continue
			}
			if !isCaptureOrPromotion(move) {
				quietsPlayed++
			}
			var score = -t.alphaBeta(-singularBeta, -singularBeta+1, newDepth, height+1, false)
			if score >= singularBeta {
				ttMoveIsSingular = false
				break
			}
		}
	}

	var movesSearched = 0
	var hasLegalMove = false
	var movesSeen = 0

	var quietsSearched = t.stack[height].quietsSearched[:0]
	var bestMove Move
	const SortMovesIndex = 4

	var lmp = 5 + depth*depth
	if !improving {
		lmp /= 2
	}

	var best = -valueInfinity

	for mi.Reset(); ; {
		var move = mi.Next()
		if move == MoveEmpty {
			break
		}
		movesSeen++

		if depth <= 8 && best > valueLoss && hasLegalMove {
			// late-move pruning
			if !isCaptureOrPromotion(move) && movesSeen > lmp {
				continue
			}

			// futility pruning
			if !(isCheck ||
				isCaptureOrPromotion(move) ||
				move == mi.killer1 ||
				move == mi.killer2 ||
				position.LastMove == MoveEmpty) &&
				staticEval+pawnValue*depth <= alpha {
				continue
			}

			// SEE pruning
			if !isCheck &&
				(!isCaptureOrPromotion(move) || staticEval-pawnValue*depth <= alpha) &&
				!SeeGE(position, move, -depth) {
				continue
			}
		}

		if !position.MakeMove(move, child) {
			movesSeen--
			continue
		}
		hasLegalMove = true

		movesSearched++

		var extension, reduction int

		extension = t.extend(depth, height)
		if move == ttMove && ttMoveIsSingular {
			extension = 1
		}

		if depth >= 3 && movesSearched > 1 &&
			!(isCaptureOrPromotion(move)) {
			reduction = t.engine.lateMoveReduction(depth, movesSearched)
			if move == mi.killer1 || move == mi.killer2 {
				reduction--
			}
			var history = historyContext.ReadTotal(position.WhiteMove, move)
			reduction -= Max(-2, Min(2, history/5000))
			reduction = Max(0, Min(depth-2, reduction))
		}

		if !isCaptureOrPromotion(move) {
			quietsSearched = append(quietsSearched, move)
		}

		newDepth = depth - 1 + extension
		var nextFirstline = firstline && movesSearched == 1

		var score = alpha + 1
		// LMR
		if reduction > 0 {
			score = -t.alphaBeta(-(alpha + 1), -alpha, newDepth-reduction, height+1, nextFirstline)
		}
		// full search
		if score > alpha {
			score = -t.alphaBeta(-beta, -alpha, newDepth, height+1, nextFirstline)
		}

		best = Max(best, score)
		if score > alpha {
			alpha = score
			bestMove = move
			t.stack[height].pv.assign(move, &t.stack[height+1].pv)
			if alpha >= beta {
				break
			}
		}
	}

	if !hasLegalMove {
		if isCheck {
			return lossIn(height)
		}
		return valueDraw
	}

	if bestMove != MoveEmpty && !isCaptureOrPromotion(bestMove) {
		historyContext.Update(position.WhiteMove, quietsSearched, bestMove, depth)
		t.updateKiller(bestMove, height)
	}

	ttBound = 0
	if best > oldAlpha {
		ttBound |= boundLower
	}
	if best < beta {
		ttBound |= boundUpper
	}
	t.engine.transTable.Update(position.Key, depth, valueToTT(best, height), ttBound, bestMove)

	return best
}

func (t *thread) quiescence(alpha, beta, height int) int {
	t.stack[height].pv.clear()
	t.incNodes()
	var position = &t.stack[height].position
	if isDraw(position) {
		return valueDraw
	}
	if height >= maxHeight {
		return t.evaluator.Evaluate(position)
	}

	var _, ttValue, ttBound, _, ttHit = t.engine.transTable.Read(position.Key)
	if ttHit {
		ttValue = valueFromTT(ttValue, height)
		if ttBound == boundExact ||
			ttBound == boundLower && ttValue >= beta ||
			ttBound == boundUpper && ttValue <= alpha {
			return ttValue
		}
	}

	var isCheck = position.IsCheck()
	var best = -valueInfinity
	if !isCheck {
		var eval = t.evaluator.Evaluate(position)
		best = Max(best, eval)
		if eval > alpha {
			alpha = eval
			if alpha >= beta {
				return alpha
			}
		}
	}
	var mi = moveIteratorQS{
		position: position,
		buffer:   t.stack[height].moveList[:],
	}
	mi.Init()
	var hasLegalMove = false
	var child = &t.stack[height+1].position
	for mi.Reset(); ; {
		var move = mi.Next()
		if move == MoveEmpty {
			break
		}
		if !isCheck && !seeGEZero(position, move) {
			continue
		}
		if !position.MakeMove(move, child) {
			continue
		}
		hasLegalMove = true
		var score = -t.quiescence(-beta, -alpha, height+1)
		best = Max(best, score)
		if score > alpha {
			alpha = score
			t.stack[height].pv.assign(move, &t.stack[height+1].pv)
			if alpha >= beta {
				break
			}
		}
	}
	if isCheck && !hasLegalMove {
		return lossIn(height)
	}
	return best
}

func (t *thread) incNodes() {
	t.nodes++
	if t.nodes > 255 {
		var globalNodes = atomic.AddInt64(&t.engine.nodes, t.nodes)
		var globalDepth = atomic.LoadInt32(&t.engine.depth)
		t.nodes = 0
		t.engine.timeManager.OnNodesChanged(int(globalNodes))
		if t.depth <= globalDepth {
			panic(errSearchLowDepth)
		}
		if isDone(t.engine.done) {
			panic(errSearchTimeout)
		}
	}
}

func isDone(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

func isDraw(p *Position) bool {
	if p.Rule50 > 100 {
		return true
	}

	if (p.Pawns|p.Rooks|p.Queens) == 0 &&
		!MoreThanOne(p.Knights|p.Bishops) {
		return true
	}

	return false
}

func (t *thread) isRepeat(height int) bool {
	var p = &t.stack[height].position

	if p.Rule50 == 0 || p.LastMove == MoveEmpty {
		return false
	}
	for i := height - 1; i >= 0; i-- {
		var temp = &t.stack[i].position
		if temp.Key == p.Key {
			return true
		}
		if temp.Rule50 == 0 || temp.LastMove == MoveEmpty {
			return false
		}
	}

	return t.engine.historyKeys[p.Key] >= 2
}

func (t *thread) extend(depth, height int) int {
	//var p = &t.stack[height].position
	var child = &t.stack[height+1].position
	var givesCheck = child.IsCheck()

	if givesCheck {
		return 1
	}

	return 0
}

func findMoveIndex(ml []Move, move Move) int {
	for i := range ml {
		if ml[i] == move {
			return i
		}
	}
	return -1
}

func moveToBegin(ml []Move, index int) {
	if index == 0 {
		return
	}
	var item = ml[index]
	for i := index; i > 0; i-- {
		ml[i] = ml[i-1]
	}
	ml[0] = item
}

func cloneMoves(ml []Move) []Move {
	var result = make([]Move, len(ml))
	copy(result, ml)
	return result
}

func (e *Engine) genRootMoves() []Move {
	var t = &e.threads[0]
	const height = 0
	var p = &t.stack[height].position
	_, _, _, transMove, _ := e.transTable.Read(p.Key)

	var historyContext = t.history.getContext(p.WhiteMove, p.LastMove, MoveEmpty)
	var mi = moveIterator{
		position:  p,
		buffer:    t.stack[height].moveList[:],
		history:   historyContext,
		transMove: transMove,
	}
	mi.Init()

	var result []Move
	var child = &t.stack[height+1].position
	for mi.Reset(); ; {
		var move = mi.Next()
		if move == MoveEmpty {
			break
		}
		if p.MakeMove(move, child) {
			result = append(result, move)
		}
	}
	return result
}

func (t *thread) updateKiller(move Move, height int) {
	if t.stack[height].killer1 != move {
		t.stack[height].killer2 = t.stack[height].killer1
		t.stack[height].killer1 = move
	}
}
