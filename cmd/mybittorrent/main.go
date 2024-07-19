package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	bencode "github.com/jackpal/bencode-go"
)

const BLOCK_SIZE = uint32(16 * 1024)

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

type Torrent struct {
	Meta        Meta
	InfoHash    []byte
	PieceHashes [][]byte
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

		t := getTorrentInfo(torrentFile)
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

		t := getTorrentInfo(torrentFile)
		peers := getPeers(t)
		for _, p := range peers {
			fmt.Println(p)
		}
	case "handshake":
		fs := flag.NewFlagSet("handshake", flag.ExitOnError)
		fs.Parse(os.Args[2:])
		torrentFile := fs.Arg(0)
		addr := flag.Arg(1)

		t := getTorrentInfo(torrentFile)
		_, id := doHandshake(t, addr)

		fmt.Printf("Peer ID: %s\n", id)
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

		t := getTorrentInfo(torrentFile)
		peers := getPeers(t)
		conn, id := doHandshake(t, peers[1])
		log.Printf("Connected to peer: %s", id)

		m := getMessage(conn)
		if m.Id != MID_BITFIELD {
			log.Fatalf("expected bitfield message, received %d\n", m.Id)
		}
		log.Printf("Received bitfield: %b\n", m.Payload)

		m = Message{Id: MID_INTERESTED}
		_, err = conn.Write(m.Bytes())
		if err != nil {
			log.Fatalf("cant write to connection: %+v\n", err)
		}

		m = getMessage(conn)
		if m.Id != MID_UNCHOKE {
			log.Fatalf("expected unchoke message, received %d\n", m.Id)
		}
		log.Printf("Received unchoke message\n")

		pieceLength := (t.Meta.Info.Length - ((uint32(pieceNumber) + 1) * t.Meta.Info.PieceLength)) % t.Meta.Info.PieceLength
		piece := downloadPiece(conn, uint32(pieceNumber), uint32(pieceLength))

		h := sha1.New()
		h.Write(piece)
		hash := h.Sum(nil)
		if bytes.Compare(hash, t.PieceHashes[pieceNumber]) != 0 {
			log.Fatalf("Hashsum of piece invalid! Got=%x, Want=%x", h, t.PieceHashes[pieceNumber])
		}

		log.Printf("Received piece, writing to file %s", *output)
		fd, err := os.OpenFile(*output, os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatalf("Cannot open file: %+v", err)
		}
		_, err = fd.WriteAt(piece, int64(pieceNumber)*int64(BLOCK_SIZE))
		if err != nil {
			log.Fatalf("Cannot write file: %+v", err)
		}
	case "download":
		fs := flag.NewFlagSet("download", flag.ExitOnError)
		output := fs.String("o", "", "")
		fs.Parse(os.Args[2:])
		torrentFile := fs.Arg(0)
		if output == nil {
			log.Fatal("Output file not given")
		}

		t := getTorrentInfo(torrentFile)
		peers := getPeers(t)
		conn, id := doHandshake(t, peers[1])
		log.Printf("Connected to peer: %s", id)

		m := getMessage(conn)
		if m.Id != MID_BITFIELD {
			log.Fatalf("expected bitfield message, received %d\n", m.Id)
		}
		log.Printf("Received bitfield: %b\n", m.Payload)

		m = Message{Id: MID_INTERESTED}
		_, err := conn.Write(m.Bytes())
		if err != nil {
			log.Fatalf("cant write to connection: %+v\n", err)
		}

		m = getMessage(conn)
		if m.Id != MID_UNCHOKE {
			log.Fatalf("expected unchoke message, received %d\n", m.Id)
		}
		log.Printf("Received unchoke message\n")

		var fd *os.File
		var pieceLength uint32 = t.Meta.Info.PieceLength
		for i, hash := range t.PieceHashes {
			if i == len(t.PieceHashes)-1 {
				pieceLength = t.Meta.Info.Length % t.Meta.Info.PieceLength
			}
			piece := downloadPiece(conn, uint32(i), uint32(pieceLength))
			if !validatePiece(piece, hash) {
				log.Fatalf("Piece %d invalid!\n", i)
			}

			if fd == nil {
				fd, err = os.OpenFile(*output, os.O_CREATE|os.O_WRONLY, 0644)
				if err != nil {
					log.Fatalf("Cannot open file: %+v\n", err)
				}
			}

			log.Printf("Received piece %d, writing to file %s\n", i, *output)
			_, err = fd.WriteAt(piece, int64(i)*int64(BLOCK_SIZE))
			if err != nil {
				log.Fatalf("Cannot write file: %+v\n", err)
			}
		}
		log.Printf("Downloaded %s to %s\n", torrentFile, *output)
	default:
		fmt.Println("Unknown command: " + command)
		os.Exit(1)
	}
}

