package core

import (
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/emirpasic/gods/maps/treemap"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/geec/election"
	"github.com/ethereum/go-ethereum/core/geecCore"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
	"math"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	//"time"
	"time"
	"github.com/ethereum/go-ethereum/node"
)



var(
	ErrNoCandidate = errors.New("not a GeecMember")
	ErrInitFailed  = errors.New("Geec State Init Failed")
	ErrInvalidReg  = errors.New("invalid Registration Transaction Format")
)

type ProtocolManagerInterface interface {
	InsertBlock(blk *types.Block)
}

func (gs *GeecState) SetPm(pm interface{}){
	gs.Pm, _ = pm.(ProtocolManagerInterface)
}


type GeecState struct {
	/*
	pointers to useful structures
	 */
	bc      *BlockChain
	chainDB ethdb.Database
	miner   geecCore.ThwMiner
	Pm ProtocolManagerInterface
	handlerMux *event.TypeMux
	nodecfg *node.Config



	mu sync.Mutex
	//members
	candidateList *treemap.Map    //key: addr, value: *GeecMember
	candidateCount uint64




	//event loop for handling state transition
	newBlockChannel chan *types.Block


	Coinbase common.Address

	pendingReg *treemap.Map

	Registered chan interface{}
	Registering int32


	QuerySuccessChannel   chan *geecCore.QueryResult
	queryReplyChannel 	  chan *geecCore.QueryReply
	examineReplyChannel   chan *geecCore.ValidateReply
	ExamineSuccessChannel chan *geecCore.ProposeResult

	//MaxBlock uint64
	Wb *geecCore.WorkingBlock

	trustRandList *treemap.Map
	trustRandLock sync.Mutex


	//Pending Block, and its lock
	PendingBlocks *treemap.Map
	MaxConfirmedBlock uint64
	PendingBlockLock sync.Mutex
	/*
	**Unconfirmed** Empty (timeout) block number list
	The blocks number is removed once it is confirmed.
	 */
	EmptyBlockList		[]uint64
	EmptyBlockListLock	sync.Mutex
	/*
	Blocks received but not yet confirmed.
	This is because there is an empty block before it.
	The blocks's transaction need to be applied **in order**, so it need to wait for the empty blocks to be confirmed.
	 */
	unconfirmedBlocks []*types.Block
	unconfirmedBlocksLock sync.Mutex
	Ipstr string
	portStr string



	confidenceThreshold uint64

	es *election.Server



	/*
	Parameters::
	 */
	nCandidates uint64
	nAcceptors  uint64
	//Committee Timeout, in s.
	blockTimeout int
	//Reg Timeout
	regTimeout int

	queryTimeout int
	//Election Timeout
	electionTimeout int
	//Max Reg to be put into each block
	MaxRegPerBlk int
	//Whether count breakdown time
	breakdown bool
	//
	failureTest bool

	totalNodes uint64

	InitialTTL        uint64
	bonusTTL          uint64
	renewTTLThreshold uint64
	maxTTL            uint64
	TTLInterval       uint64
}





func (gs *GeecState) SetTrustRand(blknum uint64, rand uint64){
	gs.trustRandLock.Lock()
	defer gs.trustRandLock.Unlock()
	gs.trustRandList.Put(blknum, rand)
}

func (gs *GeecState) GetTrustRand(blknum uint64) (rand uint64,exist bool){
	gs.trustRandLock.Lock()
	defer gs.trustRandLock.Unlock()

	//xstodo: consider to change to real random number.
	return blknum, true

	//
	//r, exist := gs.trustRandList.Get(blknum)
	//if exist {
	//	return r.(uint64), true
	//} else {
	//	return 0, false
	//}

}


/*
The simple comparator functions used for compare
the address as key for tree map.
 */
func addressComparator(a, b interface{}) int{
	addr1 := a.(common.Address)
	addr2 := b.(common.Address)

	for i := 0; i < common.AddressLength; i++{
		if addr1[i] < addr2[i]{
			return -1
		}else if addr1[i] > addr2[i] {
			return 1
		}
	}
	return 0
}

func uint64Comparator(a, b interface{}) int{
	x1 := a.(uint64)
	x2 := b.(uint64)

	if x1 < x2{
		return  -1
	}else if x1 > x2{
		return 1
	}else{
		return 0
	}
}

