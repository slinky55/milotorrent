package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/jackpal/bencode-go"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
)

const ShaDigestLen = 20

type Peer struct {
	IP   net.IP
	Port uint16
}

func (p *Peer) ToString() string {
	return p.IP.String() + ":" + strconv.Itoa(int(p.Port))
}

func ParsePeers(pStr string) ([]Peer, error) {
	const peerSize = 6 // 4 for IP, 2 for port
	p := []byte(pStr)
	numPeers := len(p) / peerSize
	if len(p)%peerSize != 0 {
		err := fmt.Errorf("received malformed peers")
		return nil, err
	}
	peers := make([]Peer, numPeers)
	for i := 0; i < numPeers; i++ {
		offset := i * peerSize
		peers[i].IP = p[offset : offset+4]
		peers[i].Port = binary.BigEndian.Uint16(p[offset+4 : offset+6])
	}
	return peers, nil
}

type TorrentFile struct {
	Interval    uint
	Peers       []Peer
	Announce    string
	InfoHash    [ShaDigestLen]byte
	PieceHashes [][ShaDigestLen]byte
	PieceLength int
	Length      int
	Name        string
}

func (t *TorrentFile) Print() {
	fmt.Println("Announce: ", t.Announce)
	fmt.Println("Info Hash: ", t.InfoHash)
	fmt.Println("File Size: ", t.Length)
	fmt.Println("Piece Size: ", t.PieceLength)
	for i := 0; i < len(t.PieceHashes); i++ {
		fmt.Println("Piece ", i, " Hash: ", t.PieceHashes[i])
	}
}

func (t *TorrentFile) BuildTrackerURL(peerID [ShaDigestLen]byte, port uint16) (string, error) {
	base, err := url.Parse(t.Announce)
	if err != nil {
		return "", err
	}
	params := url.Values{
		"info_hash":  []string{string(t.InfoHash[:])},
		"peer_id":    []string{string(peerID[:])},
		"port":       []string{strconv.Itoa(int(port))},
		"uploaded":   []string{"0"},
		"downloaded": []string{"0"},
		"compact":    []string{"1"},
		"left":       []string{strconv.Itoa(t.Length)},
	}
	base.RawQuery = params.Encode()
	return base.String(), nil
}

type BencodeInfo struct {
	Pieces      string `bencode:"pieces"`
	PieceLength int    `bencode:"piece length"`
	Length      int    `bencode:"length"`
	Name        string `bencode:"name"`
}

func (info *BencodeInfo) Hash() ([ShaDigestLen]byte, error) {
	var buf bytes.Buffer
	err := bencode.Marshal(&buf, *info)
	if err != nil {
		return [ShaDigestLen]byte{}, err
	}
	h := sha1.Sum(buf.Bytes())
	return h, nil
}

type BencodeTorrent struct {
	Announce string      `bencode:"announce"`
	Info     BencodeInfo `bencode:"info"`
}

