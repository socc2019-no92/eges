package geecCore

import "github.com/ethereum/go-ethereum/core/types"

func CalcConfidence(parentConf uint64, block *types.Block) uint64 {
	c :=  parentConf + 1000
	if c > 10000{
		return 10000
	}else{
		return c
	}
}