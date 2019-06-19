package geec

import (
	"errors"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"math/big"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/geecCore"
	"github.com/ethereum/go-ethereum/event"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/node"
	"math/rand"
	"sync"

	//"github.com/naoina/toml/ast"
	//"time"
	"github.com/ethereum/go-ethereum/params"
	"strconv"
	"time"
)

var (
	// errUnknownBlock is returned when the list of signers is requested for a block
	// that is not part of the local blockchain.
	errUnknownBlock = errors.New("unknown block")
	ErrNoCommittee  = errors.New("not a committee member")
	ErrNoLeader     = errors.New("not leader, cannot generate a block")
)



type Geec struct{
	config *params.GeecConfig
	nodecfg *node.Config
	InitialAccounts []common.Address

	Mux *event.TypeMux
	Coinbase common.Address

	PendingGeecTxnLock sync.Mutex
	PendingGeecTxns []*types.Transaction


	InitChan chan interface{}
	initDone bool
	Registered chan interface{}

	ConsensusIP string
	ConsensusPort string

	gs *core.GeecState

	validateTimeout int
	backoffTime int

	eth geecCore.ThwMiner

	txnPerBlock int
	txnDataLen int

	Breakdown   bool
	failureTest bool

	last_seal_finsih_time time.Time
	last_leader bool
}






func New (coinbase common.Address, nodecfg *node.Config, config *params.GeecConfig, mux *event.TypeMux, eth geecCore.ThwMiner) *Geec {
	//set missing configs


	g := new(Geec)
	g.config = config
	//
	//
	//go validator_thread_func(g.validate_blocks, g.validate_abort, g.validate_errors)
	g.Mux = mux


	g.nodecfg = nodecfg
	g.ConsensusIP = nodecfg.ConsensusIP
	g.ConsensusPort = nodecfg.ConsensusPort
	g.Coinbase = coinbase

	g.eth = eth


	g.InitChan = make(chan interface{}, 2)
	g.initDone = false
	g.Registered = make(chan interface{}, 2)

	g.validateTimeout = config.ValidateTimeout

	g.backoffTime = config.BackoffTime

	g.txnPerBlock = nodecfg.TxnPerBlock
	g.txnDataLen = nodecfg.TxnSize //byte

	g.Breakdown = nodecfg.Breakdown
	g.failureTest = nodecfg.FailureTest


	log.Geec("Created Geec consensus Engine",
		"Coin Base", coinbase,
		"IP", g.ConsensusIP,
		"port", g.ConsensusPort,
		"validate Timeout", g.validateTimeout,
		"backofftime", g.backoffTime,
		"Breakdown", g.Breakdown,
		"failure_test", g.failureTest)

	if nodecfg.GeecTxnPort != 0{
		go g.TxnService(nodecfg.GeecTxnPort)
	}



	return g
}


/*
Bootstarp the initial state.
Cannot be called in the New method because the state is not created yet.
 */
func (g *Geec) Bootstrap (chain consensus.ChainReader){
	state := chain.GetGeecState()
	g.gs = state.(*core.GeecState)


	// a new goroutine to handle the registeration.
	go g.gs.Register(g.Mux, g.ConsensusIP, g.ConsensusPort, 0)
}


//put the author (the leader of the committee)
func (g *Geec) Author(header *types.Header) (common.Address, error){
	return header.Coinbase, nil
}


//used to verify header downloaded from other peers.
func (g *Geec) VerifyHeader (chain consensus.ChainReader, header *types.Header, seal bool) error {

	err := g.verifyHeader(chain, header, nil);
	return err
}

// VerifyHeaders is similar to VerifyHeader, but verifies a batch of headers
// concurrently. The method returns a quit channel to abort the operations and
// a results channel to retrieve the async verifications.
//
// XS: its an async function.
func (g *Geec) VerifyHeaders(chain consensus.ChainReader, headers []*types.Header, seals []bool) (chan<- struct{}, <-chan error) {
	abort := make(chan struct{})
	results := make(chan error, len(headers))

	go func() {
		for i, header := range headers {
			err := g.verifyHeader(chain, header, headers[:i])

			select {
			case <-abort:
				return
			case results <- err:
			}
		}
	}()
	return abort, results
}