func (gs *GeecState) Init(chain interface{}, db interface{}, coinbase common.Address) error { //TODO: set parameters
	gs.mu.Lock()
	defer gs.mu.Unlock()

	gs.candidateList = treemap.NewWith(addressComparator)
	gs.pendingReg = treemap.NewWith(addressComparator)
	gs.trustRandList = treemap.NewWith(uint64Comparator)
	gs.SetTrustRand(uint64(0), uint64(0))
	gs.PendingBlocks = treemap.NewWith(uint64Comparator)

	bc, ok := chain.(*BlockChain)
	if !ok {
		log.Crit("Faild to init geec state")
		return ErrInitFailed
	}
	gs.bc = bc

	chainDb, ok := db.(ethdb.Database)
	if !ok {
		log.Crit("Faild to init geec state")
		return ErrInitFailed
	}
	gs.chainDB = chainDb


	gs.confidenceThreshold = 9999

	gs.Coinbase = coinbase
	gs.nodecfg = gs.bc.engine.GetNodeCfg()


	//the eventLoop
	gs.newBlockChannel = make(chan *types.Block, 1024)
	gs.examineReplyChannel = make(chan *geecCore.ValidateReply, 1024)
	gs.ExamineSuccessChannel = make(chan *geecCore.ProposeResult, 1024)
	gs.queryReplyChannel = make(chan *geecCore.QueryReply, 1024)
	gs.QuerySuccessChannel = make(chan *geecCore.QueryResult, 1024)


	thwConfig := bc.Config().THW
	if (thwConfig == nil){
		log.Crit("Failed to parse the Geec config in genesis file")
	}

	gs.nAcceptors = uint64(gs.nodecfg.NAccetpors)
	gs.nCandidates = uint64(gs.nodecfg.NCandidates)
	gs.blockTimeout = gs.nodecfg.BlockTimeout
	gs.breakdown = gs.nodecfg.Breakdown
	gs.failureTest = gs.nodecfg.FailureTest
	gs.totalNodes = uint64(gs.nodecfg.TotalNodes)


	gs.MaxRegPerBlk = thwConfig.MaxRegPerBlk
	gs.regTimeout = thwConfig.RegTimeout
	gs.electionTimeout = thwConfig.ElectionTimeout
	gs.queryTimeout = thwConfig.ValidateTimeout

	if gs.totalNodes > 200{
		gs.InitialTTL = 200
	}else if gs.totalNodes < 50{
		gs.InitialTTL = 50
	}else{
		gs.InitialTTL = gs.totalNodes
	}
	gs.bonusTTL = 20
	gs.renewTTLThreshold = 20
	gs.maxTTL = gs.InitialTTL
	gs.TTLInterval = 10


	for _, node := range(thwConfig.BootstrapNodes) {
		candidate := new(geecCore.GeecMember)
		candidate.JoinedBlock = 0
		decoded, _ := hex.DecodeString(node.Account)
		copy(candidate.Addr[:],decoded)
		copy(candidate.Referee[:], decoded)
		candidate.Ip = net.ParseIP(node.IpStr)
		port, err := strconv.Atoi(node.PortStr)
		if err != nil{
			log.Crit("Wrong Port format", "value", port)
		}
		candidate.Port = port
		candidate.TTL = gs.InitialTTL
		gs.AddGeecMember(candidate)
	}

	gs.Ipstr, gs.portStr = gs.bc.engine.GetConsensusIPPort()

	gs.es = election.NewServer(gs.Ipstr, gs.portStr, coinbase, gs)
	gs.Wb = geecCore.NewWorkingBlock(coinbase)




	log.Geec("State initialized",
		"Coinbase", coinbase,
		"NAcceptors", gs.nAcceptors,
		"NCandidates", gs.nCandidates,
		"Block Timeout", gs.blockTimeout,
		"txn per block", gs.nodecfg.TxnPerBlock,
		"txn size", gs.nodecfg.TxnSize,
		"total nodes", gs.totalNodes,
		"MaxRegPerBlk", gs.MaxRegPerBlk,
		"regTimeout", gs.regTimeout,
		"electionTimeout", gs.electionTimeout)

	gs.Registered = make(chan interface{}, 1024000)

	gs.miner = bc.engine.GetMiner()

	go gs.blockLoop()           //event driven on new blocks
	go gs.handleVerifyReplies() //handling reply for examine.
	go gs.handleQueryReply()
	go gs.es.HandleMessage()

	return nil
}



/* This method should be called when the lock is held */
func (gs *GeecState) AddGeecMember (candidate *geecCore.GeecMember) error{
	ret, found := gs.candidateList.Get(candidate.Addr)
	if found == true{
		//found an candidate
		c, ok := ret.(*geecCore.GeecMember)
		if !ok {
			fmt.Println("Wrong type in the hash map")
			panic("Wrong type in the hash map")
		}
		//renew the

		c.RenewedTimes = candidate.RenewedTimes
		c.TTL = c.TTL + candidate.TTL
		if c.TTL > gs.maxTTL{
			c.TTL = gs.maxTTL
		}

		log.Geec("Renewed GeecMember", "addr", candidate.Addr, "port", c.Port)

	}else{
		//not found in the list
		gs.candidateList.Put(candidate.Addr, candidate)
		gs.candidateCount++
		log.Geec("Added GeecMember", "addr", candidate.Addr, "port", candidate.Port, "count", gs.candidateCount)
	}

	return nil
}

/*
IMPORTANT: This method is called when holding the lock
 */
