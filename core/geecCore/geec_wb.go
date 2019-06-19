package geecCore

import (
	"github.com/emirpasic/gods/maps/hashmap"
	"github.com/emirpasic/gods/sets/hashset"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/log"
	"math/rand"
	"sync"
)

const(
	ELEC_Candidate = 0x01
	ELEC_Voted     = 0x02
	ELEC_ELECTED   = 0x03
)



type WorkingBlock struct{
	BlkNum          uint64
	//PendingBlock    []byte
	MaxVersion       int64
	MaxValidateRetry int64
	MaxValidateVersion int64
	MaxQueryRetry 	 int64
	Mu               *sync.Mutex
	//Electing for proposer
	ElectState  uint8
	Supporters  *hashset.Set
	MyRand      uint64
	Delegator   common.Address
	NCandidates uint64
	ElectionThreshold uint64

	Delegator_Ipstr string
	Delegator_Port  uint
	Max_Election_Retry uint64
	//validating
	IsProposer        bool
	ValidateReplies   *hashmap.Map
	ValidateThreshold uint64
	ValidateSucceeded bool
	//query
	QueryReplies *hashmap.Map
	QueryEmptyCount   uint64
	QueryNonEmptyCount uint64
	QueryThreshold uint64
	QueryRecvMajority bool
	//util, not changed during move
	R			*rand.Rand
	Coinbase 	common.Address
	Cond	*sync.Cond


}

func NewWorkingBlock(coinbase common.Address) *WorkingBlock{
	wb := new(WorkingBlock)
	wb.Mu = new(sync.Mutex)
	wb.Cond = sync.NewCond(wb.Mu)
	wb.Mu.Lock()
	defer wb.Mu.Unlock()
	wb.Coinbase = coinbase
	wb.ValidateReplies = hashmap.New()
	wb.QueryReplies = hashmap.New()
	wb.Supporters = hashset.New()
	ss := rand.NewSource(int64(coinbase.Big().Uint64()))
	wb.R = rand.New(ss)
	wb.Move(1)
	return wb
}

const(
	WB_PASSED = 0x00
	WB_CURRENT = 0x01
	WB_TIMEOUT = 0x02
)
/*
IMPORTANT
This method must be called when the lock is held.
 */
func (wb *WorkingBlock) Move(blkNum uint64){

	log.Gdbug("moved to next working blk", "blknum", blkNum)

	wb.BlkNum = blkNum
	wb.MaxVersion = -1
	wb.MaxValidateRetry = -1
	wb.MaxQueryRetry = -1
	wb.ElectState = ELEC_Candidate
	wb.Supporters.Clear()
	wb.Delegator = wb.Coinbase
	wb.NCandidates = math.MaxUint64
	wb.ElectionThreshold = math.MaxUint64
	wb.Max_Election_Retry = 0


	wb.ValidateReplies.Clear()
	wb.MyRand = wb.R.Uint64()
	wb.IsProposer = false
	wb.ValidateThreshold = math.MaxUint64
	wb.ValidateSucceeded = false
	wb.Cond.Broadcast()
}

/*
IMPORTANT
This method must be called when the lock is held.
 */
 //xstodo: needs a timeout mechanism to prevent stucking here in timeout.





func (wb *WorkingBlock) Wait(num uint64) int{
	if wb.BlkNum > num {
		return WB_PASSED
	}else if wb.BlkNum < num {
		//t1 := time.Now()
		log.Debug("Started waiting for block", "block", num)
		for wb.BlkNum < num {
			wb.Cond.Wait()
		}
		//t2 := time.Now()

		log.Debug("Waited for working block", "block", num)
	}
	if wb.BlkNum == num{
		return WB_CURRENT
	}else{
		return WB_PASSED //already passed
	}
}