// verifyHeader checks whether a header conforms to the consensus rules.The
// caller may optionally pass in a batch of parents (ascending order) to avoid
// looking those up from the database. This is useful for concurrently verifying
// a batch of new headers.
func (g *Geec) verifyHeader(chain consensus.ChainReader, header *types.Header, parents []*types.Header) error {
	//step 1: Sanity check.

	if header.Number == nil {
		return errUnknownBlock
	}
	number := header.Number.Uint64()
	//already in the local chain
	if chain.GetHeader(header.Hash(), number) != nil {
		return nil
	}
	//same as ethash, check ancestor first
	var parent *types.Header

	if parents == nil || len(parents) == 0 {
		parent = chain.GetHeader(header.ParentHash, number-1)
		if parent == nil {
			return consensus.ErrUnknownAncestor
		}
	}else{
		parent = parents[0]
	}

	return nil
}



func (g *Geec) VerifyUncles(chain consensus.ChainReader, block *types.Block) error {
	//does not support uncles
	if len(block.Uncles()) > 0 {
		return errors.New("uncles not allowed")
	}
	return nil
}

//double check the seal of an outgoing message.
func (g *Geec) VerifySeal(chain consensus.ChainReader, header *types.Header) error {
	//It is currently a double check.
	return nil
}

func (g *Geec) Prepare(chain consensus.ChainReader, header *types.Header) error {

	if g.Breakdown && g.last_leader {
		elapsed := time.Since(g.last_seal_finsih_time)
		log.Info("[Breakdown] local processing time", "time", elapsed)
	}
	g.last_leader = false


	if ! g.initDone {
		g.Bootstrap(chain)
		g.initDone = true
	}

	header.Regs = g.gs.GetPendingRegs()


	number := header.Number.Uint64()
	log.Gdbug("Preparing block", "number", number )

	isCommittee := g.gs.IsCommittee(header.Number.Uint64(), 0)

	if !isCommittee{
		return ErrNoCommittee
	}
	header.Nonce = types.BlockNonce{} //empty

	header.Difficulty = big.NewInt(100)
	header.MixDigest = common.Hash{} //empty

	parent := chain.GetHeader(header.ParentHash, number-1)
	if parent == nil {
		return consensus.ErrUnknownAncestor
	}

	return nil
}


//ensuring no uncles are set. No
func (g *Geec) Finalize(chain consensus.ChainReader, header *types.Header, state *state.StateDB, txs []*types.Transaction,
	uncles []*types.Header, receipts []*types.Receipt) (*types.Block, error){


	header.Root = state.IntermediateRoot(true)
	header.UncleHash = types.CalcUncleHash(nil)
	//TODO: Whether the rewards should come from here.


	return types.NewBlock(header, txs, nil, receipts), nil

}

//Major function to achieve consensus.
func (g *Geec) Seal (chain consensus.ChainReader, block *types.Block, stop <-chan struct{}) (*types.Block, error){
	//attempt to achieve consensus.
	var t1, t2, t3 time.Time
	if g.Breakdown {
		t1 = time.Now()
	}
	blkNum := block.NumberU64()
	var seed uint64
	seed = uint64(rand.Uint32())<<32 + uint64(rand.Uint32())

	header := block.Header()
	header.TrustRand = seed
	block = block.WithSeal(header)

	if blkNum == 1{
		time.Sleep(10 * time.Second)
	}


	//if g.failureTest == true && blkNum % 100 == 0{
	//	return  nil, errors.New("Cancel On purpose, testing failure scenario")
	//}


	ret := g.gs.ElectForProposer(blkNum, 0, stop)

	if ret == -1{
		return nil, ErrNoLeader
	}


	if g.Breakdown {
		t2 = time.Now()
		log.Info("[Breakdown 1] Election time", "time",  t2.Sub(t1).String())
		//log.Info("T2", "block", blkNum, "time", t2.String())
	}

	var geecTxns []*types.Transaction
	g.PendingGeecTxnLock.Lock()
	nPendingTxn := len(g.PendingGeecTxns)
	var nGeecTxn int
	if nPendingTxn > g.txnPerBlock {
		nGeecTxn = g.txnPerBlock
	}else{
		nGeecTxn = nPendingTxn
	}
	geecTxns = append(geecTxns, g.PendingGeecTxns[:nGeecTxn]...)
	g.PendingGeecTxns = g.PendingGeecTxns[nGeecTxn:]
	g.PendingGeecTxnLock.Unlock()
	block.GeecTxns = geecTxns

	var fakeTxns []*types.Transaction
	fakeData := make([]byte, g.txnDataLen)
	for i:= 0; i< g.txnPerBlock - nGeecTxn; i++{
		txn := types.NewTransaction(0, g.Coinbase, big.NewInt(0), 0, big.NewInt(0), fakeData)
		fakeTxns = append(fakeTxns,txn)
	}
	block.FakeTxns = fakeTxns



	supporters, err := g.AskForAck(block, 0, stop)
	if err != nil{
		return nil, err
	}
	if g.Breakdown {
		t3 = time.Now()
		log.Info("[Breakdown 2] Asking for ACK", "time", t3.Sub(t2).String())

		//elapsed := time.Since(start)
		//fmt.Println("[Breakdown 2] validate time", elapsed)
		g.last_leader = true
		g.last_seal_finsih_time = t3
	}
	time.Sleep(time.Duration(g.backoffTime) * time.Millisecond)
	//if g.failureTest == true && blkNum % 50 == 0 {
	//	return  nil, errors.New("Cancel On purpose, testing failure scenario")
	//}

	confirm := new(types.ConfirmBlockMsg)
	confirm.Supporters = supporters
	confirm.EmptyBlock = false
	confirm.Confidence = g.CalcConfidence(chain, block)
	confirm.Hash = block.Hash()
	confirm.BlockNumber = blkNum
	block.ConfirmMessage = confirm
	return block, nil;

}


