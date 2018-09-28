// Copyright 2017 The go-AdelineCoin Authors
// This file is part of the go-AdelineCoin library.
//
// The go-AdelineCoin library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-AdelineCoin library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-AdelineCoin library. If not, see <http://www.gnu.org/licenses/>.

package ethash

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"runtime"
	"time"

	"github.com/AdelineCoin/go-adln/common"
	"github.com/AdelineCoin/go-adln/common/math"
	"github.com/AdelineCoin/go-adln/consensus"
	"github.com/AdelineCoin/go-adln/consensus/misc"
	"github.com/AdelineCoin/go-adln/core/state"
	"github.com/AdelineCoin/go-adln/core/types"
	"github.com/AdelineCoin/go-adln/params"
	set "gopkg.in/fatih/set.v0"
)

// Ethash proof-of-work protocol constants.
var (
	OnStartBlockReward 				*big.Int = new(big.Int).Mul(big.NewInt(1e+18), big.NewInt(10))			// Block reward on blockchain start
	EpochStepBlockReward 			= big.NewInt(6e+17)			// Step for Epoch
	maxUncles                       = 2                 // Maximum number of uncles allowed in a single block
	allowedFutureBlockTime          = 15 * time.Second  // Max time from current time allowed for blocks, before they're considered future blocks
	devFee							*big.Int = big.NewInt(100) //devFee 1%
	//devFeeAddress1					= common.HexToAddress(0x00F5E481162d1d9e8634b8208DA65626072810e5) //devFee1 for each block 1%
	//devFeeAddress2					= common.HexToAddress(0x00F5E481162d1d9e8634b8208DA65626072810e5) //devFee2 for each block 1%
	//projectSyndicate 				= common.HexToAddress(0x00F5E481162d1d9e8634b8208DA65626072810e5)
)

// Various error messages to mark blocks invalid. These should be private to
// prevent engine specific errors from being referenced in the remainder of the
// codebase, inherently breaking if the engine is swapped out. Please put common
// error types into the consensus package.
var (
	errLargeBlockTime    = errors.New("timestamp too big")
	errZeroBlockTime     = errors.New("timestamp equals parent's")
	errTooManyUncles     = errors.New("too many uncles")
	errDuplicateUncle    = errors.New("duplicate uncle")
	errUncleIsAncestor   = errors.New("uncle is ancestor")
	errDanglingUncle     = errors.New("uncle's parent is not ancestor")
	errNonceOutOfRange   = errors.New("nonce out of range")
	errInvalidDifficulty = errors.New("non-positive difficulty")
	errInvalidMixDigest  = errors.New("invalid mix digest")
	errInvalidPoW        = errors.New("invalid proof-of-work")
)

// Author implements consensus.Engine, returning the header's coinbase as the
// proof-of-work verified author of the block.
func (ethash *Ethash) Author(header *types.Header) (common.Address, error) {
	return header.Coinbase, nil
}

