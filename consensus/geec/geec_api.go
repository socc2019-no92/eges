package geec

import (
	"fmt"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"math/big"
	"net"
)


func (g *Geec) TxnService(port int){

	udpAddress, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", port))
	if err != nil{
		log.Crit("Failed to resolve UDP addr for election Server", "Port", port)

	}
	l, err := net.ListenUDP("udp", udpAddress)
	if err != nil{
		log.Crit("Failed to create geec API", "err", err)
	}
	defer l.Close()
	log.Geec("Geec API Listening", "port", port)
	for {
		var buf [1024]byte
		n, _, err := l.ReadFromUDP(buf[:])
		if err != nil {
			log.Crit("Geec txn failed to read from udp conn", "err", err)
		}


		txn := types.NewTransaction(0, g.Coinbase, big.NewInt(0), 0, big.NewInt(0), buf[:n])

		txn.SetIsGeec()

		g.PendingGeecTxnLock.Lock()
		g.PendingGeecTxns = append(g.PendingGeecTxns, txn)
		g.PendingGeecTxnLock.Unlock()

		log.Gdbug("Geec received transaction from UDP port", "Content", string(buf[:n]))
	}

}
