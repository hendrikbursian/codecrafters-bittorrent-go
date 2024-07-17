package main

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	bencode "github.com/jackpal/bencode-go" // Available if you need it!
)

type Meta struct {
	Announce string   `bencode:"announce"`
	Info     MetaInfo `bencode:"info"`
}
type MetaInfo struct {
	Name        string `bencode:"name"`
	Pieces      string `bencode:"pieces"`
	Length      int64  `bencode:"length"`
	PieceLength int64  `bencode:"piece length"`
}

func main() {
	command := os.Args[1]

	if command == "decode" {
		reader := strings.NewReader(os.Args[2])
		decoded, err := bencode.Decode(reader)
		if err != nil {
			panic(err)
		}

		jsonOutput, _ := json.Marshal(decoded)
		fmt.Println(string(jsonOutput))
	} else if command == "info" {
		file := os.Args[2]
		fd, err := os.Open(file)
		if err != nil {
			panic(err)
		}

		var meta Meta
		if err := bencode.Unmarshal(fd, &meta); err != nil {
			panic(err)
		}

		h := sha1.New()
		if err := bencode.Marshal(h, meta.Info); err != nil {
			panic(err)
		}
		infoHash := h.Sum(nil)

		fmt.Printf("Tracker URL: %s\n", meta.Announce)
		fmt.Printf("Length: %d\n", meta.Info.Length)
		fmt.Printf("Info hash: %x\n", infoHash)
	} else {
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
