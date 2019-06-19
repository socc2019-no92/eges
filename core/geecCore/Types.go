package geecCore

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"net"
)

type GeecMember struct {
	Referee     common.Address
	Addr        common.Address
	JoinedBlock uint64 //at which round the candidate joined as candidate
	TTL         uint64 //the ``total'' term for the candidate
	Ip          net.IP
	Port        int
	RenewedTimes 	uint64
}


type ValidateRequest struct {
	BlockNum uint64
	Author   common.Address
	Retry uint64
	Version uint64
	IPstr string
	Port uint

	Block *types.Block
	EmptyList []uint64
}

type ValidateReply struct {
	BlockNum uint64
	Author   common.Address
	Accepted bool
	Retry uint64
	FillBlocks []*types.Block //Thats are not real empty block.
}



type QueryReply struct {
	BlockNum uint64
	Author   common.Address
	Empty bool
	BlockHash common.Hash
	Retry uint64
	Version uint64
}

type ThwMiner interface{
	StopMining()
	StartMining(local bool) error
	IsMining() bool
}


const(
	GeecExaimeReply = 0x01
	GeecElectMsg = 0x02
	GeecQueryReply = 0x03

)


type GeecUDPMsg struct{
	Code uint8
	Author common.Address
	Payload []byte
}


type ProposeResult struct{
	BlockNum uint64
	Supporters []common.Address
}

const(
	QUERY_EMPTY = 0
	QUERY_CONFIRMED = 1
	QUERY_UNCONFIRMED = 2
)


type QueryResult struct{
	BlockNum uint64
	Supporters []common.Address
	Stat uint
	Version uint64
	Confimred bool
	Hash common.Hash
}