func downloadPiece(conn net.Conn, index uint32, pieceLength uint32) []byte {
	messages := make(chan []byte, 5)
	defer close(messages)
	go func() {
		begin := uint32(0)
		for begin < pieceLength {
			m := make([]byte, 17)
			copy(m[0:5], []byte{0, 0, 0, 13, MID_REQUEST})
			binary.BigEndian.PutUint32(m[5:9], index)                                // index
			binary.BigEndian.PutUint32(m[9:13], begin)                               // begin
			binary.BigEndian.PutUint32(m[13:17], min(BLOCK_SIZE, pieceLength-begin)) // length
			messages <- m
			begin += BLOCK_SIZE
		}
	}()

	piece := make([]byte, pieceLength)
	downloaded := uint32(0)
	for downloaded < pieceLength {
		select {
		case m := <-messages:
			_, err := conn.Write(m)
			if err != nil {
				log.Fatalf("cant write to connection: %+v\n", err)
			}
			message := getMessage(conn)
			if message.Id != MID_PIECE {
				log.Fatalf("expected piece message, received %d\n", message.Id)
			}
			index := binary.BigEndian.Uint32(message.Payload[0:4])
			begin := binary.BigEndian.Uint32(message.Payload[4:8])

			payloadSize := uint32(len(message.Payload[8:]))
			downloaded += payloadSize

			log.Printf("Received block: idx (%d) begin (%d) blocksize (%d) downloaded (%d) progress: %.2f%%\n", index, begin, payloadSize, downloaded, float64(downloaded)/float64(pieceLength)*100)
			copy(piece[begin:], message.Payload[8:])
		}
	}
	return piece
}

func getMessage(conn net.Conn) Message {
	lengthBuf := make([]byte, 4)
	_, err := io.ReadFull(conn, lengthBuf)
	if err != nil {
		log.Fatalf("cant read length prefix from connection: %+v", err)
	}

	length := binary.BigEndian.Uint32(lengthBuf)
	payload := make([]byte, length)
	_, err = io.ReadFull(conn, payload)
	if err != nil {
		log.Fatalf("cant read payload from connection: %+v", err)
	}

	return Message{
		Id:      payload[0],
		Payload: payload[1:],
	}
}

const (
	MID_CHOKE          byte = 0
	MID_UNCHOKE        byte = 1
	MID_INTERESTED     byte = 2
	MID_NOT_INTERESTED byte = 3
	MID_HAVE           byte = 4
	MID_BITFIELD       byte = 5
	MID_REQUEST        byte = 6
	MID_PIECE          byte = 7
	MID_CANCEL         byte = 8
)

type Message struct {
	Id      byte
	Payload []byte
}

func (m *Message) Bytes() []byte {
	length := 1 + len(m.Payload)
	buf := make([]byte, 4+length)
	binary.BigEndian.PutUint32(buf[0:4], uint32(length))
	buf[4] = byte(m.Id)
	copy(buf[5:], m.Payload)
	return buf
}

func listenForMessages(conn net.Conn) chan Message {
	ch := make(chan Message, 2)

	go func() {
		lengthBuf := make([]byte, 4)
		for {
			_, err := io.ReadFull(conn, lengthBuf)
			if err != nil {
				log.Fatalf("error reading from connection: %+v", err)
				return
			}

			length := binary.BigEndian.Uint32(lengthBuf)

			// ignore keepalive messages
			if length == 0 {
				continue
			}

			payloadBuf := make([]byte, length)
			_, err = io.ReadFull(conn, payloadBuf)
			if err != nil {
				log.Fatalf("error reading from connection: %+v", err)
			}

			ch <- Message{
				Id:      payloadBuf[0],
				Payload: payloadBuf[1:],
			}
		}
	}()

	return ch
}

func getTorrentInfo(file string) Torrent {
	t := Torrent{}

	fd, err := os.Open(file)
	if err != nil {
		panic(err)
	}

	if err := bencode.Unmarshal(fd, &t.Meta); err != nil {
		panic(err)
	}

	h := sha1.New()
	if err := bencode.Marshal(h, t.Meta.Info); err != nil {
		panic(err)
	}
	t.InfoHash = h.Sum(nil)

	t.PieceHashes = [][]byte{}
	for i := 0; i < len(t.Meta.Info.Pieces); i += 20 {
		t.PieceHashes = append(t.PieceHashes, []byte(t.Meta.Info.Pieces[i:i+20]))
	}
	return t
}

func getPeers(t Torrent) []string {
	u, err := url.Parse(t.Meta.Announce)
	if err != nil {
		panic(err)
	}

	values := url.Values{}
	values.Add("info_hash", string(t.InfoHash))
	values.Add("peer_id", "00112233445566778899")
	values.Add("port", "6881")
	values.Add("uploaded", "0")
	values.Add("downloaded", "0")
	values.Add("left", strconv.Itoa(int(t.Meta.Info.Length)))
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
		// port := int(res.Peers[i+4])<<8 + int(res.Peers[i+5])
		port := binary.BigEndian.Uint16([]byte(res.Peers[i+4 : i+6]))
		peer := fmt.Sprintf("%d.%d.%d.%d:%d", res.Peers[i], res.Peers[i+1], res.Peers[i+2], res.Peers[i+3], port)

		peers = append(peers, peer)
	}

	return peers
}

func doHandshake(t Torrent, addr string) (conn net.Conn, id string) {
	handshake := make([]byte, 68)
	copy(handshake[0:1], []byte{19})                       // length
	copy(handshake[1:20], "BitTorrent protocol")           // protocol
	copy(handshake[20:28], []byte{0, 0, 0, 0, 0, 0, 0, 0}) // reserved
	copy(handshake[28:48], t.InfoHash)                     // infoHash
	copy(handshake[48:68], "00112233445566778899")         // peerId

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		panic(err)
	}

	_, err = conn.Write(handshake)
	if err != nil {
		panic(err)
	}

	buf := make([]byte, 68)
	_, err = conn.Read(buf)
	if err != nil && err != io.EOF {
		panic(err)
	}

	return conn, hex.EncodeToString(buf[48:])
}

func validatePiece(piece []byte, expected []byte) bool {
	h := sha1.New()
	_, err := h.Write(piece)
	if err != nil {
		log.Fatalf("Cant write hash: %+v", err)
	}
	hash := h.Sum(nil)
	return bytes.Compare(hash, expected) == 0
}