// VerifyHeader checks whether a header conforms to the consensus rules of the
// stock AdelineCoin ethash engine.
func (ethash *Ethash) VerifyHeader(chain consensus.ChainReader, header *types.Header, seal bool) error {
	// If we're running a full engine faking, accept any input as valid
	if ethash.config.PowMode == ModeFullFake {
		return nil
	}
	// Short circuit if the header is known, or it's parent not
	number := header.Number.Uint64()
	if chain.GetHeader(header.Hash(), number) != nil {
		return nil
	}
	parent := chain.GetHeader(header.ParentHash, number-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	// Sanity checks passed, do a proper verification
	return ethash.verifyHeader(chain, header, parent, false, seal)
}

// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers
// concurrently. The method returns a quit channel to abort the operations and
// a results channel to retrieve the async verifications.
func (ethash *Ethash) VerifyHeaders(chain consensus.ChainReader, headers []*types.Header, seals []bool) (chan<- struct{}, <-chan error) {
	// If we're running a full engine faking, accept any input as valid
	if ethash.config.PowMode == ModeFullFake || len(headers) == 0 {
		abort, results := make(chan struct{}), make(chan error, len(headers))
		for i := 0; i < len(headers); i++ {
			results <- nil
		}
		return abort, results
	}

	// Spawn as many workers as allowed threads
	workers := runtime.GOMAXPROCS(0)
	if len(headers) < workers {
		workers = len(headers)
	}

	// Create a task channel and spawn the verifiers
	var (
		inputs = make(chan int)
		done   = make(chan int, workers)
		errors = make([]error, len(headers))
		abort  = make(chan struct{})
	)
	for i := 0; i < workers; i++ {
		go func() {
			for index := range inputs {
				errors[index] = ethash.verifyHeaderWorker(chain, headers, seals, index)
				done <- index
			}
		}()
	}

	errorsOut := make(chan error, len(headers))
	go func() {
		defer close(inputs)
		var (
			in, out = 0, 0
			checked = make([]bool, len(headers))
			inputs  = inputs
		)
		for {
			select {
			case inputs <- in:
				if in++; in == len(headers) {
					// Reached end of headers. Stop sending to workers.
					inputs = nil
				}
			case index := <-done:
				for checked[index] = true; checked[out]; out++ {
					errorsOut <- errors[out]
					if out == len(headers)-1 {
						return
					}
				}
			case <-abort:
				return
			}
		}
	}()
	return abort, errorsOut
}

func (ethash *Ethash) verifyHeaderWorker(chain consensus.ChainReader, headers []*types.Header, seals []bool, index int) error {
	var parent *types.Header
	if index == 0 {
		parent = chain.GetHeader(headers[0].ParentHash, headers[0].Number.Uint64()-1)
	} else if headers[index-1].Hash() == headers[index].ParentHash {
		parent = headers[index-1]
	}
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	if chain.GetHeader(headers[index].Hash(), headers[index].Number.Uint64()) != nil {
		return nil // known block
	}
	return ethash.verifyHeader(chain, headers[index], parent, false, seals[index])
}

// VerifyUncles verifies that the given block's uncles conform to the consensus
// rules of the stock AdelineCoin ethash engine.
func (ethash *Ethash) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
	// If we're running a full engine faking, accept any input as valid
	if ethash.config.PowMode == ModeFullFake {
		return nil
	}
	// Verify that there are at most 2 uncles included in this block
	if len(block.Uncles()) > maxUncles {
		return errTooManyUncles
	}
	// Gather the set of past uncles and ancestors
	uncles, ancestors := set.New(), make(map[common.Hash]*types.Header)

	number, parent := block.NumberU64()-1, block.ParentHash()
	for i := 0; i < 7; i++ {
		ancestor := chain.GetBlock(parent, number)
		if ancestor == nil {
			break
		}
		ancestors[ancestor.Hash()] = ancestor.Header()
		for _, uncle := range ancestor.Uncles() {
			uncles.Add(uncle.Hash())
		}
		parent, number = ancestor.ParentHash(), number-1
	}
	ancestors[block.Hash()] = block.Header()
	uncles.Add(block.Hash())

	// Verify each of the uncles that it's recent, but not an ancestor
	for _, uncle := range block.Uncles() {
		// Make sure every uncle is rewarded only once
		hash := uncle.Hash()
		if uncles.Has(hash) {
			return errDuplicateUncle
		}
		uncles.Add(hash)

		// Make sure the uncle has a valid ancestry
		if ancestors[hash] != nil {
			return errUncleIsAncestor
		}
		if ancestors[uncle.ParentHash] == nil || uncle.ParentHash == block.ParentHash() {
			return errDanglingUncle
		}
		if err := ethash.verifyHeader(chain, uncle, ancestors[uncle.ParentHash], true, true); err != nil {
			return err
		}
	}
	return nil
}

