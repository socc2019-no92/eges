package geec

import (
	"github.com/ethereum/go-ethereum/consensus"
	"github.com/ethereum/go-ethereum/common"
)

type API struct {
	chain  consensus.ChainReader
	thw *Geec
}

func (api *API) Register(coinbase common.Address)(bool, error){
	return true, nil
}