func (gs *GeecState) getAllCommittee(seed uint64) []*geecCore.GeecMember {
	var list []*geecCore.GeecMember
	//defer log.Geec("Get all committee", "size", len(list))
	it := gs.candidateList.Iterator()

	size := uint64(gs.candidateList.Size())
	if size < gs.nCandidates {
		//All candidates are committee.
		for it.Begin(); it.Next(); {
			cand, success := it.Value().(*geecCore.GeecMember)
			if success == false {
				log.Crit("Failed to cast candidate for getAllCommittee")
			}
			list = append(list, cand)
		}
		return list

	}else {
		start := seed % size

		if start+gs.nCandidates > size {
			/*
			0...ncommittee-size+start-1		total ncommittee-size+start
			start....size-1 (end)  			total (size-start)
			 */
			count := uint64(0)
			for it.Begin(); it.Next() && count < gs.nCandidates-size+start; count++ {
				cand, success := it.Value().(*geecCore.GeecMember)
				if success == false {
					log.Crit("Failed to cast candidate for getAllCommittee")
				}
				list = append(list, cand)
			}

			count = uint64(0)

			for it.End(); it.Prev() && count < size-start; count++{
				cand, success := it.Value().(*geecCore.GeecMember)
				if success == false {
					log.Crit("Failed to cast candidate for getAllCommittee")
				}
				list = append(list, cand)
			}
			return list
		} else {
			//start ....  start+ncommittee-1
			index := uint64(0)
			for it.Begin(); it.Next() && index < start+gs.nCandidates; index++ {
				if index < start {
					continue
				}
				cand, success := it.Value().(*geecCore.GeecMember)
				if success == false {
					log.Crit("Failed to cast candidate for getAllCommittee")
				}
				list = append(list, cand)
			}
			return list

		}
	}
}

func (gs *GeecState) getAcceptorCount() uint64{
	size := uint64(gs.candidateList.Size())
	if size < gs.nAcceptors {
		return size
	}else{
		return gs.nAcceptors
	}
}





func (gs *GeecState) CandidateCount() uint64{
	return gs.candidateCount
}


func (gs *GeecState) IsValidator (blknum uint64) (result bool){
	//First check whether I am candidate
	//check against my coin addreess.
	var seed uint64
	exist := false
	seed, exist = gs.GetTrustRand(blknum-1);

	count := 0

	for exist == false && count < 20 {
		log.Geec("Falied to get trust rand, waiting for 10ms", "blk", blknum-1, "retry", count)
		time.Sleep(10 * time.Millisecond)
		seed, exist = gs.GetTrustRand(blknum-1);
		count ++
	}
	if exist == false{
		log.Geec("Cannot get seed, give up", "Blk", blknum)
		return false
	}


	//XSTODO: copied from getallcommittee.
	//Copied from get
	it := gs.candidateList.Iterator()

	size := uint64(gs.candidateList.Size())
	if size < gs.nAcceptors {
		//All candidates validator
		return true

	}else {
		start := seed % size

		if start+gs.nAcceptors > size {
			/*
			0...nVal-size+start-1		total nVal-size+start
			start....size-1 (end)  			total (size-start)
			 */
			count := uint64(0)
			for it.Begin(); it.Next() && count < gs.nAcceptors-size+start; count++ {
				cand, success := it.Value().(*geecCore.GeecMember)
				if success == false {
					log.Crit("Failed to cast candidate for getAllCommittee")
				}
				if addressComparator(cand.Addr, gs.Coinbase) == 0{
					return  true
				}
			}

			count = uint64(0)

			for it.End(); it.Prev() && count < size-start; count++{
				cand, success := it.Value().(*geecCore.GeecMember)
				if success == false {
					log.Crit("Failed to cast candidate for getAllCommittee")
				}
				if addressComparator(cand.Addr, gs.Coinbase) == 0{
					return  true
				}
			}
			return false
		} else {
			//start ....  start+ncommittee-1
			index := uint64(0)
			for it.Begin(); it.Next() && index < start+gs.nAcceptors; index++ {
				if index < start {
					continue
				}
				cand, success := it.Value().(*geecCore.GeecMember)
				if success == false {
					log.Crit("Failed to cast candidate for getAllCommittee")
				}
				if addressComparator(cand.Addr, gs.Coinbase) == 0{
					return  true
				}
			}
			return false
		}
	}



}



/*
This funcction is called when the WB.MU is locked
 */
func (gs *GeecState) Validate (req *geecCore.ValidateRequest){


	//log.Geec("Received pending block", "len", len(gs.Wb.PendingBlock))

	isValidator := gs.IsValidator(req.BlockNum)
	if isValidator{
		log.Gdbug("I am Acceptor, sending reply", "Blk", req.BlockNum)
	}else{
		log.Gdbug("I am not Acceptor", "Blk", req.BlockNum)
		return
	}





	reply := new(geecCore.ValidateReply)
	reply.BlockNum = req.BlockNum
	reply.Author = gs.Coinbase
	reply.Retry = req.Retry
	valResult := true
	reply.Accepted = valResult




	if req.EmptyList != nil{
		for _, emptyBlkNum := range req.EmptyList {
			log.Geec("Propose request asking for empty block", "number", emptyBlkNum)

			block := gs.bc.GetBlockByNumber(emptyBlkNum)
			if block != nil {
				reply.FillBlocks = append(reply.FillBlocks, block)
			}
		}
	}




	payload, err := rlp.EncodeToBytes(reply)
	if err != nil{
		log.Crit("Failed to encode Examine reply")
	}

	msg := new(geecCore.GeecUDPMsg)
	msg.Code = geecCore.GeecExaimeReply
	msg.Author = gs.Coinbase
	msg.Payload = payload

	buffer, err := rlp.EncodeToBytes(msg)
	if err != nil{
		log.Crit("Failed to encode Examine reply")
	}

	conn, err := net.Dial("udp", fmt.Sprintf("%s:%d", req.IPstr, req.Port))
	if err != nil{
		log.Crit("Failed Dial while sending back reply", "err", err)
	}
	defer conn.Close()

	conn.Write(buffer)
}