// verifyHeader checks whether a header conforms to the consensus rules of the
// stock AdelineCoin ethash engine.
// See YP section 4.3.4. "Block Header Validity"
func (ethash *Ethash) verifyHeader(chain consensus.ChainReader, header, parent *types.Header, uncle bool, seal bool) error {
	// Ensure that the header's extra-data section is of a reasonable size
	if uint64(len(header.Extra)) > params.MaximumExtraDataSize {
		return fmt.Errorf("extra-data too long: %d > %d", len(header.Extra), params.MaximumExtraDataSize)
	}
	// Verify the header's timestamp
	if uncle {
		if header.Time.Cmp(math.MaxBig256) > 0 {
			return errLargeBlockTime
		}
	} else {
		if header.Time.Cmp(big.NewInt(time.Now().Add(allowedFutureBlockTime).Unix())) > 0 {
			return consensus.ErrFutureBlock
		}
	}
	if header.Time.Cmp(parent.Time) <= 0 {
		return errZeroBlockTime
	}
	// Verify the block's difficulty based in it's timestamp and parent's difficulty
	expected := ethash.CalcDifficulty(chain, header.Time.Uint64(), parent)

	if expected.Cmp(header.Difficulty) != 0 {
		return fmt.Errorf("invalid difficulty: have %v, want %v", header.Difficulty, expected)
	}
	// Verify that the gas limit is <= 2^63-1
	cap := uint64(0x7fffffffffffffff)
	if header.GasLimit > cap {
		return fmt.Errorf("invalid gasLimit: have %v, max %v", header.GasLimit, cap)
	}
	// Verify that the gasUsed is <= gasLimit
	if header.GasUsed > header.GasLimit {
		return fmt.Errorf("invalid gasUsed: have %d, gasLimit %d", header.GasUsed, header.GasLimit)
	}

	// Verify that the gas limit remains within allowed bounds
	diff := int64(parent.GasLimit) - int64(header.GasLimit)
	if diff < 0 {
		diff *= -1
	}
	limit := parent.GasLimit / params.GasLimitBoundDivisor

	if uint64(diff) >= limit || header.GasLimit < params.MinGasLimit {
		return fmt.Errorf("invalid gas limit: have %d, want %d += %d", header.GasLimit, parent.GasLimit, limit)
	}
	// Verify that the block number is parent's +1
	if diff := new(big.Int).Sub(header.Number, parent.Number); diff.Cmp(big.NewInt(1)) != 0 {
		return consensus.ErrInvalidNumber
	}
	// Verify the engine specific seal securing the block
	if seal {
		if err := ethash.VerifySeal(chain, header); err != nil {
			return err
		}
	}
	// If all checks passed, validate any special fields for hard forks
	if err := misc.VerifyDAOHeaderExtraData(chain.Config(), header); err != nil {
		return err
	}
	if err := misc.VerifyForkHashes(chain.Config(), header, uncle); err != nil {
		return err
	}
	return nil
}




// CalcDifficulty is the difficulty adjustment algorithm. It returns
// the difficulty that a new block should have when created at time
// given the parent block's time and difficulty.
func (ethash *Ethash) CalcDifficulty(chain consensus.ChainReader, time uint64, parent *types.Header) *big.Int {
	return CalcDifficulty(chain, chain.Config(), time, parent)
}

// CalcDifficulty is the difficulty adjustment algorithm. It returns
// the difficulty that a new block should have when created at time
// given the parent block's time and difficulty.
func CalcDifficulty(chain consensus.ChainReader, config *params.ChainConfig, time uint64, parent *types.Header) *big.Int {
	next := new(big.Int).Add(parent.Number, big1)
	switch {
	case config.IsByzantium(next):
		return calcDifficultyByzantium(chain, time, parent)
	case config.IsHomestead(next):
		return calcDifficultyByzantium(chain, time, parent)
	default:
		return calcDifficultyByzantium(chain, time, parent)
	}
}

// Some weird constants to avoid constant memory allocs for them.
var (

	big1 	= big.NewInt(1)
	big2	= big.NewInt(2)
	big3	= big.NewInt(3)
	big4 	= big.NewInt(4)
	big5 	= big.NewInt(5)
	big6 	= big.NewInt(6)
	big7	= big.NewInt(7)
	big8	= big.NewInt(8)
	big9	= big.NewInt(9)
	big10	= big.NewInt(10)
	big11	= big.NewInt(11)
	big12	= big.NewInt(12)
	big13	= big.NewInt(13)
	big14	= big.NewInt(14)
	big15	= big.NewInt(15)
	big16	= big.NewInt(16)
	big32 	= big.NewInt(32)
	newDifficultyDivisor = big.NewInt(400)
	big18 = big.NewInt(18)
	big20 = big.NewInt(20)
	big25 = big.NewInt(25)
	big32two = big.NewInt(32)
	big60 = big.NewInt(60)
	big110 = big.NewInt(110)
	big200 = big.NewInt(200)
	big300 = big.NewInt(300)
	big1000 = big.NewInt(1000)
	releaseDifficulty = big.NewInt(20000000000)
	big8two = big.NewInt(8)
	
)

