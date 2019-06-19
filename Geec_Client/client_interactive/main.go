package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
)

func main(){
	c, err := net.Dial("udp", "0.0.0.0:3333")
	if err != nil{
		log.Fatal("Failed to dial server")
	}
	defer c.Close()

	reader := bufio.NewReader(os.Stdin)

	count := 0

	for {
		count++
		fmt.Printf("[%d] Input your data: ", count)
		input, err := reader.ReadString('\n')
		if err != nil {
			log.Fatal("Failed to read from stdin")
		}

		_, err = c.Write([]byte(input))
		if err != nil{
			log.Fatal("Failed to write to socket")
		}
	}
}