//This method must be single-threaded (sync) to prevent problems.
func (gs *GeecState) NotifyNewBlock (blk *types.Block){
	gs.newBlockChannel <- blk
	//Think about it: whether it need to wait the handle finish.
}





func (gs *GeecState) ElectForProposer(blkNum uint64, version uint64, stop <-chan struct{}) int{


		gs.Wb.Mu.Lock()

		if blkNum != gs.Wb.BlkNum {
			log.Warn("Electing for proposer for the wrong blk", "blknum", blkNum, "wb.blknum", gs.Wb.BlkNum)
			gs.Wb.Mu.Unlock()
			return -1
		}
		gs.Wb.Mu.Unlock()

		ep := new(election.ElectParameters)
		seed, exist := gs.GetTrustRand(blkNum-1)
		if !exist {
			//This should not happen in normal.
			log.Crit("[proposer] Failed to get rand for last blk", "Last block", blkNum-1)

			return -1
		}

		if version > 0{
			seed = uint64(math.Pow(float64(seed), float64(version)))
		}



		ep.Candidates = gs.getAllCommittee(seed)
		log.Geec("Electing for proposer", "Blk", blkNum, "Version", version, "Ncandidates", len(ep.Candidates))
		for i, c := range ep.Candidates{
			log.Gdbug("Candiate", "index", i, "addr", fmt.Sprintf("%s:%d", c.Ip.String(), c.Port))
		}

		ep.BlkNum = blkNum
		ep.Version = version
		ret := gs.es.Elect(ep, stop)


		if ret == 1 {
			gs.Wb.Mu.Lock()
			gs.Wb.IsProposer = true
			/*
			Does not Minus one for itself.
			Because itself does **NOT** need to be an acceptor.
			 */
			gs.Wb.ValidateThreshold = uint64(math.Ceil(float64(gs.getAcceptorCount() + 1)  / 2.0))


			gs.Wb.Mu.Unlock()

			log.Geec("Elected as proposer", "Blk", blkNum, "Version", version, "NAcceptors", gs.getAcceptorCount(), "ValidateThreshold", gs.Wb.ValidateThreshold)
			return 1
		}else{
			return -1
		}
}



/*
Append the newly received reg request in the pending requests tree map
This function is not written considering efficiency.
 */
func (gs *GeecState) AppendRegReq(registratoin *types.Registratoin){
	gs.mu.Lock()
	defer gs.mu.Unlock()

	r, exist := gs.pendingReg.Get(registratoin.Account)
	if exist {
		record := r.(*types.Registratoin)
		if record.IpStr == registratoin.IpStr && record.PortStr == registratoin.PortStr && record.Renew <= registratoin.Renew{
			log.Geec("Received an Registration request already known")
			return
		}
	}
	gs.pendingReg.Put(registratoin.Account, registratoin)
	log.Geec("Added an Registration request", "Account", registratoin.Account, "IP", registratoin.IpStr, "Port", registratoin.PortStr)
}

func (gs *GeecState) GetPendingRegs() (regs []*types.Registratoin){
	gs.mu.Lock()
	defer gs.mu.Unlock()

	count := 0

	it := gs.pendingReg.Iterator()
	for it.Begin(); it.Next(); {
		reg, success := it.Value().(*types.Registratoin)
		if success == false {
			log.Crit("Failed to cast from pendingRequest")
		}
		regs = append(regs, reg)
		count++
		if count >= gs.MaxRegPerBlk{
			break
		}
	}
	return regs
}

func (gs *GeecState) Register (mux *event.TypeMux,	ipstr string, portStr string, renew uint64){

	if mux == nil{
		mux = gs.handlerMux
	}else{
		gs.handlerMux = mux
	}
	//if it is the bootstrap node, stop the registration.
	if atomic.CompareAndSwapInt32(&gs.Registering, 0, 1) == false {
		log.Geec("Another thread is registering", "give up")
		return
	}
	defer atomic.StoreInt32(&gs.Registering, 0)


	result, found := gs.candidateList.Get(gs.Coinbase)
	if found {
		m, _ := result.(*geecCore.GeecMember)
		if m.RenewedTimes >= renew {
			log.Geec("Already handled renew msg", "membered renewed times", m.RenewedTimes, "Renew", renew)
			return
		}
	} else{
		log.Geec("I am not a candidate, trying to register", "list", gs.candidateList.Keys(), "Coinbase", gs.Coinbase)
	}


	reg := new(types.Registratoin)
	reg.PortStr = portStr
	reg.IpStr = ipstr
	reg.Referee = gs.Coinbase
	reg.Account = gs.Coinbase
	reg.Signature = types.FakeSignature[:]
	reg.Renew = renew


	i := 0
	mux.Post(RegisterReqEvent{reg})
	log.Geec("Registering Geec", "renew", renew)

	for{
		select{
		case <-time.After( time.Duration(gs.regTimeout) * time.Second):
			i++ //wait for more time.
			mux.Post(RegisterReqEvent{reg})
			log.Geec("Registering thw", "retry", i)
		case <-gs.Registered:
			log.Geec("Registering succeeded")
			return
		}
	}
}


/*
IMPORTANT: this method must be called when gs.Mu lock is held.
 */
func (gs *GeecState) IsMember (addr common.Address) bool{
	_, found := gs.candidateList.Get(addr)
	return found
}