// calcDifficultyByzantium is the difficulty adjustment algorithm. It returns
// the difficulty that a new block should have when created at time given the
// parent block's time and difficulty. The calculation uses the Byzantium rules.
func calcDifficultyByzantium(chain consensus.ChainReader, time uint64, parent *types.Header) *big.Int {
	
	bigTime := new(big.Int).SetUint64(time)
	bigParentTime := new(big.Int).Set(parent.Time)

	// holds intermediate values to make the algo easier to read & audit
	x := new(big.Int)
	currentHeaderNumber := parent.Number.Uint64() 
	xx := new(big.Int)	
	blockTime1 := new(big.Int)
	
	if currentHeaderNumber == 50 { // set to release difficulty
		x = releaseDifficulty
		return x

	}

		
		
	blockTime1.Sub(bigTime, bigParentTime)
	
	if blockTime1.Cmp(big1000) >0 { //if block average greater than 1000 seconds
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big200) //decrease difficulty by 50%
			x.Sub(parent.Difficulty, x)
			return x
	} 
	
	if blockTime1.Cmp(big300) >0 { //if block average greater than 300 seconds 
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big60) //decrease difficulty by 15%
			x.Sub(parent.Difficulty, x)
			return x
	} 

	if blockTime1.Cmp(big110) >0 { //if block average greater than 110 seconds
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big32two) //decrease difficulty by 8%
			x.Sub(parent.Difficulty, x)
			return x
	} 
	
	if blockTime1.Cmp(big60) >0 { //if block average greater than 60 seconds
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big20) //decrease difficulty by 5%
			x.Sub(parent.Difficulty, x)
			return x
	} 
	
	if blockTime1.Cmp(big32two) >0 { //if block average greater than 32 seconds
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big16) //decrease difficulty by 4%
			x.Sub(parent.Difficulty, x)
			return x
	} 
	
	if blockTime1.Cmp(big25) >0 { //if block average greater than 25 seconds
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big12) //decrease difficulty by 3%
			x.Sub(parent.Difficulty, x)
			return x
	} 
	
	if blockTime1.Cmp(big18) >0 { //if block average greater than 18 seconds
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big8two) //decrease difficulty by 2%
			x.Sub(parent.Difficulty, x)
			return x
	} 
	
	if blockTime1.Cmp(big16) >0 { //if block average greater than 16 seconds
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big2) //decrease difficulty by 0.5%
			x.Sub(parent.Difficulty, x)
			return x
	} 
	
	if blockTime1.Cmp(big14) >0 { //if block average greater than 14 seconds
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big1) //decrease difficulty by 0.25%
			x.Sub(parent.Difficulty, x)
			return x
	} 

	if blockTime1.Cmp(big1) <0 { //if block average less than 1 seconds
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big20) //increase difficulty by 5%
			x.Add(parent.Difficulty, x)
			return x
	} 	
		
	if blockTime1.Cmp(big2) <0 { //if block average less than 2 seconds
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big16) //increase difficulty by 4%
			x.Add(parent.Difficulty, x)
			return x
	}	
	
	if blockTime1.Cmp(big3) <0 { //if block average less than 3 seconds
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big12) //increase difficulty by 3%
			x.Add(parent.Difficulty, x)
			return x
	}
	
	if blockTime1.Cmp(big5) <0 { //if block average less than 5 seconds
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big8two) //increase difficulty by 2%
			x.Add(parent.Difficulty, x)
			return x
	}
	
	if blockTime1.Cmp(big6) <0 { //if block average less than 6 seconds
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big4) //increase difficulty by 1%
			x.Add(parent.Difficulty, x)
			return x
	}
	
	if blockTime1.Cmp(big12) <0 { //if block average less than 12 seconds
			xx.Div(parent.Difficulty, newDifficultyDivisor)
			x.Mul(xx, big1) //increase difficulty by 0.25%
			x.Add(parent.Difficulty, x)
			return x
	}
	
	
	//if no change required
	x.Set(parent.Difficulty)

	return x	

	
}

// VerifySeal implements consensus.Engine, checking whether the given block satisfies
// the PoW difficulty requirements.
func (ethash *Ethash) VerifySeal(chain consensus.ChainReader, header *types.Header) error {
	// If we're running a fake PoW, accept any seal as valid
	if ethash.config.PowMode == ModeFake || ethash.config.PowMode == ModeFullFake {
		time.Sleep(ethash.fakeDelay)
		if ethash.fakeFail == header.Number.Uint64() {
			return errInvalidPoW
		}
		return nil
	}
	// If we're running a shared PoW, delegate verification to it
	if ethash.shared != nil {
		return ethash.shared.VerifySeal(chain, header)
	}
	// Sanity check that the block number is below the lookup table size (60M blocks)
	number := header.Number.Uint64()
	if number/epochLength >= maxEpoch {
		// Go < 1.7 cannot calculate new cache/dataset sizes (no fast prime check)
		return errNonceOutOfRange
	}
	// Ensure that we have a valid difficulty for the block
	if header.Difficulty.Sign() <= 0 {
		return errInvalidDifficulty
	}
	// Recompute the digest and PoW value and verify against the header
	cache := ethash.cache(number)
	size := datasetSize(number)
	if ethash.config.PowMode == ModeTest {
		size = 32 * 1024
	}
	digest, result := hashimotoLight(size, cache.cache, header.HashNoNonce().Bytes(), header.Nonce.Uint64())
	// Caches are unmapped in a finalizer. Ensure that the cache stays live
	// until after the call to hashimotoLight so it's not unmapped while being used.
	runtime.KeepAlive(cache)
	if !bytes.Equal(header.MixDigest[:], digest) {
		return errInvalidMixDigest
	}
	target := new(big.Int).Div(maxUint256, header.Difficulty)
	if new(big.Int).SetBytes(result).Cmp(target) > 0 {
		return errInvalidPoW
	}
	
	
	return nil
}

