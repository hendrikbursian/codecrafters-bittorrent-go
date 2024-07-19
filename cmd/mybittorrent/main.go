package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	bencode "github.com/jackpal/bencode-go"
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
	Length      uint32 `bencode:"length"`
	PieceLength uint32 `bencode:"piece length"`
}

func main() {
	command := os.Args[1]

	switch command {
	case "decode":
		fs := flag.NewFlagSet("decode", flag.ExitOnError)
		fs.Parse(os.Args[2:])
		torrentFile := fs.Arg(0)

		reader := strings.NewReader(torrentFile)
		decoded, err := bencode.Decode(reader)
		if err != nil {
			panic(err)
		}

		jsonOutput, _ := json.Marshal(decoded)
		fmt.Println(string(jsonOutput))
	case "info":
		fs := flag.NewFlagSet("info", flag.ExitOnError)
		fs.Parse(os.Args[2:])
		torrentFile := fs.Arg(0)

		t := NewTorrent(torrentFile)
		fmt.Printf("Tracker URL: %s\n", t.Meta.Announce)
		fmt.Printf("Length: %d\n", t.Meta.Info.Length)
		fmt.Printf("Info hash: %x\n", t.InfoHash)
		fmt.Printf("Piece Length: %d\n", t.Meta.Info.PieceLength)
		fmt.Printf("Piece Hashes:\n")
		for _, p := range t.PieceHashes {
			fmt.Printf("%x\n", p)
		}
	case "peers":
		fs := flag.NewFlagSet("peers", flag.ExitOnError)
		fs.Parse(os.Args[2:])
		torrentFile := fs.Arg(0)

		t := NewTorrent(torrentFile)
		t.refreshPeers()
		for _, p := range t.Peers {
			fmt.Println(p)
		}
	case "handshake":
		fs := flag.NewFlagSet("handshake", flag.ExitOnError)
		fs.Parse(os.Args[2:])
		torrentFile := fs.Arg(0)
		addr := flag.Arg(1)

		t := NewTorrent(torrentFile)
		t.refreshPeers()
		t.connectPeers()
		for _, p := range t.Peers {
			if p.addr == addr {
				fmt.Printf("Peer ID: %s\n", p.id)
				break
			}
		}
	case "download_piece":
		fs := flag.NewFlagSet("download_piece", flag.ExitOnError)
		output := fs.String("o", "", "")
		fs.Parse(os.Args[2:])
		torrentFile := fs.Arg(0)
		pieceNumberStr := fs.Arg(1)
		if output == nil {
			log.Fatal("Output file not given")
		}

		pieceNumber, err := strconv.Atoi(pieceNumberStr)
		if err != nil {
			log.Fatalf("piecenumber not a number: %+v", err)
		}

		t := NewTorrent(torrentFile)
		t.refreshPeers()
		t.connectPeers()
		t.DownloadPiece(pieceNumber, *output)
	case "download":
		fs := flag.NewFlagSet("download", flag.ExitOnError)
		output := fs.String("o", "", "")
		fs.Parse(os.Args[2:])
		torrentFile := fs.Arg(0)
		if output == nil {
			log.Fatal("Output file not given")
		}

		t := NewTorrent(torrentFile)
		t.refreshPeers()
		t.connectPeers()
		t.DownloadFile(*output)
		fmt.Println("Done")
	default:
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}
