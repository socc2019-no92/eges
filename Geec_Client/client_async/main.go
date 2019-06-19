package main

import (
	"fmt"
	"log"
	"net"
	"strconv"
	"time"
)

func main(){
	c, err := net.Dial("udp", "0.0.0.0:3333")
	if err != nil{
		log.Fatal("Failed to dial server")
	}
	defer c.Close()

	var req uint64

	for {
		req = req+1
		input := strconv.FormatUint(req, 10)
		_, err = c.Write([]byte(input))
		if err != nil{
			log.Fatal("Failed to write to socket")
		}
		fmt.Println(input)
		time.Sleep(10 * time.Millisecond)
	}
}