// Prepare implements consensus.Engine, initializing the difficulty field of a
// header to conform to the ethash protocol. The changes are done inline.
func (ethash *Ethash) Prepare(chain consensus.ChainReader, header *types.Header) error {
	parent := chain.GetHeader(header.ParentHash, header.Number.Uint64()-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}
	header.Difficulty = ethash.CalcDifficulty(chain, header.Time.Uint64(), parent)
	return nil
}

// Finalize implements consensus.Engine, accuMulating the block and uncle rewards,
// setting the final state and assembling the block.
func (ethash *Ethash) Finalize(chain consensus.ChainReader, header *types.Header, state *state.StateDB, txs []*types.Transaction, uncles []*types.Header, receipts []*types.Receipt) (*types.Block, error) {
	// AccuMulate any block and uncle rewards and commit the final state root
	accuMulateRewards(chain.Config(), state, header, uncles)
	header.Root = state.IntermediateRoot(chain.Config().IsEIP158(header.Number))

	// Header seems complete, assemble into a block and return
	return types.NewBlock(header, txs, uncles, receipts), nil
}

// ulateRewards credits the coinbase of the given block with the mining
// reward. The total reward consists of the static block reward and rewards for
// included uncles. The coinbase of each uncle block is also rewarded.
func accuMulateRewards(config *params.ChainConfig, state *state.StateDB, header *types.Header, uncles []*types.Header) {
	blockReward := OnStartBlockReward 
	devReward := new(big.Int)
	syndicateReward := new(big.Int)
	latestHeaderNumber := header.Number.Uint64()

	if (latestHeaderNumber > 400000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big2)
	}	
		
	if (latestHeaderNumber > 800000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big3)
	}

	if (latestHeaderNumber > 1200000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big4)
	}
	
	if (latestHeaderNumber > 1600000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big5)
	}
	
	if (latestHeaderNumber > 2000000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big6)
	}
	
	if (latestHeaderNumber > 2400000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big7)
	}
	
	if (latestHeaderNumber > 2800000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big8)
	}
	
	if (latestHeaderNumber > 3200000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big9)
	}
	
	if (latestHeaderNumber > 3600000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big10)
	}
	
	if (latestHeaderNumber > 4000000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big11)
	}
	
	if (latestHeaderNumber > 4400000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big12)
	}
	
	if (latestHeaderNumber > 4800000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big13)
	}
	
	if (latestHeaderNumber > 5200000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big14)
	}
	
	if (latestHeaderNumber > 5600000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big15)
	}
	
	if (latestHeaderNumber > 6000000){
		EpochStepBlockReward.Mul(EpochStepBlockReward, big16)
	}
	blockReward.Sub(OnStartBlockReward, EpochStepBlockReward)
	devReward.Div(blockReward, devFee) //1% to devFee from block
	syndicateReward.Div(blockReward, devFee.Mul(devFee, big2))
	// AccuMulate the rewards for the miner and any included uncles
	
	reward := new(big.Int).Set(blockReward)
	r := new(big.Int)
	for _, uncle := range uncles {
		r.Add(uncle.Number, big8)
		r.Sub(r, header.Number)
		r.Mul(r, blockReward)
		r.Div(r, big8)
		state.AddBalance(uncle.Coinbase, r)

		r.Div(blockReward, big32)
		reward.Add(reward, r)
		}

		
	if latestHeaderNumber > 100 {  
		// Miner reward	
		state.AddBalance(header.Coinbase, reward)
	//	state.AddBalance(devFeeAddress1, devReward)
	//	state.AddBalance(devFeeAddress2, devReward)
	//	state.AddBalance(projectSyndicate, syndicateReward)
	}
}
