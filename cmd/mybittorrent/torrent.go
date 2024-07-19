package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"

	"github.com/jackpal/bencode-go"
)

const MAX_PEERS = 5
const BLOCK_SIZE = 16 * 1024

type Peer struct {
	id       string
	addr     string
	bitfield []byte
	conn     net.Conn
}

type Torrent struct {
	Meta        Meta
	InfoHash    []byte
	PieceHashes [][]byte
	Peers       []Peer
	queue       chan int
	OutputFile  *os.File
}

func NewTorrent(torrentFile string) *Torrent {
	t := &Torrent{}

	fd, err := os.Open(torrentFile)
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

	t.queue = make(chan int, len(t.PieceHashes))

	return t
}

func (t *Torrent) refreshPeers() {
	u, err := url.Parse(t.Meta.Announce)
	if err != nil {
		panic(err)
	}

	values := url.Values{}
	values.Add("info_hash", string(t.InfoHash))
	values.Add("peer_id", "02112234485866778899")
	values.Add("port", "6881")
	values.Add("uploaded", "0")
	values.Add("downloaded", "0")
	values.Add("left", strconv.Itoa(int(t.Meta.Info.Length)))
	values.Add("compact", "1")

	u.RawQuery = values.Encode()

	log.Printf("Request: %s\n", u.String())

	resp, err := http.Get(u.String())
	if err != nil {
		log.Fatalf("Cant make http request: %+v", err)
	}

	var res TrackerResponse
	err = bencode.Unmarshal(resp.Body, &res)
	if err != nil {
		panic(err)
	}
	if res.FailureReason != "" {
		panic(res.FailureReason)
	}

	t.Peers = []Peer{}
	for i := 0; i < len(res.Peers); i += 6 {
		port := binary.BigEndian.Uint16([]byte(res.Peers[i+4 : i+6]))
		addr := fmt.Sprintf("%d.%d.%d.%d:%d", res.Peers[i], res.Peers[i+1], res.Peers[i+2], res.Peers[i+3], port)
		t.Peers = append(t.Peers, Peer{addr: addr})
	}
}

func (t *Torrent) DownloadPiece(index int, output string) {
	t.queue <- index

	fd, err := os.OpenFile(output, os.O_WRONLY, 0644)
	if err != nil {
		log.Panicf("Cannot open file %q for writing: %+v", output, err)
	}
	t.OutputFile = fd
	t.waitForDownloads()
}

func (t *Torrent) DownloadFile(output string) {
	for i := range len(t.PieceHashes) {
		t.queue <- i
	}
	fd, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		log.Panicf("Cannot open file %q for writing: %+v", output, err)
	}
	t.OutputFile = fd
	t.waitForDownloads()
}

func (p *Peer) download(t *Torrent, done func()) {
	defer done()

	if p.bitfield == nil {
		log.Printf("Client %s has no bifield.\n", p.addr)
		return
	}

	p.sendInterest()
	for {
		index, found := p.nextPiece(t)
		if !found {
			log.Printf("Peer %s Nothing more to download (remaining queue: %d). Bye!\n", p.addr, len(t.queue))
			return
		}

		log.Printf("Peer %s Starting download of piece %d", p.addr, index)

		var begin uint32
		var blockSize uint32

		var pieceLen = t.Meta.Info.PieceLength
		if index == len(t.PieceHashes)-1 {
			pieceLen = t.Meta.Info.Length % t.Meta.Info.PieceLength
		}

		pieceBuf := make([]byte, pieceLen)

		for begin < pieceLen {
			blockSize = min(BLOCK_SIZE, pieceLen-begin)

			request := make([]byte, 17)
			binary.BigEndian.PutUint32(request[0:4], uint32(13))
			request[4] = MID_REQUEST
			binary.BigEndian.PutUint32(request[5:9], uint32(index))
			binary.BigEndian.PutUint32(request[9:13], begin)
			binary.BigEndian.PutUint32(request[13:17], blockSize)

			log.Printf("Peer %s -> Requesting block %d-%d of piece %d\n", p.addr, begin, begin+blockSize, index)
			_, err := p.conn.Write(request)
			if err != nil {
				t.queue <- index
				log.Panicf("Cannot write on connection to peer %q, readding piece to queue: %+v\n", p.addr, err)
			}

			log.Printf("Peer %s Waiting for block %d-%d of piece %d\n", p.addr, begin, begin+blockSize, index)
			m := p.receiveMessage()
			if m.Id != MID_PIECE {
				t.queue <- index
				log.Panicf("Peer %s Cannot receive piece message. Received %d instead, readding piece to queue.\n", p.addr, m.Id)
			}
			log.Printf("Peer %s <- Received block %d-%d of piece %d\n", p.addr, begin, begin+blockSize, index)

			// index := binary.BigEndian.Uint32(m.Payload[0:4])
			begin = binary.BigEndian.Uint32(m.Payload[4:8])
			copy(pieceBuf[begin:], m.Payload[8:])
			begin += uint32(len(m.Payload[8:]))
		}

		if !validatePiece(pieceBuf, t.PieceHashes[index]) {
			t.queue <- index
			log.Panicf("Piece %d from peer %q invalid!, readding piece to queue.\n", index, p.addr)
		}

		offset := index * int(t.Meta.Info.PieceLength)
		_, err := t.OutputFile.WriteAt(pieceBuf, int64(offset))
		if err != nil {
			t.queue <- index
			log.Panicf("Piece %d from peer %q cannot be written to file, readding piece to queue.\n", index, p.addr)
		}
	}
}

