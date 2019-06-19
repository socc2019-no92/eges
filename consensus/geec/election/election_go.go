package election

import (
	"math"
	"time"
	"github.com/ethereum/go-ethereum/core/geecCore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/log"
	"fmt"
	"net"
)

const(
	 MSG_ELECT = 0x01
	 MSG_VOTE = 0x02
)



type electMessage struct{
	Code uint8

	Rand uint64
	Author common.Address
	BlockNum uint64
	Retry uint64
	Version uint64


	Ipstring string
	Port     uint
}



func (s *Server) Elect(ep *ElectParameters, stop <-chan struct{}) int{
	wb := s.state.GetWorkingBlock()
	wb.Mu.Lock()

	//check on the working block
	if wb.BlkNum < ep.BlkNum{
		log.Crit("Electing a non-working block")
	}else if wb.BlkNum > ep.BlkNum{
		log.Geec("Election failed, already received that block")
		wb.Mu.Unlock()
		return -1
	}
	//check version
	if int64(ep.Version) > wb.MaxVersion{
		wb.MaxVersion = int64(ep.Version)
		wb.MaxQueryRetry = -1
		wb.MaxValidateRetry = -1
	}else if int64(ep.Version) == wb.MaxVersion && wb.ElectState == geecCore.ELEC_Voted{
		log.Geec("Already voted on this version")
		wb.Mu.Unlock()
		return -1
	}else if int64(ep.Version) < wb.MaxVersion {
		log.Geec("election failed, already on a newer version")
		wb.Mu.Unlock()
		return -1
	}

	wb.ElectState = geecCore.ELEC_Candidate
	wb.NCandidates = uint64(len(ep.Candidates))
	wb.ElectionThreshold = uint64(math.Ceil(float64(wb.NCandidates + 1) / 2.0)) - 1
	r := wb.MyRand
	wb.Mu.Unlock()

	req := new(electMessage)
	req.BlockNum = ep.BlkNum
	req.Code = MSG_ELECT
	req.Ipstring = s.ipstr
	req.Port = uint(s.port)
	req.Author = s.coinbase
	req.Rand = r
	req.Version = ep.Version


	var conns []net.Conn

	for _, cand := range (ep.Candidates) {
		if cand.Addr == s.coinbase {
			continue //important: not sending to myself
		}
		conn, err := net.Dial("udp", fmt.Sprintf("%s:%d", cand.Ip.String(), cand.Port))
		if err != nil {
			log.Crit("Failed Dial while sending back reply", "err", err)
		}
		defer conn.Close()
		conns = append(conns, conn)
	}


	retry := 0

	for {
		req.Retry = uint64(retry)
		payload, err := rlp.EncodeToBytes(req)
		if err != nil {
			log.Crit("Failed to encode elect req")
		}

		msg := new(geecCore.GeecUDPMsg)
		msg.Code = geecCore.GeecElectMsg
		msg.Author = s.coinbase
		msg.Payload = payload
		buffer, err := rlp.EncodeToBytes(msg)
		if err != nil {
			log.Crit("Failed to encode elect reply to msg")
		}



		wb.Mu.Lock()
		if retry > 0 {
			log.Geec("retry electing for block", "blknum", ep.BlkNum, "version", ep.Version, "retry", retry, "state", wb.ElectState, "supporters", wb.Supporters.Size())
		} else{
			log.Gdbug("Electing for block", "blknum", ep.BlkNum, "Ncandidates", wb.NCandidates, "threshold", wb.ElectionThreshold)
		}
		retry = retry + 1

		wb.Mu.Unlock()

		for _, conn :=  range conns{
			conn.Write(buffer)
		}

		select {
		case blk := <-s.electSuccessCh:
			wb.Mu.Lock()
			if blk == ep.BlkNum  {
				if wb.MaxVersion == int64(ep.Version) {
					wb.Mu.Unlock()
					return 1
				}else{
					wb.Mu.Unlock()
					return -1
				}
			} else if blk > ep.BlkNum{
				s.electSuccessCh <- blk
				wb.Mu.Unlock()
				return -1
			} else{
				wb.Mu.Unlock()
				//This should not happen
				log.Warn("received success not for me", "received", blk, "waiting", ep.BlkNum )
				continue
			}
		case <- time.After(1 * time.Second):
			wb.Mu.Lock()
			if wb.BlkNum > ep.BlkNum{
				//already received that block
				wb.Mu.Unlock()
				return -1
			}
			if wb.ElectState == geecCore.ELEC_Voted{
				wb.Mu.Unlock()
				return -1
			}
			if wb.MaxVersion > int64(ep.Version){
				wb.Mu.Unlock()
				return -1
			}
			wb.Mu.Unlock()
			continue
		case <- stop:
			//outside stop
			log.Geec("elect stopped by signal", "blk", ep.BlkNum)
			return -1
		}

	}

}