func (gs *GeecState) IsCommittee(blknum uint64, version uint64) (result bool){
	//First check whether I am candidate
	//check against my coin addreess.


	gs.Wb.Mu.Lock()

	wait := gs.Wb.Wait(blknum)

	if wait == geecCore.WB_PASSED{
		log.Gdbug("iscommittee, WB passed", "wait for", blknum, "cur", gs.Wb.BlkNum)
		gs.Wb.Mu.Unlock()
		return false
	}
	gs.Wb.Mu.Unlock()

	seed, exist := gs.GetTrustRand(blknum-1)

	if exist == false{
		log.Gdbug("Cannot get seed, give up", "Blk", blknum)
		return false
	}

	gs.mu.Lock()
	defer gs.mu.Unlock()

	if !gs.IsMember(gs.Coinbase){
		return false
	}

	if version > 0{
		seed = uint64(math.Pow(float64(seed), float64(version + 1)))
		log.Geec("Checking membership", "version", version, "seed", seed)
	}


	it := gs.candidateList.Iterator()

	size := uint64(gs.candidateList.Size())
	if size < gs.nCandidates {
		//All candidates are proposer
		return true

	}else {
		start := seed % size
		log.Debug("Is proposer check", "seed", seed, "size", size)
		if start+gs.nCandidates > size {
			/*
			0...nVal-size+start-1		total nVal-size+start
			start....size-1 (end)  			total (size-start)
			 */
			count := uint64(0)
			for it.Begin(); it.Next() && count < gs.nCandidates-size+start; count++ {
				cand, success := it.Value().(*geecCore.GeecMember)
				if success == false {
					log.Crit("Failed to cast candidate for getAllCommittee")
				}
				if addressComparator(cand.Addr, gs.Coinbase) == 0{
					return  true
				}
			}

			count = uint64(0)
			for it.End(); it.Prev() && count < size-start; count++{
				cand, success := it.Value().(*geecCore.GeecMember)
				if success == false {
					log.Crit("Failed to cast candidate for getAllCommittee")
				}
				if addressComparator(cand.Addr, gs.Coinbase) == 0{
					return  true
				}
			}
			return false
		} else {
			//start ....  start+ncommittee-1
			index := uint64(0)
			for it.Begin(); it.Next() && index < start+gs.nCandidates; index++ {
				if index < start {
					continue
				}
				cand, success := it.Value().(*geecCore.GeecMember)
				if success == false {
					log.Crit("Failed to cast candidate for getAllCommittee")
				}
				if addressComparator(cand.Addr, gs.Coinbase) == 0{
					return  true
				}
			}
			return false
		}
	}
}


func (gs *GeecState) RecvExamineReply (reply *geecCore.ValidateReply){

	log.Gdbug("Received Examine Reply")
	gs.examineReplyChannel <- reply
}

func (gs *GeecState) RecvQueryReply (reply *geecCore.QueryReply){
	log.Gdbug("Received Query reply", "author", reply.Author, "empty", reply.Empty)
	gs.queryReplyChannel <- reply
}


func (gs *GeecState) GetWorkingBlock() *geecCore.WorkingBlock{
	return gs.Wb
}


/*
This function is called when the lock is held.
 */

func (gs *GeecState) GenerateEmptyBlock(last uint64) *types.Block{
	gs.bc.Lock()
	defer gs.bc.Unlock()
	parent := gs.bc.CurrentBlock()
	num := parent.Number()
	if num.Uint64() != last {
		log.Warn("\n\n\n\n Already not on the block", "last", last, "Header", num)
		return nil
	}


	log.Gdbug("Current Block number", "number", parent.NumberU64())
	parentTimeUnix := parent.Time()



	header := &types.Header{
		ParentHash: parent.Hash(),
		Number:     num.Add(num, common.Big1),
		GasLimit:   CalcGasLimit(parent),
		Extra:      []byte{},
		Time:       parentTimeUnix.Add(parentTimeUnix, common.Big1),
		Nonce:		types.BlockNonce{}, //empty
		Difficulty: common.Big1,
		MixDigest:  common.Hash{},  //empty
		Coinbase:   types.EmptyAddr,
		Root: 		parent.Root(), //xstodo: a bad but effective way... because we don't insert txns.
	}
	log.Gdbug("created header", "NUM", header.Number.Uint64())


	emptyBlk := types.NewBlock(header, nil, nil, nil)

	log.Gdbug("created empty block", "NUM", emptyBlk.NumberU64())

	return emptyBlk
}





func (gs *GeecState) HandleBlockTimeout(last uint64){
	log.Warn("Timeout for new block!!!!\n")
	//insert an empty block to skip the current block. .
	gs.mu.Lock()
	defer gs.mu.Unlock()

	emptyBlk := gs.GenerateEmptyBlock(last)
	if emptyBlk == nil{
		log.Warn("Already has that block", "last", last)
		return
	}


	gs.EmptyBlockListLock.Lock()
	gs.EmptyBlockList = append(gs.EmptyBlockList, emptyBlk.NumberU64())
	gs.EmptyBlockListLock.Unlock()

	confirm := new(types.ConfirmBlockMsg)

	confirm.Hash = emptyBlk.Hash()
	confirm.BlockNumber = emptyBlk.NumberU64()
	confirm.Confidence = uint64(0)

	emptyBlk.ConfirmMessage = confirm

	gs.Pm.InsertBlock(emptyBlk)
}
//
//func (gs *GeecState)  handleGeecTxn(){
//	PnedingBlocks :=
//}