func main() {
	filePath := "debian-12.0.0-amd64-netinst.iso.torrent"

	// Open the file and get the file handle (os.File)
	file, err := os.Open(filePath)
	if err != nil {
		fmt.Println("Error opening file:", err)
		return
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			log.Println("Error closing torrent file: ", err)
			return
		}
	}(file)

	torrentBencode, err := Open(file)

	if err != nil {
		fmt.Println("Error parsing file: ", err)
		return
	}

	torrentFile, err := BencodeToTorrent(torrentBencode)

	if err != nil {
		fmt.Println("Error converting bencode struct: ", err)
		return
	}

	peerID, err := GenerateRandomID()
	trackerURL, err := torrentFile.BuildTrackerURL(peerID, 6969)

	// Announce
	torrentFile.Peers, torrentFile.Interval, err = Announce(peerID, trackerURL)
	if err != nil {
		log.Println(err)
	}

	// Printing Peers
	//log.Println("Printing ", len(torrentFile.Peers), " Peers...\n")
	//
	//for i := 0; i < len(torrentFile.Peers); i++ {
	//	log.Println(torrentFile.Peers[i].ToString())
	//}

	workQ := make(chan *Work, len(torrentFile.PieceHashes)) //var conn net.Conn

	//for i := 0; i < len(torrentFile.Peers); i++ {
	//	var peer Peer
	//	peer = torrentFile.Peers[i]
	//
	//	log.Println("Attempting: ", peer.ToString())
	//
	//	conn, err = net.DialTimeout("tcp", peer.ToString(), 3*time.Second)
	//	if err != nil {
	//		log.Println("Failed to connect to client: ", err)
	//		continue
	//	}
	//	conn.SetDeadline(time.Now().Add(3 * time.Second))
	//
	//	newHandshake := handshake.New(torrentFile.InfoHash, peerID)
	//
	//	_, err := conn.Write(newHandshake.Serialize())
	//	if err != nil {
	//		log.Println("Failed to send handshake to peer: ", peer.ToString())
	//		conn.Close()
	//		continue
	//	}
	//
	//	res, err := handshake.Deserialize(conn)
	//	if err != nil {
	//		log.Println("Failed to read handshake response: ", err)
	//		conn.Close()
	//		continue
	//	}
	//
	//	if !bytes.Equal(res.InfoHash[:], newHandshake.InfoHash[:]) {
	//		log.Println("Recieved incorrect info hash from peer, handshake failed")
	//		conn.Close()
	//		continue
	//	}
	//
	//	log.Println("Handshake successfull with: ", peer.IP)
	//
	//	err = conn.Close()
	//	err = conn.SetDeadline(time.Time{})
	//}
}

// Open parses a torrent file
func Open(r io.Reader) (*BencodeTorrent, error) {
	bto := BencodeTorrent{}
	err := bencode.Unmarshal(r, &bto)
	if err != nil {
		return nil, err
	}
	return &bto, nil
}

func BencodeToTorrent(b *BencodeTorrent) (*TorrentFile, error) {
	torrentFile := TorrentFile{}

	torrentFile.Announce = b.Announce
	torrentFile.Length = b.Info.Length
	torrentFile.Name = b.Info.Name
	torrentFile.PieceLength = b.Info.PieceLength

	piecesBytes := []byte(b.Info.Pieces)

	for idx := 0; idx < len(piecesBytes); idx += ShaDigestLen {
		end := idx + ShaDigestLen

		piece := piecesBytes[idx:end]

		var hash [ShaDigestLen]byte
		copy(hash[:], piece)

		torrentFile.PieceHashes = append(torrentFile.PieceHashes, hash)
	}

	hash, err := b.Info.Hash()
	if err != nil {
		log.Println("Error hashing info dict")
		return nil, err
	}

	torrentFile.InfoHash = hash

	return &torrentFile, nil
}

func GenerateRandomID() ([ShaDigestLen]byte, error) {
	byteArray := [ShaDigestLen]byte{}

	if _, err := rand.Read(byteArray[:]); err != nil {
		fmt.Println("Error generating random bytes:", err)
		return [ShaDigestLen]byte{}, err
	}

	return byteArray, nil
}

func Announce(pid [20]byte, url string) ([]Peer, uint, error) {
	var peers []Peer
	var interval uint

	response, err := http.Get(url)
	if err != nil {
		fmt.Println("Error making GET request")
		return nil, 0, err
	}

	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		type AnnounceResponse struct {
			Interval int    `bencode:"interval"`
			Peers    string `bencode:"peers"`
		}

		announceRes := AnnounceResponse{}
		err := bencode.Unmarshal(response.Body, &announceRes)
		if err != nil {
			log.Println("Error parsing bencode response")
			return nil, 0, err
		}

		interval = uint(announceRes.Interval)
		peers, err = ParsePeers(announceRes.Peers)
		if err != nil {
			return nil, 0, err
		}
	} else {
		return nil, 0, errors.New("Announce failed with status " + strconv.Itoa(response.StatusCode))
	}

	return peers, interval, nil
}