func (s *Server) handleElectMessage() {

	wb := s.state.GetWorkingBlock()
	for wb == nil{
		log.Gdbug("wb not ready")
		time.Sleep(1 * time.Second)
		wb = s.state.GetWorkingBlock()
	}


	for {
		em := <- s.electMsgCh

		log.Gdbug("received elect message", "author", em.Author, "code", em.Code, "blk", em.BlockNum, "version", em.Version)

		wb.Mu.Lock()

		/*
		Wait until the working block catch-up
		If the message falls behind, discard it.
		 */
		ret := wb.Wait(em.BlockNum)
		if ret == geecCore.WB_PASSED{
			wb.Mu.Unlock()
			continue
		}

		if wb.MaxVersion > int64(em.Version){
			log.Geec("Discard old version elect message", "msg version", em.Version, "max version", wb.MaxVersion)
			wb.Mu.Unlock()
			continue
		}else if wb.MaxVersion < int64(em.Version){
			wb.MaxVersion = int64(em.Version)
			wb.MaxQueryRetry = -1
			wb.MaxValidateRetry = -1


			wb.ElectState = geecCore.ELEC_Candidate
			wb.Supporters.Clear()
		}

		switch em.Code{
			case MSG_ELECT:
				if wb.ElectState == geecCore.ELEC_Candidate{
					if wb.MyRand > em.Rand || (wb.MyRand == em.Rand && AddrToInt(s.coinbase) > AddrToInt(em.Author)) {
						//Discard
						log.Gdbug("not answering to elect, I have a larger rand", "blk", em.BlockNum, "my rand", wb.MyRand, "incomming rand", em.Rand)
						wb.Mu.Unlock()
						continue

					}else{
						//Vote for it.
						wb.ElectState = geecCore.ELEC_Voted
						wb.Delegator = em.Author
						wb.Delegator_Ipstr = em.Ipstring
						wb.Delegator_Port = em.Port
						s.vote(wb, em.BlockNum, em.Ipstring, em.Port, em.Version)
					}

				}else if wb.ElectState == geecCore.ELEC_Voted{
					/*
					Already voted for someone else, one scenario need to be handled.
					1) Re-send of elect message from my delegator(e.g., because of packet loss), only for the same person with same address.
					2) Re-send of message after two rounds.
					*/

					if em.Author == wb.Delegator || em.Retry > wb.Max_Election_Retry + 1 {
						log.Gdbug("received retry message from my delegator, resending votes")
						s.vote(wb, em.BlockNum, wb.Delegator_Ipstr, wb.Delegator_Port, em.Version)
						wb.Max_Election_Retry = em.Retry
					}
				}
			case MSG_VOTE:
				if wb.ElectState == geecCore.ELEC_Candidate{
					wb.Supporters.Add(em.Author)
					log.Gdbug("Received vote", "blk", em.BlockNum, "addr", em.Author, "Count", wb.Supporters.Size(), "ElectionThreshold", wb.ElectionThreshold, "(and one vote from itself)", "")
					if uint64(wb.Supporters.Size()) >= wb.ElectionThreshold{
						s.electSuccessCh <- wb.BlkNum
						wb.ElectState = geecCore.ELEC_ELECTED
					}

				}else if wb.ElectState == geecCore.ELEC_Voted{
					/*
					Already voted for some one else
					Transfer the vote to my delegator
					 */


					 //Remember this, for retry.
					wb.Supporters.Add(em.Author)




					reply := new(electMessage)
					reply.BlockNum = em.BlockNum
					reply.Code = MSG_VOTE
					reply.Author = em.Author
					reply.Version = em.Version
					//not useful
					reply.Ipstring = s.ipstr
					reply.Port = uint(s.port)


					msg := new(geecCore.GeecUDPMsg)
					msg.Code = geecCore.GeecElectMsg
					msg.Author = s.coinbase

					conn, err := net.Dial("udp", fmt.Sprintf("%s:%d", wb.Delegator_Ipstr, wb.Delegator_Port))
					if err != nil{
						log.Crit("Failed Dial while sending back reply", "err", err)
					}
					payload, err := rlp.EncodeToBytes(reply)
					if err != nil {
						log.Crit("Failed to encode elect reply")
					}
					msg.Payload = payload

					buffer, err := rlp.EncodeToBytes(msg)
					if err != nil {
						log.Crit("Failed to encode elect reply to msg")
					}
					log.Gdbug("tranferring votes", "original src", em.Author, "blk", em.BlockNum)

					conn.Write(buffer)
					conn.Close()
				}
			}
		wb.Mu.Unlock()
	}

}


func (s *Server) vote (wb *geecCore.WorkingBlock, BlockNum uint64, Ipstring string, Port uint, version uint64){
	reply := new(electMessage)
	reply.BlockNum = BlockNum
	reply.Code = MSG_VOTE
	reply.Ipstring = s.ipstr
	reply.Port = uint(s.port)
	reply.Version = version

	msg := new(geecCore.GeecUDPMsg)
	msg.Code = geecCore.GeecElectMsg
	msg.Author = s.coinbase

	log.Gdbug("Sending votes", "blk", BlockNum, "count", wb.Supporters.Size()+1, "target", fmt.Sprintf("%s:%d", Ipstring, Port))

	conn, err := net.Dial("udp", fmt.Sprintf("%s:%d", Ipstring, Port))
	defer conn.Close()
	if err != nil{
		log.Crit("Failed Dial while sending back reply", "err", err)
	}

	//vote representing myself
	reply.Author = s.coinbase
	payload, err := rlp.EncodeToBytes(reply)
	if err != nil {
		log.Crit("Failed to encode elect reply")
	}
	msg.Payload = payload

	buffer, err := rlp.EncodeToBytes(msg)
	if err != nil {
		log.Crit("Failed to encode elect reply to msg")
	}
	conn.Write(buffer)

	//vote representing my supporters
	for _, value := range(wb.Supporters.Values()) {

		addr, _ := value.(common.Address)
		reply.Author = addr
		payload, err := rlp.EncodeToBytes(reply)
		if err != nil {
			log.Crit("Failed to encode elect reply")
		}
		msg.Payload = payload

		buffer, err := rlp.EncodeToBytes(msg)
		if err != nil {
			log.Crit("Failed to encode elect reply to msg")
		}
		conn.Write(buffer)
	}
}