//The blocks are supposed to come ``in order''.
//This is guaranteeed by the insert function
func (gs *GeecState) handleNewBlock (blk *types.Block){
	gs.mu.Lock()
	defer gs.mu.Unlock()


	var confidence uint64
	if blk.ConfirmMessage != nil {
		log.Geec("handleNewBlock", "Blocknum", blk.NumberU64(), "Geec Txns", blk.GeecTxns.Len(), "Fake Txns", blk.FakeTxns.Len(), "author", blk.Coinbase(), "seed", blk.Header().TrustRand, "Confidence", blk.ConfirmMessage.Confidence)
		confidence = blk.ConfirmMessage.Confidence
	}else{
		log.Geec("handleNewBlock", "Blocknum", blk.NumberU64(), "Geec Txns", blk.GeecTxns.Len(), "Fake Txns", blk.FakeTxns.Len(), "author", blk.Coinbase(), "seed", blk.Header().TrustRand)
	}


	if blk.Coinbase() == types.EmptyAddr{
	//This is an empty block.
	gs.EmptyBlockListLock.Lock()
	exist := false
	for _, i := range gs.EmptyBlockList{
		if i == blk.NumberU64(){
			exist = true
			break
		}
	}
	if exist == false{
		gs.EmptyBlockList = append(gs.EmptyBlockList, blk.NumberU64())
	}
	gs.EmptyBlockListLock.Unlock()

	}



	gs.SetTrustRand(blk.NumberU64(), blk.Header().TrustRand)


	gs.unconfirmedBlocksLock.Lock()
	gs.unconfirmedBlocks = append(gs.unconfirmedBlocks, blk)
	gs.unconfirmedBlocksLock.Unlock()

	if confidence > gs.confidenceThreshold {
		//The block is confirmed
		gs.handleConfirmedBlock()
	}

	gs.Wb.Mu.Lock()
	if blk.NumberU64() >= gs.Wb.BlkNum{
		//start to work on the next block
		gs.Wb.Move(blk.NumberU64()+1)
	}
	gs.Wb.Mu.Unlock()



}


func (gs *GeecState) handleConfirmedBlock(){
	gs.unconfirmedBlocksLock.Lock()
	defer gs.unconfirmedBlocksLock.Unlock()

	for _, blk := range gs.unconfirmedBlocks {
		/*
		Handle the registration first
		Because those candidate registered can be in the list.
		 */
		for _, reg := range blk.Header().Regs {
			log.Geec("Received Registration", "blk", blk.NumberU64(), "ip", reg.IpStr, "port", reg.PortStr, "renew", reg.Renew)
			//** remove the pending registration request
			ret, found := gs.pendingReg.Get(reg.Account)
			if found{
				localReg, _ := ret.(*types.Registratoin)
				if localReg.Renew <= reg.Renew {
					gs.pendingReg.Remove(reg.Account)
				}
			}
			//Add the candidate to list
			m := new(geecCore.GeecMember)
			m.Referee = reg.Referee
			m.Addr = reg.Account
			m.JoinedBlock = blk.NumberU64()
			m.TTL = gs.InitialTTL
			m.RenewedTimes = reg.Renew

			port, err := strconv.Atoi(reg.PortStr)
			if err != nil {
				log.Warn("Failed to parse Port string, ignore", "Port", reg.PortStr)
				continue
			}
			m.Port = port
			m.Ip = net.ParseIP(reg.IpStr)
			gs.AddGeecMember(m)

			//If my account is registered, stop registering
			if addressComparator(gs.Coinbase, reg.Account) == 0 {
				gs.Registered <- true
			}
		}
		for _, txn := range blk.GeecTxns{
			//Tian Kai todo:
			log.Geec("Received Geec Txn from blockchain", "content", string(txn.Data()[:]))
		}


		if gs.failureTest {
			gs.CheckMembership(blk)
		}




	}
	log.Gdbug("Handle confirmed block", "start", gs.unconfirmedBlocks[0].NumberU64(), "end", gs.unconfirmedBlocks[len(gs.unconfirmedBlocks)-1].NumberU64())
	gs.unconfirmedBlocks = nil
	gs.EmptyBlockListLock.Lock()
	gs.EmptyBlockList = nil
	gs.EmptyBlockListLock.Unlock()

}

/*
IMPORTANT: This function is called when
Gs.Mu is locked
 */