func (g *Geec) AskForAck(block *types.Block, version uint64, stop  <-chan struct{})(supporters []common.Address, err error){
	blkNum := block.NumberU64()
	req := new(geecCore.ValidateRequest)
	req.BlockNum = block.NumberU64()
	req.Author = g.Coinbase
	req.Retry = 0
	req.Version = version
	port, _ := strconv.Atoi(g.ConsensusPort)
	req.Port = uint(port)
	req.IPstr = g.ConsensusIP
	req.Version = 0
	req.Block = block
	g.gs.EmptyBlockListLock.Lock()

	log.Gdbug("Checking emptyList", "number", block.NumberU64(), "list", g.gs.EmptyBlockList)
	//copy the slice rather than copy the pointer.
	copy(req.EmptyList, g.gs.EmptyBlockList)
	g.gs.EmptyBlockListLock.Unlock()


	g.PostEvent(core.ValidateBlockEvent{req})


	for{
		select {
		case result := <-g.gs.ExamineSuccessChannel:
			if result.BlockNum != req.BlockNum {
				log.Warn("Received Validate Success for the wrong block", "Waiting for", req.BlockNum, "Received", blkNum)
				g.gs.ExamineSuccessChannel <- result
				time.Sleep(10 * time.Millisecond)
			} else {
				//The validate succeeded.

				log.Geec("Got majority ACKs", "Block num", blkNum, "nsupporters", len(result.Supporters))

				return result.Supporters, nil
			}
		case <-time.After(time.Duration(g.validateTimeout) * time.Millisecond):
			req.Retry = req.Retry + 1
			log.Geec("retry proposing", "retry", req.Retry)
			g.PostEvent(core.ValidateBlockEvent{req})
		case <- stop:
			return nil, errors.New("Seal stopped by event")
		}
	}

}




//A toy function to calculate the confidence of current block
func (g *Geec) CalcConfidence(chain consensus.ChainReader, block *types.Block) uint64{
	/*
	FIX IT: Current genesis block has nil pointer on confidence.
	Not considering failure sc
	 */
	 if block.NumberU64() == 1{
    	return 10000
	}
	parentNumber, parentHash := block.NumberU64()-1, block.ParentHash()
	parent := chain.GetBlock(parentHash, parentNumber)
	if parent == nil{
		log.Warn("Failed to get parent")
		return 0
	}

	return geecCore.CalcConfidence(parent.ConfirmMessage.Confidence, block)
}

func (g *Geec) CalcDifficulty(chain consensus.ChainReader, time uint64, parent *types.Header) *big.Int {
	//Can use this function to change the protocol parameters.

	return big.NewInt(1)
}


func (g *Geec) APIs(chain consensus.ChainReader) []rpc.API {
	return []rpc.API{{
		Namespace: "thw",
		Version:   "1.0",
		Service:   &API{chain: chain, thw: g},
		Public:    false,
	}}
}


func (g *Geec) PostEvent(ev interface{}) error{
	return g.Mux.Post(ev)
}

func (g *Geec)  GetEthBase() common.Address {
	return g.Coinbase
}

func (g *Geec) GetMiner() geecCore.ThwMiner{
	return g.eth
}


func (g *Geec) GetConsensusIPPort() (string, string){
	return g.ConsensusIP, g.ConsensusPort
}


func (g *Geec) GetNodeCfg() (cfg *node.Config){
	return g.nodecfg
}