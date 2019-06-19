package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)


func main() {
	var port = "0.0.0.0:9001"

	conn, err := net.Dial("tcp", port)
	if err != nil {
		fmt.Println("ERROR", err)
		os.Exit(1)
	}

	response := bufio.NewReader(conn)
	for {

		input := make([]byte, 409600)
		for i:=0; i<409599; i++{
			input[i] = 0x65
		}
		input[409599] = '\n'


		t1 := time.Now()
		conn.Write(input)



		serverLine, err := response.ReadBytes(byte('\n'))
		switch err {
		case nil:
			//fmt.Print(string(serverLine))
			fmt.Println("received", len(serverLine), "time=",  time.Since(t1).String())
		case io.EOF:
			os.Exit(0)
		default:
			fmt.Println("ERROR", err)
			os.Exit(2)
		}
	}
}