func (gs *GeecState) CheckMembership(blk *types.Block){
	t1 := time.Now()
	defer func(){
		t2 := time.Now()
		log.Geec("ChecMembership Time", "time", t2.Sub(t1).String())
	}()

	for _, addr := range append(blk.ConfirmMessage.Supporters, blk.Coinbase()){
		result, found := gs.candidateList.Get(addr)
		if found{
			m, _ := result.(*geecCore.GeecMember)
			m.TTL = m.TTL + gs.bonusTTL
			if m.TTL > gs.maxTTL{
				m.TTL = gs.maxTTL
			}
			log.Geec("Given bonus TTL", "account", m.Addr, "port", m.Port, "TTL", m.TTL)
		}
	}

	if blk.NumberU64() % gs.TTLInterval == 0 {
		it := gs.candidateList.Iterator()
		for it.Begin(); it.Next(); {
			m, _ := it.Value().(*geecCore.GeecMember)
			log.Geec("member TTL", "ip", m.Ip, "port", m.Port, "TTL", m.TTL)

			if m.TTL <= gs.TTLInterval {
				//remove it.
				log.Geec("Removing member", "account", m.Addr, "ip", m.Ip, "port", m.Port )
				gs.candidateList.Remove(m.Addr)
			}else {
				m.TTL = m.TTL - gs.TTLInterval
			}
			if m.Addr == gs.Coinbase {
				log.Geec("My memebership TTL", "remaining", m.TTL)
				if m.TTL <= gs.renewTTLThreshold {
					log.Geec("Need to renew membership before it expires")
					go gs.Register(nil, m.Ip.String(), strconv.FormatInt(int64(m.Port), 10), m.RenewedTimes+1)
				}
			}
		}
	}
}


func (gs *GeecState) blockLoop(){
	var timeoutTimes int
	var stopChan chan struct{}
	stopChanClosed := true

	var MaxBlock uint64


	for{
		select{
		case blk := <- gs.newBlockChannel:
			if stopChanClosed == false{
				close(stopChan)
				stopChanClosed = true
			}
			timeoutTimes = 0
			gs.handleNewBlock(blk)
			MaxBlock = blk.NumberU64()
		case <- time.After(time.Duration(gs.blockTimeout) * time.Second):
			/*
			Don't do timeout on init
			 */
			gs.Wb.Mu.Lock()
			if gs.Wb.BlkNum == 1{
				gs.Wb.Mu.Unlock()
				continue
			}
			gs.Wb.Mu.Unlock()



			if timeoutTimes < 3{
				if timeoutTimes > 0{
					close(stopChan)
					stopChanClosed = true
				}
				timeoutTimes++
				stop := make(chan struct{})
				stopChan = stop
				stopChanClosed = false
				go gs.HandleCommitteeTimeout(uint64(timeoutTimes), stop, MaxBlock)
			}else{
				close(stopChan)
				stopChanClosed = true
				timeoutTimes = 0
				gs.HandleBlockTimeout(MaxBlock)
			}
		}
	}
}


func (gs *GeecState) handleVerifyReplies(){
	for{
		select{
		case reply := <-gs.examineReplyChannel:
			func(){
				//a closure for easy de

				gs.Wb.Mu.Lock()
				defer gs.Wb.Mu.Unlock()
				if reply.BlockNum == gs.Wb.BlkNum{
					_, exist := gs.Wb.ValidateReplies.Get(reply.Author)
					if exist{
						return
					}
					//handle the filled in blocks
					if reply.FillBlocks != nil{
						for _, blk := range reply.FillBlocks{
							log.Geec("Received filled block", "num", blk.NumberU64(), "author", blk.Coinbase())
						}
					}




					gs.Wb.ValidateReplies.Put(reply.Author, reply.Retry)
					log.Gdbug("Received examine reply", "count", gs.Wb.ValidateReplies.Size(), "threshold", gs.Wb.ValidateThreshold)
					if uint64(gs.Wb.ValidateReplies.Size()) >= gs.Wb.ValidateThreshold {
						if gs.Wb.ValidateSucceeded == false{
							gs.Wb.ValidateSucceeded = true
							result := new(geecCore.ProposeResult)
							result.BlockNum = reply.BlockNum
							result.Supporters = make([]common.Address, gs.Wb.ValidateReplies.Size())
							addresses := gs.Wb.ValidateReplies.Keys()
							for i, addr := range addresses{
								result.Supporters[i] = addr.(common.Address)
							}
							gs.ExamineSuccessChannel <- result
						}
					}
				}
			}()
		}
	}
}



func (gs *GeecState) handleQueryReply(){
	for {
		reply := <- gs.queryReplyChannel
		func(){
			//a closure for easy de

			gs.Wb.Mu.Lock()
			defer gs.Wb.Mu.Unlock()


			if reply.BlockNum == gs.Wb.BlkNum && int64(reply.Version) == gs.Wb.MaxVersion{
				_, exist := gs.Wb.QueryReplies.Get(reply.Author)
				if exist{
					return
				}

				gs.Wb.QueryReplies.Put(reply.Author, reply.Retry)
				if reply.Empty == true{
					gs.Wb.QueryEmptyCount++
				}else{
					gs.Wb.QueryNonEmptyCount++
				}
				log.Gdbug("Received query reply", "count", gs.Wb.QueryReplies.Size(), "threshold", gs.Wb.QueryThreshold)

				if uint64(gs.Wb.QueryReplies.Size()) >= gs.Wb.QueryThreshold {
					if gs.Wb.QueryRecvMajority == false{
						gs.Wb.QueryRecvMajority = true

						result := new(geecCore.QueryResult)
						if gs.Wb.QueryEmptyCount >= gs.Wb.QueryThreshold{
							result.Stat = geecCore.QUERY_EMPTY
						}else if gs.Wb.QueryNonEmptyCount >= gs.Wb.QueryThreshold{
							result.Stat = geecCore.QUERY_CONFIRMED
							result.Hash = reply.BlockHash
						}else{
							result.Stat = geecCore.QUERY_UNCONFIRMED
						}
						result.Version = reply.Version
						result.BlockNum = reply.BlockNum
						result.Supporters = make([]common.Address, gs.Wb.QueryReplies.Size())
						addresses := gs.Wb.QueryReplies.Keys()
						for i, addr := range addresses{
							result.Supporters[i] = addr.(common.Address)
						}
						gs.QuerySuccessChannel <- result
					}
				}
			}
		}()
	}
}