func (p *Peer) nextPiece(t *Torrent) (item int, found bool) {
	if len(t.queue) == 0 {
		return 0, false
	}

	var hasPiece bool
	startItem := -1
	for index := range t.queue {
		hasPiece = (p.bitfield[index/8] & (1 << (7 - index%8))) != 0
		if hasPiece {
			return index, true
		}

		t.queue <- index

		if startItem == -1 {
			startItem = index
		} else if startItem == index {
			return 0, false
		}
	}
	return 0, false
}

func (t *Torrent) waitForDownloads() {
	var wg sync.WaitGroup
	for _, p := range t.Peers {
		wg.Add(1)
		go p.download(t, wg.Done)
	}
	wg.Wait()
}

func (t *Torrent) connectPeers() {
	handshake := make([]byte, 68)
	copy(handshake[0:1], []byte{19})                       // length
	copy(handshake[1:20], "BitTorrent protocol")           // protocol
	copy(handshake[20:28], []byte{0, 0, 0, 0, 0, 0, 0, 0}) // reserved
	copy(handshake[28:48], t.InfoHash)                     // infoHash
	copy(handshake[48:68], "00112233445566778899")         // peerId

	var wg sync.WaitGroup
	wg.Add(len(t.Peers))
	for i, peersN := 0, 0; i < len(t.Peers) || peersN >= MAX_PEERS; i++ {
		go func() {
			var err error
			p := &t.Peers[i]
			p.conn, err = net.Dial("tcp", p.addr)
			if err != nil {
				log.Printf("Cannot connect to peer %q: %+v\n", p.addr, err)
				return
			}

			_, err = p.conn.Write(handshake)
			if err != nil {
				log.Printf("Cannot write on connection to peer %q: %+v\n", p.addr, err)
				return
			}

			handshakeBuf := make([]byte, 68)
			_, err = io.ReadFull(p.conn, handshakeBuf)
			if err != nil {
				log.Printf("Cannot read from connection to peer %q: %+v\n", p.addr, err)
				return
			}
			p.id = hex.EncodeToString(handshakeBuf[48:68])

			m := p.receiveMessage()
			if m.Id != MID_BITFIELD {
				log.Printf("Expected bitfield message from peer %q, received %d\n", p.addr, m.Id)
				return
			}
			p.bitfield = m.Payload
			peersN++

			log.Printf("Peer %s connected (id: %s)\n", p.addr, p.id)
			wg.Done()
		}()
	}
	wg.Wait()
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

func (p *Peer) receiveMessage() *Message {
	for {
		lengthBuf := make([]byte, 4)
		_, err := io.ReadFull(p.conn, lengthBuf)
		if err != nil {
			log.Panicf("Cannot read length prefix from connection with client %s: %+v", p.addr, err)
		}
		length := binary.BigEndian.Uint32(lengthBuf)

		// log.Printf("Peer %s <- Length prefix %d received\n", p.addr, length)

		// ignore keepalive
		if length == 0 {
			continue
		}

		payloadBuf := make([]byte, length)
		_, err = io.ReadFull(p.conn, payloadBuf)
		if err != nil {
			log.Panicf("Cannot read payload from connection with client %s: %+v", p.addr, err)
		}

		return &Message{
			Id:      payloadBuf[0],
			Payload: payloadBuf[1:],
		}
	}
}

func (p *Peer) sendInterest() {
	log.Printf("Peer %s -> Sending interest\n", p.addr)
	_, err := p.conn.Write([]byte{0, 0, 0, 1, MID_INTERESTED})
	if err != nil {
		log.Panicf("Cannot write on connection to peer %q: %+v\n", p.addr, err)
	}

	m := p.receiveMessage()
	if m.Id != MID_UNCHOKE {
		log.Panicf("Cannot receive unchoke message from peer %q. Received %d instead\n", p.addr, m.Id)
	}
	log.Printf("Peer %s <- Unchocked message received\n", p.addr)
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
