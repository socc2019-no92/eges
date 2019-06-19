package election

import (
	"net"
	"fmt"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/core/geecCore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rlp"
	"encoding/binary"
	"strconv"
)

type Server struct{
	ipstr string
	portstr string

	port int

	currentBlock uint64
	coinbase     common.Address

	conn *net.UDPConn

	state geecCore.State

	electMsgCh chan *electMessage
	electSuccessCh chan uint64


}

func NewServer(ipstr string, portstr string, myAddr common.Address, state geecCore.State) *Server{
	s := new(Server)
	s.portstr = portstr
	s.port, _ = strconv.Atoi(portstr)
	s.ipstr = ipstr
	s.coinbase = myAddr
	s.state = state

	udpAddress, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", s.port))
	if err != nil{
		log.Crit("Failed to resolve UDP addr for election Server", "Port", s.port)

	}

	s.conn, err = net.ListenUDP("udp", udpAddress)
	if err != nil{
		log.Crit("Failed to listen UDP addr for election Server", "Port", s.port)
	}

	s.electMsgCh = make (chan *electMessage, 1024)
	s.electSuccessCh = make(chan uint64, 1024)

	go s.handleElectMessage()


	return s
}

type ElectParameters struct{
	Candidates []*geecCore.GeecMember
	BlkNum     uint64
	Version    uint64
}




func (s *Server) HandleMessage(){
	for {
		var buf [1024]byte
		n, addr, err := s.conn.ReadFromUDP(buf[:])


		if err != nil {
			log.Crit("Failed to read from udp conn", "err", err)
		}

		msg := new(geecCore.GeecUDPMsg)

		err = rlp.DecodeBytes(buf[0:n], msg)

		if err != nil {
			log.Warn("Failed to decode udp message", "err", err, "msg len", n, "addr", addr.String(), "message", buf)
			//There may be aribitary UDP packets from the internet.
			continue
		}

		log.Debug("received udp packets", "type", msg.Code, "source", addr, "Payload length", len(msg.Payload))


		switch msg.Code {
		case geecCore.GeecExaimeReply:
			reply := new(geecCore.ValidateReply)
			err = rlp.DecodeBytes(msg.Payload[:], reply)

			if err != nil {
				log.Crit("Failed to decode examine reply", "err", err)
			}
			s.state.RecvExamineReply(reply)
		case geecCore.GeecElectMsg:
			em := new(electMessage)
			err = rlp.DecodeBytes(msg.Payload[:], em)
			if err != nil {
				log.Crit("Failed to decode elect message", "err", err)
			}
			s.electMsgCh <- em

		case geecCore.GeecQueryReply:
			reply := new(geecCore.QueryReply)
			err = rlp.DecodeBytes(msg.Payload[:], reply)
			s.state.RecvQueryReply(reply)



		}

	}
}

func AddrToInt (address common.Address) uint64{
	return binary.BigEndian.Uint64(address[0:8]) +
		binary.BigEndian.Uint64(address[8:16]) + uint64(binary.BigEndian.Uint32(address[16:20]))
}