func (gs *GeecState) HandleCommitteeTimeout(version uint64, stop chan struct{}, maxBlock uint64){
	log.Geec("Timeout for committee")
	//Check for committee
	gs.Wb.Mu.Lock()
	blknum := gs.Wb.BlkNum
	gs.Wb.Mu.Unlock()

	if gs.IsCommittee(blknum, version) == false{
		log.Geec("Not a committee member", "version", version)
		return
	}
	//Elect for version committee
	//result := make(chan int)


	elected := gs.ElectForProposer(blknum, version, stop)
	if elected == 1 {
		log.Geec("Elected as a high-version proposer", "version", version)
		gs.mu.Lock()
		defer gs.mu.Unlock()
		//query

		var pendingBlk *types.Block
		gs.PendingBlockLock.Lock()

		ret, exist := gs.PendingBlocks.Get(blknum)
		if exist{
			pendingBlk = ret.(*types.Block)
		}
		gs.PendingBlockLock.Unlock()


		query := new(types.QueryBlockMsg)
		query.BlockNumber = blknum
		query.Version = version
		query.IPstr = gs.Ipstr
		port, _ := strconv.Atoi(gs.portStr)
		query.Port = uint(port)
		query.Retry = 0

		gs.Wb.Mu.Lock()
		gs.Wb.QueryThreshold = uint64(math.Ceil(float64(gs.getAcceptorCount() + 1)  / 2.0))
		gs.Wb.QueryReplies.Clear()
		gs.Wb.QueryEmptyCount = 0
		gs.Wb.QueryNonEmptyCount = 0
		gs.Wb.QueryRecvMajority = false
		gs.Wb.Mu.Unlock()

		gs.handlerMux.Post(QueryReqEvent{query})
		//confirm
		for{
			select{
			case result := <- gs.QuerySuccessChannel:
				if result.BlockNum == blknum && result.Version == version{
					log.Geec("Received Majority Query Results", "blk", result.BlockNum, "version", result.Version, "Stat", result.Stat)
					//confirm

					gs.bc.Lock()
					defer gs.bc.Unlock()

					bcHead := gs.bc.CurrentBlock().NumberU64()
					if bcHead != maxBlock{
						log.Warn("Already has the block, give up on the committee event", "bcHead", bcHead, "Maxblock When timeout", maxBlock)
						return
					}
					if result.Stat == geecCore.QUERY_EMPTY {
						//confirm empty
						confirm := new(types.ConfirmBlockMsg)
						confirm.Supporters = result.Supporters
						confirm.Confidence = geecCore.CalcConfidence(gs.bc.CurrentBlock().ConfirmMessage.Confidence, nil)
						confirm.EmptyBlock = true
						confirm.BlockNumber = blknum

						gs.handlerMux.Post(ConfirmBlockEvent{confirm})

					}else if result.Stat == geecCore.QUERY_CONFIRMED{
						confirm := new(types.ConfirmBlockMsg)
						confirm.Supporters = result.Supporters
						confirm.Confidence = geecCore.CalcConfidence(gs.bc.CurrentBlock().ConfirmMessage.Confidence, nil)
						confirm.EmptyBlock = false
						confirm.BlockNumber = blknum
						confirm.Hash = result.Hash
						gs.handlerMux.Post(ConfirmBlockEvent{confirm})
					}else if result.Stat == geecCore.QUERY_UNCONFIRMED{
						//Not confirmed, need to re-elect ack.
						if pendingBlk == nil{
							log.Warn("I cannot confirm, But I don't have the block. Give up")
							return
						}
						supporters, err := gs.bc.Engine().AskForAck(pendingBlk, version, stop)
						if err != nil{
							log.Warn("Error when try to reconfirm", "Blknum", pendingBlk.NumberU64(), "error", err)
						}
						confirm := new(types.ConfirmBlockMsg)
						confirm.Supporters = supporters
						confirm.EmptyBlock = false
						confirm.Confidence = geecCore.CalcConfidence(gs.bc.CurrentBlock().ConfirmMessage.Confidence, nil)
						confirm.Hash = result.Hash
						confirm.BlockNumber = blknum
						gs.handlerMux.Post(ConfirmBlockEvent{confirm})
					}
					return
				}else{
					log.Warn("Received Query Success for the wrong block", "Waiting for", blknum, "Received", result.BlockNum)
					gs.QuerySuccessChannel <- result
					time.Sleep(10 * time.Millisecond)
				}
			case <-time.After(time.Duration(gs.queryTimeout) * time.Millisecond):
				query.Retry++
				log.Geec("retry querying", "retry", query.Retry)
				gs.handlerMux.Post(QueryReqEvent{query})
			case <- stop:
				log.Geec("Wating for query reply, stopped by external event")
				return
			}
		}
	}else {
		log.Geec("Not elected as retry leader", "version", version, "blk", blknum)
	}
}