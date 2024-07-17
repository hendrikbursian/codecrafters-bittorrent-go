package main

import (
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	bencode "github.com/jackpal/bencode-go" // Available if you need it!
)

type TrackerResponse struct {
	FailureReason string `bencode:"failure reason"`
	Interval      int64  `bencode:"interval"`
	Peers         string `bencode:"peers"`
}

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
		fmt.Printf("Piece Length: %d\n", meta.Info.PieceLength)
		fmt.Printf("Piece Hashes:\n")
		for i := 0; i < len(meta.Info.Pieces); i += 20 {
			fmt.Printf("%x\n", meta.Info.Pieces[i:i+20])
		}

		u, err := url.Parse(meta.Announce)
		if err != nil {
			panic(err)
		}

		values := url.Values{}
		values.Add("info_hash", string(infoHash))
		values.Add("peer_id", "00112233445566778899")
		values.Add("port", "6881")
		values.Add("uploaded", "0")
		values.Add("downloaded", "0")
		values.Add("left", strconv.Itoa(int(meta.Info.Length)))
		values.Add("compact", "1")

		u.RawQuery = values.Encode()

		log.Printf("Request: %s\n", u.String())

		resp, err := http.Get(u.String())
		if err != nil {
			panic(err)
		}

		var res TrackerResponse
		err = bencode.Unmarshal(resp.Body, &res)
		if err != nil {
			panic(err)
		}
		if res.FailureReason != "" {
			panic(res.FailureReason)
		}

		peers := []string{}
		for i := 0; i < len(res.Peers); i += 6 {
			port := int(res.Peers[i+4])<<8 + int(res.Peers[i+5])
			peer := fmt.Sprintf("%d.%d.%d.%d:%d", res.Peers[i], res.Peers[i+1], res.Peers[i+2], res.Peers[i+3], port)

			peers = append(peers, peer)
		}

		for _, p := range peers {
			fmt.Println(p)
		}

	} else {
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
