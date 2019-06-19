package geecCore

import (
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/event"
)

//This package is created to solve the cyclic dependency



type State interface{
	Init(hc interface{}, chaindb interface{}, coninbase common.Address) error

	/* This method should be called when the lock is held */
	//AddGeecMember(candidate *GeecMember) error


	//NewTerm (start uint64, len uint64, seed uint64) error
	//Validate (writer p2p.MsgReadWriter, blockNum uint64) bool
	AppendRegReq(registratoin *types.Registratoin)

	Register (mux *event.TypeMux, ipstr string, portStr string, renew uint64)
	RecvExamineReply (reply *ValidateReply)
	GetWorkingBlock() *WorkingBlock
	RecvQueryReply (reply *QueryReply)

}