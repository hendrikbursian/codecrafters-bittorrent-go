package main

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	bencode "github.com/jackpal/bencode-go" // Available if you need it!
)

func main() {
	command := os.Args[1]

	if command == "decode" {
		reader := strings.NewReader(os.Args[2])
		decoded, err := bencode.Decode(reader)
		if err != nil {
			log.Fatalln(err)
		}

		jsonOutput, _ := json.Marshal(decoded)
		fmt.Println(string(jsonOutput))
	} else if command == "info" {
		file := os.Args[2]
		fd, err := os.Open(file)
		if err != nil {
			log.Fatalln(err)
		}

		decoded, err := bencode.Decode(fd)
		if err != nil {
			log.Fatalf("Error reading torrent file: %+v", err)
		}

		m := decoded.(map[string]any)
		info := m["info"].(map[string]any)

		fmt.Printf("Tracker URL: %s\n", m["announce"])
		fmt.Printf("Length: %d\n", info["length"])

		h := sha1.New()
		bencode.Marshal(h, info)
		sum := h.Sum([]byte{})

		fmt.Printf("Info hash: %x\n", sum)
	} else {
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
